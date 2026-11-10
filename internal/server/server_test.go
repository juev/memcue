package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/juev/memcue/internal/hlc"
	"github.com/juev/memcue/internal/store"
)

type fixture struct {
	st   *store.Store
	path string
	srv  *Server
	url  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.db")
	st, err := store.OpenServer(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := New(st, log.New(io.Discard, "", 0), func(*http.Request) string { return "test-node" })
	srv.poll = 10 * time.Millisecond
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &fixture{st: st, path: path, srv: srv, url: ts.URL}
}

func replica(t *testing.T) *store.Store {
	t.Helper()
	r, err := store.OpenReplica(filepath.Join(t.TempDir(), "replica.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func outbox(t *testing.T, r *store.Store) []store.Change {
	t.Helper()
	out, err := r.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func post(t *testing.T, url string, body, out any) int {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func (f *fixture) push(t *testing.T, changes []store.Change) []store.Applied {
	t.Helper()
	var p Pushed
	if code := post(t, f.url+"/v1/changes", Push{Changes: changes}, &p); code != http.StatusOK {
		t.Fatalf("POST /v1/changes: %d", code)
	}
	return p.Results
}

func (f *fixture) get(t *testing.T, query string) (Changes, int) {
	t.Helper()
	resp, err := http.Get(f.url + "/v1/changes?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var c Changes
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
			t.Fatal(err)
		}
	}
	return c, resp.StatusCode
}

// fetch is get for a goroutine: it reports instead of failing the test.
func fetch(url string) (Changes, error) {
	resp, err := http.Get(url)
	if err != nil {
		return Changes{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var c Changes
	err = json.NewDecoder(resp.Body).Decode(&c)
	return c, err
}

// sync pushes the replica's outbox, acknowledges it and pulls every row through the wire.
func (f *fixture) sync(t *testing.T, r *store.Store) []store.Applied {
	t.Helper()
	res := f.push(t, outbox(t, r))
	for _, a := range res {
		if a.Refused == "" {
			if err := r.Acknowledge(a.OpID, a.Seq); err != nil {
				t.Fatal(err)
			}
		}
	}
	st, err := r.State()
	if err != nil {
		t.Fatal(err)
	}
	c, code := f.get(t, "since="+itoa(st.LastSeq))
	if code != http.StatusOK {
		t.Fatalf("GET: %d", code)
	}
	rows := make([]store.Row, 0, len(c.Rows))
	for _, row := range c.Rows {
		rows = append(rows, row.StoreRow())
	}
	if err := r.ApplyRows(c.BaseID, rows); err != nil {
		t.Fatal(err)
	}
	return res
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func createAt(at time.Time, content string) store.Change {
	id := ulid.MustNew(ulid.Timestamp(at), ulid.DefaultEntropy()).String()
	return store.Change{OpID: "op-" + id, ID: id, Create: true, Created: at, Source: "agent",
		Stamp: hlc.Stamp{Millis: at.UnixMilli(), Device: "00000000000000aa"}.String(),
		Values: map[string]string{store.RegContent: content, store.RegKind: "pitfall", store.RegScope: "global",
			store.RegExpires: "", store.RegStatus: store.Active, store.RegOrigin: "x 2026-11-10"}}
}

func TestRetryAfterALostReplyGetsTheEarlierAnswer(t *testing.T) {
	f := newFixture(t)
	r := replica(t)
	fact, err := r.AddWith("retry me", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Correct(fact.ID, "retry me, corrected", 1); err != nil {
		t.Fatal(err)
	}
	changes := outbox(t, r)
	first := f.push(t, changes)
	again := f.push(t, changes)
	if !reflect.DeepEqual(first, again) {
		t.Fatalf("retry answered %+v, first %+v", again, first)
	}
	c, _ := f.get(t, "since=0")
	if len(c.Rows) != 1 || c.Rows[0].Version != 2 || c.Rows[0].Content != "retry me, corrected" || c.Head != first[1].Seq {
		t.Fatalf("after a retry the server holds %+v", c)
	}
}

func TestConcurrentContentReachesReplicasWithTheLoser(t *testing.T) {
	f := newFixture(t)
	a, b := replica(t), replica(t)
	fact, err := a.AddWith("helper starts on demand", "x 2026-11-10", "", store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	f.sync(t, a)
	f.sync(t, b)
	if _, err := a.Correct(fact.ID, "helper starts on demand from session-start", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Correct(fact.ID, "helper starts on demand, detached", 1); err != nil {
		t.Fatal(err)
	}
	f.sync(t, a)
	res := f.sync(t, b)
	if len(res) != 1 || res[0].Loser == "" {
		t.Fatalf("no loser kept: %+v", res)
	}
	f.sync(t, a)
	winner, err := a.Get(fact.ID)
	if err != nil {
		t.Fatal(err)
	}
	loser, err := a.Get(res[0].Loser)
	if err != nil {
		t.Fatal(err)
	}
	if winner.Content == loser.Content || loser.Status != store.Active {
		t.Fatalf("winner %q, loser %+v", winner.Content, loser)
	}
	if of, err := a.LoserOf(loser.ID); err != nil || of != fact.ID {
		t.Fatalf("on the replica the loser refers to %q (%v)", of, err)
	}
}

func TestStampFarAheadIsRefusedAndNotRemembered(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	ahead := createAt(now.Add(time.Hour), "from a clock an hour ahead")
	res := f.push(t, []store.Change{ahead, createAt(now, "from a sound clock")})
	if res[0].Refused != RefusedAhead || res[1].Refused != "" || res[1].Seq == 0 {
		t.Fatalf("results %+v", res)
	}
	if _, err := f.st.Get(ahead.ID); err == nil {
		t.Fatal("a change stamped too far ahead was stored")
	}
	f.srv.now = func() time.Time { return now.Add(time.Hour) }
	if res := f.push(t, []store.Change{ahead}); res[0].Refused != "" {
		t.Fatalf("the same operation once the server's clock caught up: %+v", res)
	}
}

func TestCreateOfAnIDThatIsNotAULIDIsRefused(t *testing.T) {
	f := newFixture(t)
	ch := createAt(time.Now(), "bad id")
	ch.ID = "not-a-ulid"
	if res := f.push(t, []store.Change{ch}); res[0].Refused != store.RefusedMalformed {
		t.Fatalf("results %+v", res)
	}
}

func TestImportLoadsAnEmptyBaseOnly(t *testing.T) {
	f := newFixture(t)
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	facts := []Row{
		{ID: ulid.MustNew(ulid.Timestamp(created), ulid.DefaultEntropy()).String(), Version: 3, Content: "imported one",
			Origin: "x 2026-10-01", Created: created, Status: store.Active, Kind: "rule", Scope: "global", Source: "user"},
		{ID: ulid.MustNew(ulid.Timestamp(created), ulid.DefaultEntropy()).String(), Version: 2, Content: "imported two",
			Created: created.Add(time.Minute), Status: store.Retired},
	}
	var got Imported
	if code := post(t, f.url+"/v1/import", Import{Facts: facts}, &got); code != http.StatusOK || got.Facts != 2 {
		t.Fatalf("import: %d %+v", code, got)
	}
	c, _ := f.get(t, "since=0")
	if len(c.Rows) != 2 {
		t.Fatalf("rows %+v", c.Rows)
	}
	for i, row := range c.Rows {
		st, err := hlc.Parse(row.Stamps[store.RegContent])
		if err != nil || st.Millis != facts[i].Created.UnixMilli() || row.Version != facts[i].Version ||
			row.Status != facts[i].Status || len(row.Stamps) != len(store.Registers) {
			t.Fatalf("row %d: %+v (%v)", i, row, err)
		}
	}
	if code := post(t, f.url+"/v1/import", Import{Facts: facts[:1]}, nil); code != http.StatusConflict {
		t.Fatalf("import into a base with facts: %d", code)
	}
	if c, _ := f.get(t, "since=0"); len(c.Rows) != 2 {
		t.Fatalf("a refused import stored something: %d rows", len(c.Rows))
	}

	empty := newFixture(t)
	bad := append([]Row{facts[0]}, Row{ID: facts[1].ID, Content: "x", Created: created, Status: "gone"})
	if code := post(t, empty.url+"/v1/import", Import{Facts: bad}, nil); code != http.StatusBadRequest {
		t.Fatalf("import with an invalid fact: %d", code)
	}
	if c, _ := empty.get(t, "since=0"); len(c.Rows) != 0 {
		t.Fatalf("an invalid import stored %d rows", len(c.Rows))
	}
}

func TestLongPollAnswersACommitOfAnotherProcess(t *testing.T) {
	f := newFixture(t)
	type answer struct {
		c       Changes
		err     error
		elapsed time.Duration
	}
	done := make(chan answer, 1)
	start := time.Now()
	go func() {
		c, err := fetch(f.url + "/v1/changes?since=0&wait=10s")
		done <- answer{c, err, time.Since(start)}
	}()
	time.Sleep(200 * time.Millisecond)
	// A second handle has its own connection, as a dream run in another process does.
	other, err := store.OpenServer(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := other.AddWith("written by another process", "x 2026-11-10", "", store.Meta{}); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-done:
		if a.err != nil || len(a.c.Rows) != 1 || a.c.Rows[0].Content != "written by another process" ||
			a.elapsed < 200*time.Millisecond {
			t.Fatalf("long poll answered %+v (%v) after %s", a.c, a.err, a.elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long poll did not notice a commit of another process")
	}
}

func TestLongPollAnswersAPushAtOnce(t *testing.T) {
	f := newFixture(t)
	f.srv.poll = time.Hour // only the wake of this process can answer
	done := make(chan Changes, 1)
	go func() {
		c, _ := fetch(f.url + "/v1/changes?since=0&wait=10s")
		done <- c
	}()
	time.Sleep(100 * time.Millisecond)
	f.push(t, []store.Change{createAt(time.Now(), "pushed while a client waits")})
	select {
	case c := <-done:
		if len(c.Rows) != 1 {
			t.Fatalf("long poll answered %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long poll did not answer a push")
	}
}

func TestLongPollWithoutChangesAnswersAfterTheWait(t *testing.T) {
	f := newFixture(t)
	f.push(t, []store.Change{createAt(time.Now(), "the only fact")})
	start := time.Now()
	c, code := f.get(t, "since=1&wait=300ms")
	if code != http.StatusOK || len(c.Rows) != 0 || c.Head != 1 || c.BaseID == "" || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("answer %d %+v after %s", code, c, time.Since(start))
	}
	start = time.Now()
	if c, _ := f.get(t, "since=7&wait=10s"); c.Head != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("since above the head waited %s: %+v", time.Since(start), c)
	}
}

func TestBadRequests(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"since=-1", "since=x", "limit=0", "limit=99999", "wait=1h", "wait=soon"} {
		if _, code := f.get(t, q); code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, code)
		}
	}
	resp, err := http.Post(f.url+"/v1/changes", "application/json", bytes.NewReader([]byte("{")))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed body: %d", resp.StatusCode)
	}
	for path, want := range map[string]int{"/v1/other": http.StatusNotFound, "/v1/import": http.StatusMethodNotAllowed} {
		resp, err := http.Get(f.url + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s: %d, want %d", path, resp.StatusCode, want)
		}
	}
}
