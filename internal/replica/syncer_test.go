package replica

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/juev/memcue/internal/hlc"
	"github.com/juev/memcue/internal/server"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/text"
)

type serverFixture struct {
	st *store.Store
	ts *httptest.Server
}

func newServer(t *testing.T) *serverFixture {
	t.Helper()
	st, err := store.OpenServer(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.New(st, log.New(io.Discard, "", 0), func(*http.Request) string { return "test" }))
	t.Cleanup(func() { ts.Close(); _ = st.Close() })
	return &serverFixture{st: st, ts: ts}
}

// link is the tailnet as a test controls it: where the server is and whether it can be reached.
type link struct {
	mu   sync.Mutex
	addr string
	down bool
}

func (l *link) dial(ctx context.Context) (net.Conn, error) {
	l.mu.Lock()
	addr, down := l.addr, l.down
	l.mu.Unlock()
	if down {
		return nil, errors.New("tailnet down")
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

func (l *link) set(addr string, down bool) {
	l.mu.Lock()
	l.addr, l.down = addr, down
	l.mu.Unlock()
}

func linkTo(srv *serverFixture) *link { return &link{addr: srv.ts.Listener.Addr().String()} }

// startSyncer runs the sync of a new replica the way a helper does and returns the replica's path.
func startSyncer(t *testing.T, c Client) (*Syncer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "replica.db")
	r, err := store.OpenReplica(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(r, c)
	s.wait, s.poll, s.retry = 2*time.Second, 20*time.Millisecond, 100*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = r.Close() })
	return s, path
}

// agent opens the replica as another process of the machine does: a CLI that reads and writes it.
func agent(t *testing.T, path string) *store.Store {
	t.Helper()
	r, err := store.OpenReplica(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func eventually(t *testing.T, what string, cond func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < 10*time.Second {
		if cond() {
			return time.Since(start)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
	return 0
}

func found(s *store.Store, q, id string) bool {
	hits, err := s.Search(text.Terms(q), 20)
	if err != nil {
		return false
	}
	for _, h := range hits {
		if h.ID == id {
			return true
		}
	}
	return false
}

func outboxLen(t *testing.T, s *store.Store) int {
	t.Helper()
	out, err := s.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	return len(out)
}

func TestWriteOnOneReplicaReachesAnother(t *testing.T) {
	srv := newServer(t)
	_, pathA := startSyncer(t, NewHTTP(linkTo(srv).dial))
	_, pathB := startSyncer(t, NewHTTP(linkTo(srv).dial))
	a, b := agent(t, pathA), agent(t, pathB)
	time.Sleep(100 * time.Millisecond) // both long polls are waiting
	f, err := a.AddWith("the helper long-polls the sync server", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	took := eventually(t, "the write on B", func() bool { return found(b, "long-polls sync server", f.ID) })
	t.Logf("a write on replica A was found on replica B after %s", took)
	if took > 2*time.Second {
		t.Fatalf("a write took %s to reach another replica", took)
	}
	eventually(t, "the outbox of A to empty", func() bool { return outboxLen(t, a) == 0 })
}

func TestOfflineWriteIsFoundAtOnceAndSentLater(t *testing.T) {
	srv := newServer(t)
	l := linkTo(srv)
	l.set(l.addr, true)
	s, path := startSyncer(t, NewHTTP(l.dial))
	a := agent(t, path)
	f, err := a.AddWith("written while the tailnet is down", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if !found(a, "tailnet down", f.ID) {
		t.Fatal("an offline write is not found on its replica")
	}
	eventually(t, "the sync to report the failure", func() bool {
		st, err := s.Status()
		return err == nil && st.Error != "" && st.Outbox == 1
	})
	if _, err := srv.st.Get(f.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the server has an offline write: %v", err)
	}
	l.set(l.addr, false)
	eventually(t, "the write on the server", func() bool { _, err := srv.st.Get(f.ID); return err == nil })
	eventually(t, "the outbox to empty and the sync to recover", func() bool {
		st, err := s.Status()
		return err == nil && st.Outbox == 0 && st.Error == "" && !st.LastSync.IsZero()
	})
}

func TestReplicaOfAnotherServerBaseIsRebuiltWithItsOutbox(t *testing.T) {
	first, second := newServer(t), newServer(t)
	l := linkTo(first)
	_, path := startSyncer(t, NewHTTP(l.dial))
	a := agent(t, path)
	old, err := a.AddWith("a fact of the first server", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first server's row", func() bool { _, err := first.st.Get(old.ID); return err == nil })
	theirs, err := second.st.AddWith("a fact of the second server", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	l.set(l.addr, true)
	pending, err := a.AddWith("written between the servers", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	first.ts.Close() // the old server's connections go with it
	l.set(second.ts.Listener.Addr().String(), false)
	eventually(t, "the second server's fact", func() bool { _, err := a.Get(theirs.ID); return err == nil })
	eventually(t, "the pending write on the second server", func() bool {
		_, err := second.st.Get(pending.ID)
		return err == nil
	})
	if _, err := a.Get(old.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a row of the first server survived the rebuild: %v", err)
	}
	base, _ := second.st.BaseID()
	st, err := a.State()
	if err != nil || st.BaseID != base {
		t.Fatalf("replica state %+v (%v), want base %s", st, err, base)
	}
}

// refuseFirst refuses every change of its first push as ahead, as a server whose clock is behind does (R8).
type refuseFirst struct {
	Client
	mu      sync.Mutex
	refused bool
}

func (c *refuseFirst) Push(ctx context.Context, changes []store.Change) ([]store.Applied, error) {
	c.mu.Lock()
	first := !c.refused
	c.refused = true
	c.mu.Unlock()
	if !first {
		return c.Client.Push(ctx, changes)
	}
	out := make([]store.Applied, len(changes))
	for i, ch := range changes {
		out[i] = store.Applied{OpID: ch.OpID, ID: ch.ID, Refused: server.RefusedAhead}
	}
	return out, nil
}

func TestChangeAheadOfTheServerIsReportedAndSentAgain(t *testing.T) {
	srv := newServer(t)
	c := &refuseFirst{Client: NewHTTP(linkTo(srv).dial)}
	s, path := startSyncer(t, c)
	s.retry = 500 * time.Millisecond
	a := agent(t, path)
	f, err := a.AddWith("stamped by a clock ahead", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the refusal in the status", func() bool {
		st, err := s.Status()
		return err == nil && len(st.Rejected) == 1 && st.Rejected[0].Reason == server.RefusedAhead &&
			st.Rejected[0].ID == f.ID && st.Outbox == 1
	})
	eventually(t, "the retried change on the server", func() bool { _, err := srv.st.Get(f.ID); return err == nil })
	eventually(t, "the refusal to clear", func() bool {
		st, err := s.Status()
		return err == nil && len(st.Rejected) == 0 && st.Outbox == 0
	})
}

func TestCreateOfATakenIDGetsANewID(t *testing.T) {
	srv := newServer(t)
	l := linkTo(srv)
	l.set(l.addr, true)
	_, path := startSyncer(t, NewHTTP(l.dial))
	a := agent(t, path)
	f, err := a.AddWith("ours", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Correct(f.ID, "ours, corrected offline", 1); err != nil {
		t.Fatal(err)
	}
	created := f.Created.Add(-time.Hour)
	other := store.Change{OpID: "other", ID: f.ID, Create: true, Created: created, Source: "agent",
		Stamp: hlc.Stamp{Millis: created.UnixMilli(), Device: "00000000000000bb"}.String(),
		Values: map[string]string{store.RegContent: "the other fact", store.RegKind: "", store.RegScope: "",
			store.RegExpires: "", store.RegStatus: store.Active, store.RegOrigin: ""}}
	if res, err := srv.st.ApplyChanges([]store.Change{other}); err != nil || res[0].Refused != "" {
		t.Fatalf("other create: %+v %v", res, err)
	}
	l.set(l.addr, false)
	eventually(t, "the outbox to empty", func() bool { return outboxLen(t, a) == 0 })
	if got, err := srv.st.Get(f.ID); err != nil || got.Content != "the other fact" {
		t.Fatalf("the fact that holds the id changed: %+v %v", got, err)
	}
	var ours string
	eventually(t, "our fact on the server under a new id", func() bool {
		hits, err := srv.st.Search(text.Terms("ours corrected offline"), 5)
		if err != nil || len(hits) == 0 {
			return false
		}
		ours = hits[0].ID
		return ours != f.ID
	})
	if got, _ := srv.st.Get(ours); got.Content != "ours, corrected offline" {
		t.Fatalf("our fact on the server: %+v", got)
	}
	eventually(t, "the replica to hold both facts", func() bool {
		held, err1 := a.Get(f.ID)
		mine, err2 := a.Get(ours)
		return err1 == nil && err2 == nil && held.Content == "the other fact" && mine.Content == "ours, corrected offline"
	})
}

// longPollSyncer runs the sync of a new replica whose long poll waits a minute, longer than the tests below allow.
func longPollSyncer(t *testing.T, l *link) *Syncer {
	t.Helper()
	r, err := store.OpenReplica(filepath.Join(t.TempDir(), "replica.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(r, NewHTTP(l.dial))
	s.wait = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = r.Close() })
	return s
}

func TestNewReplicaOfAnEmptyServerSyncsWithoutWaiting(t *testing.T) {
	s := longPollSyncer(t, linkTo(newServer(t)))
	took := eventually(t, "the first sync", func() bool {
		st, err := s.Status()
		return err == nil && !st.LastSync.IsZero()
	})
	if took > 5*time.Second {
		t.Fatalf("first sync of an empty server took %v", took)
	}
}

func TestStatusClearsTheFailureWhenTheServerIsBack(t *testing.T) {
	srv := newServer(t)
	l := linkTo(srv)
	s := longPollSyncer(t, l)
	eventually(t, "the first sync", func() bool { st, err := s.Status(); return err == nil && !st.LastSync.IsZero() })
	l.set(srv.ts.Listener.Addr().String(), true)
	srv.ts.CloseClientConnections() // the long poll in progress breaks, and the next dial fails
	eventually(t, "the failure in the status", func() bool { st, err := s.Status(); return err == nil && st.Error != "" })
	l.set(srv.ts.Listener.Addr().String(), false)
	took := eventually(t, "the failure to clear", func() bool { st, err := s.Status(); return err == nil && st.Error == "" })
	if took > 5*time.Second {
		t.Fatalf("the status kept the failure %v after the server was back", took)
	}
}

func TestWorkerReportsWhyItDoesNotRun(t *testing.T) {
	w := NewWorker(filepath.Join(t.TempDir(), "replica.db"))
	if st, ok := w.Status().(Status); !ok || st.Error != ErrNotRunning.Error() {
		t.Fatalf("status before Run: %+v", w.Status())
	}
}

func TestPathDependsOnTheServerOnly(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	t.Setenv("MEMCUE_FACTS", "/elsewhere/facts.db")
	t.Setenv("MEMCUE_STATE_DIR", "/elsewhere/state")
	got, err := Path("memcue-server")
	if err != nil || got != "/state/memcue/server-memcue-server/replica.db" {
		t.Fatalf("Path: %q %v", got, err)
	}
	for _, bad := range []string{"", "Memcue", "a/b", "../x", "-x", "host:80"} {
		if _, err := Path(bad); err == nil {
			t.Errorf("Path(%q) accepted", bad)
		}
	}
}

// scripted answers pulls from a list and records the since of each.
type scripted struct {
	mu      sync.Mutex
	answers []server.Changes
	since   []int64
}

func (c *scripted) Pull(ctx context.Context, since int64, wait time.Duration, limit int) (server.Changes, error) {
	c.mu.Lock()
	c.since = append(c.since, since)
	if len(c.answers) > 0 {
		a := c.answers[0]
		c.answers = c.answers[1:]
		c.mu.Unlock()
		return a, nil
	}
	c.mu.Unlock()
	<-ctx.Done()
	return server.Changes{}, ctx.Err()
}

func (c *scripted) Push(context.Context, []store.Change) ([]store.Applied, error) { return nil, nil }

func (c *scripted) pulls() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.since...)
}

func TestServerBehindTheReplicaMakesItStartOver(t *testing.T) {
	stamp := hlc.Stamp{Millis: time.Now().UnixMilli(), Device: "00000000000000cc"}.String()
	row := server.Row{ID: "01JDSERVER0000000000000000", Version: 1, Content: "restored from an old backup",
		Created: time.Now().UTC(), Status: store.Active, Source: "agent", Seq: 5,
		Stamps: map[string]string{store.RegContent: stamp, store.RegStatus: stamp}}
	c := &scripted{answers: []server.Changes{
		{BaseID: "base", Head: 5, Rows: []server.Row{row}},
		{BaseID: "base", Head: 2}, // the same base, behind the replica
	}}
	_, path := startSyncer(t, c)
	eventually(t, "a pull from the start", func() bool { p := c.pulls(); return len(p) >= 3 })
	if p := c.pulls(); p[0] != 0 || p[1] != 5 || p[2] != 0 {
		t.Fatalf("pulls from %v, want 0, 5, then 0 again", p)
	}
	if _, err := agent(t, path).Get(row.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a row the server no longer has survived: %v", err)
	}
}
