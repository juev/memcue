package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// dreamRunTimeout bounds the model call of one dream: a packet of 40 fresh facts, 50 to expand, 40 to translate and
// 30 groups is about 80,000 characters in and up to 30,000 tokens out.
const dreamRunTimeout = 10 * time.Minute

// dreamAllPasses bounds dream -run -all: a base of 2,800 facts behind on everything takes about 70 passes. A base
// larger than that continues where the bound stopped it on the next run.
var dreamAllPasses = 200

// dreamRunFn is the model call of dream -run, replaced in tests.
var dreamRunFn = func(packet string) (extract.DreamReply, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dreamRunTimeout)
	defer cancel()
	return extract.DreamAnswer(ctx, packet)
}

// dreamAllStalls is how many passes in a row may change nothing before dream -run -all gives up: a fact owed a
// translation waits while a group of the packet holds it (#396), and the merge sweep moves the groups on.
const dreamAllStalls = 3

// runDreamOnce is memcue dream -run. A plain run (#401) is the dream of a host whose plugin has no session model to
// hand the packet to, such as Hermes, which starts it after an extraction: it asks what the session start of Claude
// Code asks before it says a dream is due, whether MEMCUE_DREAM_EVERY has passed and some fact is fresh or owed a
// translation, and with nothing due makes no call. It needs MEMCUE_API_URL, so a long run never starts by surprise.
//
// With all (#405) it finishes the base: no interval, packets one after another while a packet has facts to split,
// expand or translate or some fact still lacks a translation. The merge sweep walks the base in a circle and never
// runs out of groups, so it does not keep the loop going. It runs on the endpoint when MEMCUE_API_URL is set and on
// claude -p otherwise, and stops on the first failed pass, after dreamAllStalls passes that change nothing, or after
// dreamAllPasses.
func runDreamOnce(out io.Writer, all bool) error {
	if !all && extract.API.URL == "" {
		return fmt.Errorf("dream -run: %w", extract.ErrNoEndpoint)
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	if !all {
		fresh, owed, _, err := dreamDue(dir, dreamEveryFromEnv(dreamEvery))
		if err != nil {
			return err
		}
		if fresh == 0 && owed == 0 {
			_, err := fmt.Fprintln(out, "dream: nothing due")
			return err
		}
	}
	unlock, err := lockDream(dir)
	if err != nil {
		return err
	}
	defer unlock()
	if !all {
		_, _, err := dreamPass(dir, out)
		return err
	}
	stalls := 0
	for pass := 1; pass <= dreamAllPasses; pass++ {
		if _, err := fmt.Fprintf(out, "dream -all: pass %d\n", pass); err != nil {
			return err
		}
		rec, work, err := dreamPass(dir, out)
		if err != nil {
			return fmt.Errorf("dream -all: pass %d: %w", pass, err)
		}
		if rec.Packet == "" { // nothing for the dream
			_, err := fmt.Fprintf(out, "dream -all: done after %d passes\n", pass)
			return err
		}
		owed, err := owedTranslations()
		if err != nil {
			return err
		}
		if rec.Translated+rec.Expanded+len(rec.Splits)+len(rec.Actions) == 0 {
			stalls++
		} else {
			stalls = 0
		}
		switch {
		case work == 0 && owed == 0:
			_, err := fmt.Fprintf(out, "dream -all: done after %d passes\n", pass)
			return err
		case stalls >= dreamAllStalls:
			_, err := fmt.Fprintf(out, "dream -all: stopped after %d passes that changed nothing; %d facts still "+
				"lack a translation\n", stalls, owed)
			return err
		}
	}
	return fmt.Errorf("dream -all: stopped after %d passes with work left; run it again", dreamAllPasses)
}

// owedTranslations counts the active facts that have no translation yet.
func owedTranslations() (int, error) {
	s, err := store.Open(factsPath())
	if err != nil {
		return 0, err
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range active {
		if owesTranslation(f) {
			n++
		}
	}
	return n, nil
}

// dreamPass is one dream under the caller's lock: the default packet, since the last dream with the facts it saw, or
// every active fact on a machine with no dream yet; then the model's answer, applied under the -apply checks, and a
// line in dream.log either way. It returns that line and how many facts the packet gave to split, expand or
// translate; a record without a packet means there was nothing for the dream.
func dreamPass(dir string, out io.Writer) (dreamRecord, int, error) {
	var from time.Time
	var seen map[string]int
	if last, ok := lastDream(dir); ok {
		from = last
		var err error
		if seen, err = readDreamSeen(dir); err != nil {
			return dreamRecord{}, 0, err
		}
	}
	var packet bytes.Buffer
	start := time.Now()
	rec, err := prepare(dir, from, seen, false, &packet)
	timing := dreamTiming{Prepare: ms(time.Since(start))}
	if err != nil || rec.Packet == "" {
		rec.Timing = &timing
		if err != nil {
			rec.Error = err.Error()
		} else if _, err := io.Copy(out, &packet); err != nil {
			return rec, 0, err
		}
		return rec, 0, errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
	}
	b, err := os.ReadFile(filepath.Join(dir, packetFile))
	if err != nil {
		return rec, 0, err
	}
	var p dreamPacket
	if err := json.Unmarshal(b, &p); err != nil {
		return rec, 0, fmt.Errorf("%s: %w", packetFile, err)
	}
	work := len(p.Split) + len(p.Expand) + len(p.Translate)
	start = time.Now()
	r, err := dreamRunFn(packet.String())
	timing.Model, timing.API, timing.TTFT = ms(time.Since(start)), ms(r.API), ms(r.TTFT)
	if err != nil {
		rec.Tokens, rec.CostUSD, rec.Timing, rec.Error = &r.Tokens, r.CostUSD, &timing, "model: "+err.Error()
		return rec, work, errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
	}
	// The answer stays next to its packet: what the model said is the record a refused action is checked by.
	path := filepath.Join(dir, "dream.answer.json")
	if err := os.WriteFile(path, r.Answer, 0o600); err != nil {
		return rec, work, err
	}
	start = time.Now()
	rec, err = applyAnswer(dir, path, out)
	timing.Apply = ms(time.Since(start))
	rec.Tokens, rec.CostUSD, rec.Timing = &r.Tokens, r.CostUSD, &timing
	if err != nil {
		rec.Error = err.Error()
	}
	return rec, work, errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
}

// dreamTiming is where the time of one dream -run pass went (#471), in milliseconds: the packet, the model call as
// memcue waits for it, the part of that call `claude -p` reports as the API's and as the wait for the first token,
// and the apply. A packet with nothing to ask has only Prepare.
type dreamTiming struct {
	Prepare int64 `json:"prepare_ms"`
	Model   int64 `json:"model_ms,omitempty"`
	API     int64 `json:"api_ms,omitempty"`
	TTFT    int64 `json:"ttft_ms,omitempty"`
	Apply   int64 `json:"apply_ms,omitempty"`
}

func ms(d time.Duration) int64 { return d.Milliseconds() }
