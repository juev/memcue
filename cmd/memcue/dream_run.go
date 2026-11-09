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

// dreamRunTimeout bounds one model call of a dream. Packets have bounded fresh facts, scopes and groups.
const dreamRunTimeout = 10 * time.Minute

// dreamAllPasses bounds dream -run -all: a base of 2,800 facts behind on everything takes about 70 passes. A base
// larger than that continues where the bound stopped it on the next run.
var dreamAllPasses = 200

// dreamRunFn is the model call of dream -run, replaced in tests.
var dreamRunFn = func(parent context.Context, packet string) (extract.DreamReply, error) {
	ctx, cancel := context.WithTimeout(parent, dreamRunTimeout)
	defer cancel()
	return extract.DreamAnswer(ctx, packet)
}

// dreamAllStalls bounds repeated unanswered split or scope work in dream -run -all.
const dreamAllStalls = 3

// runDreamOnce is memcue dream -run. A plain run (#401) is the dream started by hand or by a job, without a session
// model to hand the packet to: it asks what the session start of Claude
// Code asks before it says a dream is due, whether MEMCUE_DREAM_EVERY has passed and some fact is fresh, and with nothing due makes no call. It needs MEMCUE_API_URL, so a long run never starts by surprise.
// The background run a session start starts (#569) is that plain run, on claude -p without MEMCUE_API_URL: the
// session start checked that one of them is there. It leaves quietly when another dream holds the lock.
//
// With all (#405) it finishes the base: no interval, packets one after another while a packet has facts to split,
// or check the scope of. The merge sweep walks the base in a circle and never
// runs out of groups, so it does not keep the loop going. It runs on the endpoint when MEMCUE_API_URL is set and on
// claude -p otherwise, and stops on the first failed pass, after dreamAllStalls passes that change nothing, or after
// dreamAllPasses.
func (inv *invocation) runDreamOnce(out io.Writer, all, background bool, packets int) error {
	out = inv.writer(out)
	if !all && !background && extract.API.URL == "" {
		return fmt.Errorf("dream -run: %w", extract.ErrNoEndpoint)
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	if !all {
		fresh, _, err := inv.dreamDue(dir, dreamEveryFromEnv(dreamEvery))
		if err != nil {
			return err
		}
		if fresh == 0 {
			_, err := fmt.Fprintln(out, "dream: nothing due")
			return err
		}
	}
	unlock, err := lockDream(dir)
	if err != nil {
		if background && errors.Is(err, errDreamRunning) {
			return nil
		}
		return err
	}
	defer unlock()
	if !all {
		_, _, err := inv.dreamPass(dir, packets, out)
		return err
	}
	stalls := 0
	for pass := 1; pass <= dreamAllPasses; pass++ {
		if _, err := fmt.Fprintf(out, "dream -all: pass %d\n", pass); err != nil {
			return err
		}
		rec, work, err := inv.dreamPass(dir, packets, out)
		if err != nil {
			return fmt.Errorf("dream -all: pass %d: %w", pass, err)
		}
		if rec.Packet == "" { // nothing for the dream
			_, err := fmt.Fprintf(out, "dream -all: done after %d passes\n", pass)
			return err
		}
		// A pass that checked the scope of global facts moved its cursor, even when it moved no fact (#607).
		if len(rec.Splits)+len(rec.Actions)+rec.ScopeChecked == 0 {
			stalls++
		} else {
			stalls = 0
		}
		switch {
		case work == 0:
			_, err := fmt.Fprintf(out, "dream -all: done after %d passes\n", pass)
			return err
		case stalls >= dreamAllStalls:
			_, err := fmt.Fprintf(out, "dream -all: stopped after %d passes that changed nothing; maintenance work remains\n", stalls)
			return err
		}
	}
	return fmt.Errorf("dream -all: stopped after %d passes with work left; run it again", dreamAllPasses)
}

// dreamPass is one dream under the caller's lock: the default packet, since the last dream with the facts it saw, or
// every active fact on a machine with no dream yet; then the model's answer, applied under the -apply checks, and a
// line in dream.log either way. It returns that line and how many facts the packet gave to split or
// check the scope of; a record without a packet means there was nothing for the dream.
//
// The work of up to packets packets is cut into calls the model answers at once (#471): each packet's fresh facts and
// groups and each packet's global facts to check the scope of. Most of a call's time is the model
// writing its answer, so three calls of a third of it each take about a third of the time. Their answers are joined
// and applied as one.
func (inv *invocation) dreamPass(dir string, packets int, out io.Writer) (dreamRecord, int, error) {
	out = inv.writer(out)
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
	rec, w, err := inv.prepare(dir, from, seen, false, packets, &packet)
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
	work := len(p.Split) + len(p.Scope)
	calls, err := w.calls()
	if err != nil {
		return rec, work, err
	}
	start = time.Now()
	r, byCall, err := askAll(inv.ctx, calls)
	timing.Model, timing.API, timing.TTFT, timing.Calls = ms(time.Since(start)), ms(r.API), ms(r.TTFT), len(calls)
	if err != nil {
		rec.Tokens, rec.CostUSD, rec.Timing, rec.Error = &r.Tokens, r.CostUSD, &timing, "model: "+err.Error()
		rec.Calls = byCall
		return rec, work, errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
	}
	// The answer stays next to its packet: what the model said is the record a refused action is checked by.
	path := filepath.Join(dir, "dream.answer.json")
	if err := os.WriteFile(path, r.Answer, 0o600); err != nil {
		return rec, work, err
	}
	start = time.Now()
	rec, err = inv.applyAnswer(dir, path, out)
	timing.Apply = ms(time.Since(start))
	rec.Tokens, rec.CostUSD, rec.Timing, rec.Calls = &r.Tokens, r.CostUSD, &timing, byCall
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
	id       string
	split    []store.Fact
	groups   [][]store.Fact
	fresh    map[string]bool // the facts of the groups the dream reviews as new
	scope    []store.Fact    // the global facts to check the scope of (#607)
	projects []string        // the projects they may move to
}

// calls cuts the work into packets of the usual bounds, one task kind a call: fresh facts with groups, which need the
// model's judgement and their neighbours, then the global facts to
// check the scope of (#607). Every call is a whole packet with
// its instructions and the other data sections empty, so the model reads it as it reads one packet.
func (w dreamWork) calls() ([]dreamCall, error) {
	var out []dreamCall
	add := func(task string, split, scope []store.Fact, groups [][]store.Fact) error {
		projects := w.projects
		if len(scope) == 0 {
			projects = nil
		}
		text, err := renderPacket(w.id, split, scope, projects, groups, w.fresh)
		c := dreamCall{text: text, rec: dreamCallRecord{Task: task, Split: len(split), Groups: len(groups),
			Facts: len(scope)}}
		for _, g := range groups {
			if !slices.ContainsFunc(g, func(f store.Fact) bool { return w.fresh[f.ID] }) {
				c.rec.Swept++
			}
		}
		out = append(out, c)
		return err
	}
	for i := 0; i < len(w.split) || i/packetFresh*packetGroups < len(w.groups); i += packetFresh {
		k := i / packetFresh * packetGroups
		if err := add(taskReview, w.split[min(i, len(w.split)):min(i+packetFresh, len(w.split))], nil,
			w.groups[min(k, len(w.groups)):min(k+packetGroups, len(w.groups))]); err != nil {
			return nil, err
		}
	}
	for c := range slices.Chunk(w.scope, packetScope) {
		if err := add(taskScope, nil, c, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// The tasks of a dream call in dream.log (#643). A review call holds the fresh facts to split together with the
// groups to merge, the fresh ones and the merge sweep's, so its cost is not told apart further.
const (
	taskReview = "review"
	taskScope  = "scope"
)

// dreamCall is one model call of a pass: the packet text it sends and its line in the pass's record.
type dreamCall struct {
	text string
	rec  dreamCallRecord
}

// dreamCallRecord is one model call of a dream -run pass in dream.log (#643): its task, the work it was given, how
// many changes the model proposed, and what the call took. The record of the pass sums the tokens and the cost of its
// calls; these lines say which task they went to. CostUSD is what `claude -p` reports; an endpoint reports tokens only,
// so a call there has no cost, which does not mean it was free.
type dreamCallRecord struct {
	Task     string         `json:"task"`
	Split    int            `json:"split,omitempty"`  // fresh facts given to split
	Groups   int            `json:"groups,omitempty"` // groups given to merge
	Swept    int            `json:"swept,omitempty"`  // of them, the merge sweep's: groups with no fresh fact
	Facts    int            `json:"facts,omitempty"`  // facts given to check the scope of
	Proposed int            `json:"proposed"`         // splits, actions or scopes the model returned
	Tokens   extract.Tokens `json:"tokens"`
	CostUSD  float64        `json:"cost_usd,omitempty"`
	Model    int64          `json:"model_ms"` // the call as memcue waited for it
	API      int64          `json:"api_ms,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// askAll sends the calls at once and joins their answers into the one answer -apply reads. A failed call fails the
// pass, as a failed packet did: the next pass prepares the same work again. The tokens and the cost are summed; the
// API time and the first token are the longest call's. The records of the calls come back whether they failed or not.
func askAll(ctx context.Context, calls []dreamCall) (extract.DreamReply, []dreamCallRecord, error) {
	replies := make([]extract.DreamReply, len(calls))
	errs := make([]error, len(calls))
	took := make([]time.Duration, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Go(func() {
			start := time.Now()
			replies[i], errs[i] = dreamRunFn(ctx, c.text)
			took[i] = time.Since(start)
		})
	}
	wg.Wait()
	var r extract.DreamReply
	var joined dreamAnswer
	recs := make([]dreamCallRecord, len(calls))
	for i, rp := range replies {
		recs[i] = calls[i].rec
		recs[i].Tokens, recs[i].CostUSD, recs[i].Model, recs[i].API = rp.Tokens, rp.CostUSD, ms(took[i]), ms(rp.API)
		r.Tokens.Add(rp.Tokens)
		r.CostUSD += rp.CostUSD
		r.API, r.TTFT = max(r.API, rp.API), max(r.TTFT, rp.TTFT)
		if errs[i] != nil {
			recs[i].Error = errs[i].Error()
			continue
		}
		a, err := decodeDreamAnswer(rp.Answer)
		if err != nil {
			errs[i] = fmt.Errorf("answer: %w", err)
			recs[i].Error = errs[i].Error()
			continue
		}
		recs[i].Proposed = len(a.Splits) + len(a.Expansions) + len(a.Actions) + len(a.Scopes)
		// The first answer that came back sets the packet; a failed call before it is no packet to compare with.
		if joined.Packet != "" && a.Packet != joined.Packet {
			errs[i] = fmt.Errorf("answer is for packet %q, the others for %q", a.Packet, joined.Packet)
			recs[i].Error = errs[i].Error()
			continue
		}
		joined.Packet = a.Packet
		joined.Splits = append(joined.Splits, a.Splits...)
		joined.Expansions = append(joined.Expansions, a.Expansions...)
		joined.Actions = append(joined.Actions, a.Actions...)
		joined.Scopes = append(joined.Scopes, a.Scopes...)
	}
	if err := errors.Join(errs...); err != nil {
		return r, recs, err
	}
	var err error
	r.Answer, err = json.Marshal(joined)
	return r, recs, err
}
