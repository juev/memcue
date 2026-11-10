// Package server is the HTTP side of memcue server (#714, docs/specs/sync-server.md): it hands the changes of the
// shared base to replicas by long poll, merges the changes they send, and loads an empty base once. The transport is
// the caller's: memcue server serves it on the tailnet, tests on httptest.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/juev/memcue/internal/hlc"
	"github.com/juev/memcue/internal/store"
)

// Limits of a request.
const (
	// MaxAhead is how far a change's stamp may be ahead of the server's clock before the change is refused (R8).
	MaxAhead = 5 * time.Minute
	// MaxWait caps the wait of a long poll.
	MaxWait = 2 * time.Minute
	// DefaultLimit and MaxLimit bound the rows of one answer.
	DefaultLimit = 500
	MaxLimit     = 5000
	// MaxChanges bounds the changes of one POST, MaxBody its size.
	MaxChanges = 5000
	MaxBody    = 32 << 20
)

// RefusedAhead: the change's stamp is ahead of the server's clock by more than MaxAhead (R8). Unlike the refusals of
// the store it is not remembered: the same operation is accepted once the server's clock catches up.
const RefusedAhead = "ahead"

// Row is a fact as the wire carries it.
type Row struct {
	ID      string            `json:"id"`
	Version int               `json:"version"`
	Content string            `json:"content"`
	Origin  string            `json:"origin,omitempty"`
	Created time.Time         `json:"created"`
	Status  string            `json:"status"`
	Expires string            `json:"expires,omitempty"`
	Kind    string            `json:"kind,omitempty"`
	Scope   string            `json:"scope,omitempty"`
	Source  string            `json:"source,omitempty"`
	Stamps  map[string]string `json:"stamps,omitempty"`
	Seq     int64             `json:"seq,omitempty"`
	Updated time.Time         `json:"updated,omitzero"`
	LoserOf string            `json:"loser_of,omitempty"`
}

// WireRow is r as the wire carries it.
func WireRow(r store.Row) Row {
	return Row{ID: r.ID, Version: r.Version, Content: r.Content, Origin: r.Origin, Created: r.Created, Status: r.Status,
		Expires: r.Expires, Kind: r.Kind, Scope: r.Scope, Source: r.Source, Stamps: r.Stamps, Seq: r.Seq,
		Updated: r.Updated, LoserOf: r.LoserOf}
}

// StoreRow is the store's form of r.
func (r Row) StoreRow() store.Row {
	stamps := store.Stamps{}
	for k, v := range r.Stamps {
		stamps[k] = v
	}
	return store.Row{Fact: r.fact(), Stamps: stamps, Seq: r.Seq, Updated: r.Updated, LoserOf: r.LoserOf}
}

func (r Row) fact() store.Fact {
	return store.Fact{ID: r.ID, Version: r.Version, Content: r.Content, Origin: r.Origin, Created: r.Created,
		Status: r.Status, Expires: r.Expires, Meta: store.Meta{Kind: r.Kind, Scope: r.Scope, Source: r.Source}}
}

// Changes is the answer of GET /v1/changes.
type Changes struct {
	BaseID string `json:"base_id"`
	Head   int64  `json:"head"`
	Rows   []Row  `json:"rows"`
}

// Push is the body of POST /v1/changes.
type Push struct {
	Changes []store.Change `json:"changes"`
}

// Pushed is its answer: one result per change, in order.
type Pushed struct {
	Results []store.Applied `json:"results"`
}

// Import is the body of POST /v1/import: the latest version of every fact of a base (R12).
type Import struct {
	Facts []Row `json:"facts"`
}

// Imported is its answer.
type Imported struct {
	Facts int `json:"facts"`
}

// Server serves one server base.
type Server struct {
	st  *store.Store
	log *log.Logger
	who func(*http.Request) string
	// now is the server's clock for R8, poll how often a waiting long poll looks for commits of other processes.
	now  func() time.Time
	poll time.Duration

	mu      sync.Mutex
	changed chan struct{} // closed and replaced by every commit of this process
}

// New serves st, logging each write with the name of the client who gives (R13).
func New(st *store.Store, logger *log.Logger, who func(*http.Request) string) *Server {
	return &Server{st: st, log: logger, who: who, now: time.Now, poll: 100 * time.Millisecond,
		changed: make(chan struct{})}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/changes" && r.Method == http.MethodGet:
		s.changes(w, r)
	case r.URL.Path == "/v1/changes" && r.Method == http.MethodPost:
		s.push(w, r)
	case r.URL.Path == "/v1/import" && r.Method == http.MethodPost:
		s.importFacts(w, r)
	case r.URL.Path == "/v1/changes" || r.URL.Path == "/v1/import":
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		fail(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) wake() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *Server) waiter() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// changes answers GET /v1/changes?since=N&wait=D&limit=L: the rows after since, at once when there are any, and
// otherwise as soon as one is written or after wait (R2).
func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, err := intParam(q.Get("since"), 0)
	if err != nil || since < 0 {
		fail(w, http.StatusBadRequest, "since must be a non-negative integer")
		return
	}
	limit, err := intParam(q.Get("limit"), DefaultLimit)
	if err != nil || limit < 1 || limit > MaxLimit {
		fail(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", MaxLimit))
		return
	}
	var wait time.Duration
	if v := q.Get("wait"); v != "" {
		if wait, err = time.ParseDuration(v); err != nil || wait < 0 || wait > MaxWait {
			fail(w, http.StatusBadRequest, fmt.Sprintf("wait must be a duration up to %s", MaxWait))
			return
		}
	}
	base, err := s.st.BaseID()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	deadline := time.Now().Add(wait)
	for {
		woken := s.waiter()
		seen, err := s.st.DataVersion()
		if err != nil {
			s.internal(w, r, err)
			return
		}
		rows, head, err := s.st.Changes(since, int(limit))
		if err != nil {
			s.internal(w, r, err)
			return
		}
		// A since above the head is a replica of another base or a rebuilt server (R9): waiting cannot help it.
		if len(rows) > 0 || since > head || !time.Now().Before(deadline) {
			out := Changes{BaseID: base, Head: head, Rows: make([]Row, 0, len(rows))}
			for _, row := range rows {
				out.Rows = append(out.Rows, WireRow(row))
			}
			reply(w, out)
			return
		}
		if !s.sleep(r, woken, seen, deadline) {
			return
		}
	}
}

// sleep waits until this process commits, another process commits (data_version moves) or the deadline passes. It
// returns false when the client has gone.
func (s *Server) sleep(r *http.Request, woken <-chan struct{}, seen int64, deadline time.Time) bool {
	tick := time.NewTicker(s.poll)
	defer tick.Stop()
	timeout := time.NewTimer(time.Until(deadline))
	defer timeout.Stop()
	for {
		select {
		case <-r.Context().Done():
			return false
		case <-woken:
			return true
		case <-timeout.C:
			return true
		case <-tick.C:
			if v, err := s.st.DataVersion(); err != nil || v != seen {
				return true
			}
		}
	}
}

// push answers POST /v1/changes: each change merged once by its op_id (R4), refused when it is malformed, names a
// taken id or is stamped too far ahead (R8).
func (s *Server) push(w http.ResponseWriter, r *http.Request) {
	var p Push
	if !decode(w, r, &p) {
		return
	}
	if len(p.Changes) > MaxChanges {
		fail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("at most %d changes per request", MaxChanges))
		return
	}
	results := make([]store.Applied, len(p.Changes))
	var accepted []store.Change
	var at []int
	now := s.now()
	for i, ch := range p.Changes {
		if st, err := hlc.Parse(ch.Stamp); err == nil && hlc.Ahead(st, now) > MaxAhead {
			results[i] = store.Applied{OpID: ch.OpID, ID: ch.ID, Refused: RefusedAhead}
			continue
		}
		accepted, at = append(accepted, ch), append(at, i)
	}
	if len(accepted) > 0 {
		applied, err := s.st.ApplyChanges(accepted)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		for j, a := range applied {
			results[at[j]] = a
		}
		s.wake()
	}
	refused := 0
	for _, a := range results {
		if a.Refused != "" {
			refused++
		}
	}
	s.log.Printf("changes from %s: %d accepted, %d refused", s.who(r), len(results)-refused, refused)
	reply(w, Pushed{Results: results})
}

// importFacts answers POST /v1/import: the facts go into an empty base, or nothing is stored (R12).
func (s *Server) importFacts(w http.ResponseWriter, r *http.Request) {
	var in Import
	if !decode(w, r, &in) {
		return
	}
	facts := make([]store.Fact, 0, len(in.Facts))
	for _, f := range in.Facts {
		facts = append(facts, f.fact())
	}
	err := s.st.Import(facts)
	switch {
	case errors.Is(err, store.ErrNotEmpty):
		fail(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, store.ErrChange):
		fail(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	s.wake()
	s.log.Printf("import from %s: %d facts", s.who(r), len(facts))
	reply(w, Imported{Facts: len(facts)})
}

func intParam(v string, def int64) (int64, error) {
	if v == "" {
		return def, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBody)).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return false
		}
		fail(w, http.StatusBadRequest, "malformed JSON body")
		return false
	}
	return true
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Printf("%s %s from %s: %v", r.Method, r.URL.Path, s.who(r), err)
	fail(w, http.StatusInternalServerError, "server base error")
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
