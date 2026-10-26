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
	"slices"
	"sync"
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
func runDreamOnce(out io.Writer, all bool, packets int) error {
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
		_, _, err := dreamPass(dir, packets, out)
		return err
	}
	stalls := 0
	for pass := 1; pass <= dreamAllPasses; pass++ {
		if _, err := fmt.Fprintf(out, "dream -all: pass %d\n", pass); err != nil {
			return err
		}
		rec, work, err := dreamPass(dir, packets, out)
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
//
// The work of up to packets packets is cut into calls the model answers at once (#471): each packet's fresh facts and
// groups, each packet's facts to expand, and each packet's facts to translate. Most of a call's time is the model
// writing its answer, so three calls of a third of it each take about a third of the time. Their answers are joined
// and applied as one.
func dreamPass(dir string, packets int, out io.Writer) (dreamRecord, int, error) {
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
	rec, w, err := prepare(dir, from, seen, false, packets, &packet)
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
	calls, err := w.calls()
	if err != nil {
		return rec, work, err
	}
	start = time.Now()
	r, err := askAll(calls)
	timing.Model, timing.API, timing.TTFT, timing.Calls = ms(time.Since(start)), ms(r.API), ms(r.TTFT), len(calls)
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

// dreamTiming is where the time of one dream -run pass went (#471), in milliseconds: the packet, the model calls as
// memcue waits for them, the longest of them as `claude -p` reports its API time and its wait for the first token,
// and the apply. A packet with nothing to ask has only Prepare.
type dreamTiming struct {
	Prepare int64 `json:"prepare_ms"`
	Calls   int   `json:"calls,omitempty"` // model calls made at once
	Model   int64 `json:"model_ms,omitempty"`
	API     int64 `json:"api_ms,omitempty"`
	TTFT    int64 `json:"ttft_ms,omitempty"`
	Apply   int64 `json:"apply_ms,omitempty"`
}

func ms(d time.Duration) int64 { return d.Milliseconds() }

// dreamWork is the work prepare gathered for the model, kept apart so dream -run can cut it into calls.
type dreamWork struct {
	id                       string
	split, expand, translate []store.Fact
	groups                   [][]store.Fact
	fresh                    map[string]bool // the facts of the groups the dream reviews as new
}

// calls cuts the work into packets of the usual bounds, one task kind a call: fresh facts with groups, which need the
// model's judgement and their neighbours, then the facts to expand, then the facts to translate. Every call is a
// whole packet with its instructions and the other data sections empty, so the model reads it as it reads one packet.
func (w dreamWork) calls() ([]string, error) {
	var out []string
	add := func(split, expand, translate []store.Fact, groups [][]store.Fact) error {
		text, err := renderPacket(w.id, split, expand, translate, groups, w.fresh)
		out = append(out, text)
		return err
	}
	for i := 0; i < len(w.split) || i/packetFresh*packetGroups < len(w.groups); i += packetFresh {
		k := i / packetFresh * packetGroups
		if err := add(w.split[min(i, len(w.split)):min(i+packetFresh, len(w.split))], nil, nil,
			w.groups[min(k, len(w.groups)):min(k+packetGroups, len(w.groups))]); err != nil {
			return nil, err
		}
	}
	for c := range slices.Chunk(w.expand, packetExpand) {
		if err := add(nil, c, nil, nil); err != nil {
			return nil, err
		}
	}
	for c := range slices.Chunk(w.translate, packetTranslate) {
		if err := add(nil, nil, c, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// askAll sends the calls at once and joins their answers into the one answer -apply reads. A failed call fails the
// pass, as a failed packet did: the next pass prepares the same work again. The tokens and the cost are summed; the
// API time and the first token are the longest call's.
func askAll(calls []string) (extract.DreamReply, error) {
	replies := make([]extract.DreamReply, len(calls))
	errs := make([]error, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Go(func() { replies[i], errs[i] = dreamRunFn(c) })
	}
	wg.Wait()
	var r extract.DreamReply
	var joined dreamAnswer
	for i, rp := range replies {
		r.Tokens.Add(rp.Tokens)
		r.CostUSD += rp.CostUSD
		r.API, r.TTFT = max(r.API, rp.API), max(r.TTFT, rp.TTFT)
		if errs[i] != nil {
			continue
		}
		var a dreamAnswer
		if err := json.Unmarshal(rp.Answer, &a); err != nil {
			errs[i] = fmt.Errorf("answer: %w", err)
			continue
		}
		if i > 0 && a.Packet != joined.Packet {
			errs[i] = fmt.Errorf("answer is for packet %q, the others for %q", a.Packet, joined.Packet)
			continue
		}
		joined.Packet = a.Packet
		joined.Splits = append(joined.Splits, a.Splits...)
		joined.Expansions = append(joined.Expansions, a.Expansions...)
		joined.Actions = append(joined.Actions, a.Actions...)
		joined.Translations = append(joined.Translations, a.Translations...)
	}
	if err := errors.Join(errs...); err != nil {
		return r, err
	}
	var err error
	r.Answer, err = json.Marshal(joined)
	return r, err
}
