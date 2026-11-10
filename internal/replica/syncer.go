package replica

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/juev/memcue/internal/server"
	"github.com/juev/memcue/internal/store"
)

// Rejected is an outbox change the server did not accept.
type Rejected struct {
	OpID   string `json:"op_id"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Status is what the helper reports of the sync (connect status, the session hints of R15).
type Status struct {
	// LastSync is when the replica last heard from the server; zero before the first time.
	LastSync time.Time  `json:"last_sync,omitzero"`
	Outbox   int        `json:"outbox"`
	Rejected []Rejected `json:"rejected,omitempty"`
	// Error is the last failure to reach the server, "" once it answers again.
	Error string `json:"error,omitempty"`
}

// Syncer keeps one replica in step with one server.
type Syncer struct {
	r *store.Store
	c Client

	wait  time.Duration // of a long poll
	limit int
	poll  time.Duration // how often the outbox is looked at for writes of other processes
	retry time.Duration // when a change refused for a while (ahead, not_found) is sent again
	now   func() time.Time

	mu       sync.Mutex
	rejected map[string]rejection
	err      string
	again    bool // a push failed or left work: try again at the next poll
}

type rejection struct {
	id, reason string
	retryAt    time.Time // zero: the server will not take this change
}

// New returns a Syncer of the replica r and the server c.
func New(r *store.Store, c Client) *Syncer {
	return &Syncer{r: r, c: c, wait: 55 * time.Second, limit: server.DefaultLimit, poll: 200 * time.Millisecond,
		retry: time.Minute, now: time.Now, rejected: map[string]rejection{}}
}

// Run pulls and pushes until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.pullLoop(ctx) }()
	go func() { defer wg.Done(); s.pushLoop(ctx) }()
	wg.Wait()
}

// Status reports the sync.
func (s *Syncer) Status() (Status, error) {
	st, err := s.r.State()
	if err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Status{LastSync: st.LastSync, Outbox: st.Outbox, Error: s.err}
	for op, r := range s.rejected {
		out.Rejected = append(out.Rejected, Rejected{OpID: op, ID: r.id, Reason: r.reason})
	}
	sort.Slice(out.Rejected, func(i, j int) bool { return out.Rejected[i].OpID < out.Rejected[j].OpID })
	return out, nil
}

func (s *Syncer) failed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = ""
	if err != nil {
		s.err = err.Error()
	}
}

func (s *Syncer) pullLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := s.pullOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.failed(err)
			if !sleep(ctx, backoff) {
				return
			}
			backoff = min(2*backoff, 30*time.Second)
			continue
		}
		s.failed(nil)
		backoff = time.Second
	}
}

// pullOnce applies one answer of the server. A server of another base, or one behind the replica, makes the replica
// start over from that base with its outbox kept (R9).
func (s *Syncer) pullOnce(ctx context.Context) error {
	st, err := s.r.State()
	if err != nil {
		return err
	}
	c, err := s.c.Pull(ctx, st.LastSeq, s.wait, s.limit)
	if err != nil {
		return err
	}
	if st.BaseID != "" && (c.BaseID != st.BaseID || st.LastSeq > c.Head) {
		return s.r.ResetReplica(c.BaseID)
	}
	rows := make([]store.Row, 0, len(c.Rows))
	for _, row := range c.Rows {
		rows = append(rows, row.StoreRow())
	}
	err = s.r.ApplyRows(c.BaseID, rows)
	if errors.Is(err, store.ErrBaseChanged) {
		return s.r.ResetReplica(c.BaseID)
	}
	return err
}

func (s *Syncer) pushLoop(ctx context.Context) {
	seen := int64(-1)
	tick := time.NewTicker(s.poll)
	defer tick.Stop()
	for {
		v, err := s.r.DataVersion()
		if err == nil && (v != seen || s.due()) {
			seen = v
			if err := s.pushOnce(ctx); err != nil && ctx.Err() == nil {
				s.failed(err)
				s.setAgain()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Syncer) setAgain() {
	s.mu.Lock()
	s.again = true
	s.mu.Unlock()
}

// due: a push is owed without a new write, after a failure or for a refusal whose retry time has come.
func (s *Syncer) due() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.again {
		return true
	}
	now := s.now()
	for _, r := range s.rejected {
		if !r.retryAt.IsZero() && !now.Before(r.retryAt) {
			return true
		}
	}
	return false
}

// pushOnce sends the outbox. Changes of a fact whose create the server has not accepted wait for it: if the server
// refused the create because another fact holds the id, a change sent beside it would land on that other fact.
func (s *Syncer) pushOnce(ctx context.Context) error {
	out, err := s.r.Outbox()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.again = false
	now := s.now()
	var send []store.Change
	waiting := map[string]bool{} // facts whose create is in the outbox, sent now or held
	for _, ch := range out {
		if waiting[ch.ID] {
			continue
		}
		if ch.Create {
			waiting[ch.ID] = true
		}
		if r, ok := s.rejected[ch.OpID]; ok && (r.retryAt.IsZero() || now.Before(r.retryAt)) {
			continue
		}
		send = append(send, ch)
	}
	s.mu.Unlock()
	for len(send) > 0 {
		batch := send[:min(len(send), server.MaxChanges)]
		send = send[len(batch):]
		res, err := s.c.Push(ctx, batch)
		if err != nil {
			return err
		}
		s.failed(nil)
		for i, a := range res {
			if err := s.answered(batch[i], a); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Syncer) answered(ch store.Change, a store.Applied) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch a.Refused {
	case "":
		delete(s.rejected, ch.OpID)
		if ch.Create {
			s.again = true // the changes held behind the create can go now
		}
		return s.r.Acknowledge(ch.OpID, a.Seq)
	case store.RefusedIDTaken:
		delete(s.rejected, ch.OpID)
		s.again = true
		_, err := s.r.ReissueID(ch.ID)
		return err
	case server.RefusedAhead, store.RefusedNotFound:
		s.rejected[ch.OpID] = rejection{id: ch.ID, reason: a.Refused, retryAt: s.now().Add(s.retry)}
	default:
		s.rejected[ch.OpID] = rejection{id: ch.ID, reason: a.Refused}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Worker runs the sync of the replica at a path inside the helper, which hands it a dialer to the server.
type Worker struct {
	path string

	mu  sync.Mutex
	s   *Syncer
	err error
}

// NewWorker returns the Worker of the replica at path.
func NewWorker(path string) *Worker { return &Worker{path: path} }

// Run opens the replica and syncs it through dial until ctx ends.
func (w *Worker) Run(ctx context.Context, dial func(context.Context) (net.Conn, error)) {
	r, err := store.OpenReplica(w.path)
	if err != nil {
		w.mu.Lock()
		w.err = err
		w.mu.Unlock()
		return
	}
	defer func() { _ = r.Close() }()
	s := New(r, NewHTTP(dial))
	w.mu.Lock()
	w.s = s
	w.mu.Unlock()
	s.Run(ctx)
	w.mu.Lock()
	w.s = nil
	w.mu.Unlock()
}

// Status reports the sync, or why it is not running.
func (w *Worker) Status() any {
	w.mu.Lock()
	s, err := w.s, w.err
	w.mu.Unlock()
	if s == nil {
		if err == nil {
			err = ErrNotRunning
		}
		return Status{Error: err.Error()}
	}
	st, err := s.Status()
	if err != nil {
		return Status{Error: err.Error()}
	}
	return st
}
