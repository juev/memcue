package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/juev/memcue/internal/store"
)

const (
	serverDreamFile   = "dream.server.json"
	serverSweepEvery  = 7 * 24 * time.Hour
	serverDreamPasses = 4
	serverDreamJobs   = 4
)

type dreamSweepRange struct {
	after, through string
}

type serverDreamState struct {
	FreshAt      time.Time `json:"fresh_at"`
	FreshPending bool      `json:"fresh_pending,omitempty"`
	SweepAt      time.Time `json:"sweep_at"`
	SweepAfter   string    `json:"sweep_after,omitempty"`
	SweepThrough string    `json:"sweep_through,omitempty"`
	SweepNext    bool      `json:"sweep_next,omitempty"`
}

type dreamQueueCounts struct {
	Fresh int `json:"fresh"`
	Scope int `json:"scope"`
	Old   int `json:"old"`
}

type serverDreamProgress struct {
	Time      time.Time         `json:"time"`
	Task      string            `json:"server_task"`
	Completed dreamQueueCounts  `json:"completed"`
	Remaining *dreamQueueCounts `json:"remaining,omitempty"`
	Stop      string            `json:"stop_reason"`
	Error     string            `json:"error,omitempty"`
}

// A burst has a finite number of passes. The scheduler re-enters it immediately on success
// while work remains, and waits a tick after a failure or a pass without progress.
func runServerDreamBatches(ctx context.Context, dir string, every time.Duration) (bool, error) {
	out, err := os.OpenFile(filepath.Join(dir, "dream.out"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer func() { _ = out.Close() }()
	inv := &invocation{ctx: ctx}
	for pass := 1; pass <= serverDreamPasses; pass++ {
		progress, err := inv.serverDreamBatch(dir, time.Now(), every, out)
		if progress.Task == "" {
			return false, err // no scheduled work
		}
		if pass == serverDreamPasses && progress.Stop == "continue" {
			progress.Stop = "budget"
		}
		if writeErr := reportServerDream(dir, out, progress, err); writeErr != nil {
			return false, errors.Join(err, writeErr)
		}
		if err != nil || progress.Stop != "continue" {
			return progress.Stop == "budget", err
		}
	}
	return false, nil
}

func reportServerDream(dir string, out io.Writer, p serverDreamProgress, err error) error {
	if err != nil {
		p.Error = err.Error()
		p.Stop = "error"
		if errors.Is(err, context.Canceled) {
			p.Stop = "canceled"
		}
	}
	_, writeErr := fmt.Fprintf(out, "dream server %s: %d fresh, %d scope, %d old completed; %s",
		p.Task, p.Completed.Fresh, p.Completed.Scope, p.Completed.Old, p.Stop)
	if p.Remaining != nil {
		_, err := fmt.Fprintf(out, "; %d fresh, %d scope, %d old remaining",
			p.Remaining.Fresh, p.Remaining.Scope, p.Remaining.Old)
		writeErr = errors.Join(writeErr, err)
	}
	if p.Error != "" {
		_, err := fmt.Fprintf(out, "; %s", p.Error)
		writeErr = errors.Join(writeErr, err)
	}
	_, endErr := fmt.Fprintln(out)
	return errors.Join(writeErr, endErr, appendJSON(filepath.Join(dir, "dream.log"), p))
}

func (inv *invocation) serverDreamBatch(dir string, now time.Time, every time.Duration, out io.Writer) (serverDreamProgress, error) {
	progress := serverDreamProgress{Time: now}
	if err := inv.ctx.Err(); err != nil {
		return progress, err
	}
	unlock, err := lockDream(dir)
	if err != nil {
		return progress, err
	}
	defer unlock()

	path := filepath.Join(dir, serverDreamFile)
	var state serverDreamState
	if err := readDreamState(path, &state); err != nil {
		return progress, err
	}
	last, initialized := lastDream(dir)
	if state.FreshAt.IsZero() {
		if !initialized {
			if err := inv.markBase(dir); err != nil {
				return progress, err
			}
			state.FreshAt, state.SweepAt = now, now
			return progress, writeJSONFile(path, state)
		}
		state.FreshAt = last
	}
	freshDue := state.FreshPending || !now.Before(state.FreshAt.Add(every))
	sweepDue := state.SweepThrough != "" || !now.Before(state.SweepAt.Add(serverSweepEvery))
	if !freshDue && !sweepDue {
		return progress, nil
	}
	active, fresh, scope, err := inv.serverDreamQueues(dir)
	if err != nil {
		return progress, err
	}

	before := state
	// Capture the end of the old scan once. New writes go through ordinary review
	// and can join a later sweep.
	if sweepDue && state.SweepThrough == "" {
		for _, f := range slices.Backward(active) {
			if !fresh[f.ID] {
				state.SweepThrough = f.ID
				break
			}
		}
		state.SweepAt = now
	}
	remaining := state.remaining(active, fresh, scope)
	state.finishEmpty(remaining, now)
	freshReady := freshDue && remaining.Fresh+remaining.Scope != 0
	sweepReady := state.SweepThrough != ""
	if freshReady {
		state.FreshPending = true
	}
	if state != before {
		if err := writeJSONFile(path, state); err != nil {
			return progress, err
		}
	}
	if !freshReady && !sweepReady {
		return progress, nil
	}

	old := sweepReady && (!freshReady || state.SweepNext)
	options := dreamOptions{server: true}
	progress.Task, progress.Remaining = "fresh", &remaining
	if old {
		options.sweep = &dreamSweepRange{after: state.SweepAfter, through: state.SweepThrough}
		progress.Task = "old"
	}
	rec, _, err := inv.dreamPass(dir, serverDreamJobs, options, out)
	if err != nil {
		return progress, err
	}
	state.SweepNext = !old
	if old {
		state.SweepAfter = rec.sweepCursor
		progress.Completed.Old = rec.Swept
	} else {
		progress.Completed.Fresh, progress.Completed.Scope = rec.Fresh, rec.ScopeChecked
	}

	// Commit the cursor only once the packet has been applied (or had no model work).
	// Fresh fact and scope progress is already in the ordinary dream checkpoints.
	if err := writeJSONFile(path, state); err != nil {
		return progress, err
	}
	active, fresh, scope, err = inv.serverDreamQueues(dir)
	if err != nil {
		progress.Remaining = nil
		return progress, err
	}
	remaining = state.remaining(active, fresh, scope)
	before = state
	state.finishEmpty(remaining, now)
	if state != before {
		if err := writeJSONFile(path, state); err != nil {
			return progress, err
		}
	}
	switch {
	case !state.FreshPending && state.SweepThrough == "":
		progress.Stop = "done"
		if remaining.Fresh+remaining.Scope != 0 {
			progress.Stop = "interval"
		}
	case old && rec.Swept == 0 || !old && rec.Fresh == 0 && !rec.scopeAdvanced:
		progress.Stop = "no_progress"
	default:
		progress.Stop = "continue"
	}
	return progress, nil
}

func (s serverDreamState) remaining(active []store.Fact, fresh map[string]bool, scope int) dreamQueueCounts {
	return dreamQueueCounts{
		Fresh: len(fresh),
		Scope: scope,
		Old:   len(oldDreamFacts(active, s.SweepAfter, s.SweepThrough, fresh)),
	}
}

func (s *serverDreamState) finishEmpty(work dreamQueueCounts, now time.Time) {
	if s.FreshPending && work.Fresh+work.Scope == 0 {
		s.FreshAt, s.FreshPending = now, false
	}
	if s.SweepThrough != "" && work.Old == 0 {
		s.SweepAfter, s.SweepThrough, s.SweepAt = "", "", now
	}
}

func oldDreamFacts(active []store.Fact, after, through string, fresh map[string]bool) []store.Fact {
	var old []store.Fact
	for _, f := range active {
		if f.ID > after && f.ID <= through && !fresh[f.ID] {
			old = append(old, f)
		}
	}
	return old
}

func (inv *invocation) serverDreamQueues(dir string) ([]store.Fact, map[string]bool, int, error) {
	from, _ := lastDream(dir)
	seen, err := readDreamSeen(dir)
	if err != nil {
		return nil, nil, 0, err
	}
	cursor, err := readScopeCursor(dir)
	if err != nil {
		return nil, nil, 0, err
	}
	s, err := inv.openBase()
	if err != nil {
		return nil, nil, 0, err
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		return nil, nil, 0, err
	}
	fresh, err := freshFacts(s, active, from, seen)
	return active, freshSet(fresh), len(dreamScopeFacts(active, cursor)), err
}
