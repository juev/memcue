//go:build research

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/juev/memcue/internal/store"
)

// A machine is one copy of the base on one host, kept two ways side by side: sqlite is a clone of the repository that
// tracks memory/facts.db with memcue's merge driver, events a clone of the repository that tracks the event log. The
// writes go to the SQLite base; the event log records them. After a merge the derived base of the event log is
// materialised separately and compared with what the driver produced.
type machine struct {
	name    string
	sqlite  string // work tree of the SQLite repository
	events  string // work tree of the event-log repository
	derived string // base materialised from the event log
	mapPath string
}

func (m *machine) db() string { return filepath.Join(m.sqlite, "memory", "facts.db") }

type world struct {
	dir    string
	memcue string
	a, b   *machine
}

func (w *world) originSQLite() string { return filepath.Join(w.dir, "origin-sqlite.git") }
func (w *world) originEvents() string { return filepath.Join(w.dir, "origin-events.git") }

func newMachine(dir, name string) *machine {
	return &machine{name: name, sqlite: filepath.Join(dir, name, "sqlite"), events: filepath.Join(dir, name, "events"),
		derived: filepath.Join(dir, name, "derived.db"), mapPath: filepath.Join(dir, name, "events.map")}
}

func (w *world) configureDriver(dir string) error {
	for _, kv := range [][2]string{{"merge.memcue.name", "memcue fact base"},
		{"merge.memcue.driver", w.memcue + " merge %O %A %B"}, {"diff.memcue.textconv", w.memcue + " dump"},
		{"gc.auto", "0"}, {"pull.rebase", "false"}} {
		if err := git(dir, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// setup creates both origins, machine A with the base, and machine B as a clone of both.
func (w *world) setup(base func(*store.Store) (map[string]string, error)) (map[string]string, error) {
	for _, o := range []string{w.originSQLite(), w.originEvents()} {
		if err := git(w.dir, "init", "-q", "--bare", "-b", "main", o); err != nil {
			return nil, err
		}
	}
	w.a, w.b = newMachine(w.dir, "a"), newMachine(w.dir, "b")
	a := w.a
	for _, d := range []string{a.sqlite, a.events} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
		if err := git(d, "init", "-q", "-b", "main"); err != nil {
			return nil, err
		}
		if err := git(d, "config", "gc.auto", "0"); err != nil {
			return nil, err
		}
	}
	if err := w.configureDriver(a.sqlite); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(a.sqlite, ".gitattributes"), []byte("memory/facts.db merge=memcue diff=memcue\n"),
		0o600); err != nil {
		return nil, err
	}
	var ids map[string]string
	err := a.write(func(s *store.Store) error {
		var err error
		ids, err = base(s)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := a.recordAndCommit("base", EventMap{}); err != nil {
		return nil, err
	}
	for _, p := range [][2]string{{a.sqlite, w.originSQLite()}, {a.events, w.originEvents()}} {
		if err := git(p[0], "push", "-q", "file://"+p[1], "main"); err != nil {
			return nil, err
		}
		if err := git(p[0], "remote", "add", "origin", "file://"+p[1]); err != nil {
			return nil, err
		}
		if err := git(p[0], "fetch", "-q", "origin"); err != nil {
			return nil, err
		}
	}
	b := w.b
	for _, p := range [][2]string{{w.originSQLite(), b.sqlite}, {w.originEvents(), b.events}} {
		if err := git(w.dir, "clone", "-q", "file://"+p[0], p[1]); err != nil {
			return nil, err
		}
		if err := git(p[1], "config", "gc.auto", "0"); err != nil {
			return nil, err
		}
	}
	if err := w.configureDriver(b.sqlite); err != nil {
		return nil, err
	}
	for _, m := range []*machine{a, b} {
		same, err := m.materialize()
		if err != nil {
			return nil, err
		}
		if !same {
			return nil, fmt.Errorf("setup: the event log of %s does not give its base", m.name)
		}
	}
	return ids, nil
}

// write runs f on the machine's SQLite base, then stamps the versions it wrote with the machine's name: memcue takes
// the author from the host name, and both simulated machines run on one host.
func (m *machine) write(f func(*store.Store) error) error {
	before := map[string]bool{}
	if _, err := os.Stat(m.db()); err == nil {
		rows, err := exportRows(m.db())
		if err != nil {
			return err
		}
		for _, r := range rows {
			before[mapKey(r.ID, r.Version)] = true
		}
	}
	s, err := store.Open(m.db())
	if err != nil {
		return err
	}
	if err := f(s); err != nil {
		_ = s.Close()
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	rows, err := exportRows(m.db())
	if err != nil {
		return err
	}
	db, err := openRaw(m.db(), false)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	for _, r := range rows {
		if !before[mapKey(r.ID, r.Version)] {
			if _, err := db.Exec(`UPDATE facts SET author = ? WHERE id = ? AND version = ?`, "machine-"+m.name, r.ID,
				r.Version); err != nil {
				return err
			}
		}
	}
	return nil
}

// recordAndCommit records the new versions as events and commits both repositories.
func (m *machine) recordAndCommit(msg string, events EventMap) error {
	if loaded, err := loadMap(m.mapPath); err == nil {
		events = loaded
	}
	rows, err := exportRows(m.db())
	if err != nil {
		return err
	}
	if _, err := record(m.events, rows, events); err != nil {
		return err
	}
	if err := saveMap(m.mapPath, events); err != nil {
		return err
	}
	for _, d := range []string{m.sqlite, m.events} {
		if err := git(d, "add", "--all", "."); err != nil {
			return err
		}
		if err := git(d, "commit", "-q", "--allow-empty", "--no-verify", "-m", m.name+": "+msg); err != nil {
			return err
		}
	}
	return nil
}

// materialize rebuilds the derived base from the event log, saves the map and says whether the derived rows equal
// the rows of the SQLite base (the driver's result after a merge).
func (m *machine) materialize() (bool, error) {
	evs, err := loadEvents(m.events)
	if err != nil {
		return false, err
	}
	rows, em, err := materialize(evs)
	if err != nil {
		return false, err
	}
	if err := importRows(rows, m.derived); err != nil {
		return false, err
	}
	if err := saveMap(m.mapPath, em); err != nil {
		return false, err
	}
	got, err := exportRows(m.derived)
	if err != nil {
		return false, err
	}
	want, err := exportRows(m.db())
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(got, want), nil
}

// pushPull sends the machine's commits to the origins, merging first if the origin moved: the order of two syncs.
func (m *machine) sync() (conflict bool, err error) {
	for _, d := range []string{m.sqlite, m.events} {
		if err := git(d, "fetch", "-q", "origin"); err != nil {
			return false, err
		}
		if err := git(d, "merge", "-q", "--no-edit", "origin/main"); err != nil {
			return true, err
		}
		if err := git(d, "push", "-q", "origin", "HEAD:main"); err != nil {
			return false, err
		}
	}
	return false, nil
}

type round struct {
	name   string
	bFirst bool // B writes before A in wall time
	a, b   func(*store.Store, map[string]string) error
}

type scenario struct {
	name   string
	rounds []round
	watch  string // the fact whose final state the scenario is about
}

type orderResult struct {
	Conflict bool   `json:"conflict"`
	Error    string `json:"error,omitempty"`
	rows     []Row
	evRows   []Row
}

type roundResult struct {
	Scenario         string         `json:"scenario"`
	Round            string         `json:"round"`
	SQLiteOrderEqual bool           `json:"sqlite_both_orders_equal"`
	EventsOrderEqual bool           `json:"events_both_orders_equal"`
	SQLiteVsEvents   bool           `json:"sqlite_equals_events"`
	SQLiteNoLoss     bool           `json:"sqlite_no_lost_or_invented_versions"`
	EventsNoLoss     bool           `json:"events_no_lost_or_invented_versions"`
	Contiguous       bool           `json:"versions_contiguous"`
	IndexOK          bool           `json:"index_matches_live_facts"`
	Idempotent       *bool          `json:"remerge_changes_nothing,omitempty"`
	RemergeExtra     int            `json:"remerge_extra_rows,omitempty"`
	Watch            map[string]any `json:"watched_fact,omitempty"`
	TextConflicts    int            `json:"text_single_line_merge_conflicts"`
	TextUnion        string         `json:"text_single_union_merge"`
	Errors           []string       `json:"errors,omitempty"`
}

func sig(r Row) string {
	r.Version = 0
	return string(encodeLine(r))
}

// expected is the spec the merge is checked against, independent of how Merge works: every version either side
// holds is in the result, as many times as the side that holds it most often, and nothing else is.
func expected(a, b []Row) map[string]int {
	ca, cb := map[string]int{}, map[string]int{}
	for _, r := range a {
		ca[sig(r)]++
	}
	for _, r := range b {
		cb[sig(r)]++
	}
	out := map[string]int{}
	for k, v := range ca {
		out[k] = v
	}
	for k, v := range cb {
		out[k] = max(out[k], v)
	}
	return out
}

func counts(rows []Row) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[sig(r)]++
	}
	return out
}

func contiguous(rows []Row) bool {
	next := map[string]int{}
	for _, r := range rows { // ordered by id, version
		if r.Version != next[r.ID]+1 {
			return false
		}
		next[r.ID] = r.Version
	}
	return true
}

func latest(rows []Row, id string) (Row, int) {
	var out Row
	n := 0
	for _, r := range rows {
		if r.ID == id {
			out = r
			n++
		}
	}
	return out, n
}

func copyTree(src, dst string) error {
	if out, err := exec.Command("cp", "-a", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("cp %s: %v: %s", src, err, out)
	}
	return nil
}

// runOrder syncs first then second (first pushes, second merges and pushes, first pulls) in the world at dir and
// returns the base both end with.
func runOrder(w *world, first, second *machine) orderResult {
	var res orderResult
	for _, m := range []*machine{first, second, first} {
		conflict, err := m.sync()
		if err != nil {
			res.Conflict = res.Conflict || conflict
			res.Error = err.Error()
			return res
		}
	}
	for _, m := range []*machine{first, second} {
		if _, err := m.materialize(); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	ra, err := exportRows(first.db())
	if err != nil {
		res.Error = err.Error()
		return res
	}
	rb, err := exportRows(second.db())
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if !reflect.DeepEqual(ra, rb) {
		res.Error = "the two machines ended with different SQLite bases"
	}
	ea, err := exportRows(first.derived)
	if err != nil {
		res.Error = strings.TrimSpace(res.Error + "; " + err.Error())
		return res
	}
	eb, err := exportRows(second.derived)
	if err != nil {
		res.Error = strings.TrimSpace(res.Error + "; " + err.Error())
		return res
	}
	if !reflect.DeepEqual(ea, eb) {
		res.Error = strings.TrimSpace(res.Error + "; the two machines derived different bases from the event log")
	}
	res.rows, res.evRows = ra, ea
	return res
}

// machines returns the machines of a copy of a world at dir, their remotes pointed at the copy's own origins: the
// clones name their origin by absolute path, and a copy must not push into the world it was copied from.
func (w *world) machines(dir string) (*machine, *machine, error) {
	a, b := newMachine(dir, "a"), newMachine(dir, "b")
	for _, m := range []*machine{a, b} {
		for _, p := range [][2]string{{m.sqlite, "origin-sqlite.git"}, {m.events, "origin-events.git"}} {
			if err := git(p[0], "remote", "set-url", "origin", "file://"+filepath.Join(dir, p[1])); err != nil {
				return nil, nil, err
			}
		}
	}
	return a, b, nil
}

// remerge merges the pre-merge bases into a copy of the result with store.Merge, the code the driver runs, and says
// whether that changed any row: a second merge of inputs already merged must be a no-op.
func remerge(dir, result string, inputs ...string) (bool, int, error) {
	tmp := filepath.Join(dir, "remerge.db")
	if err := copyFile(result, tmp); err != nil {
		return false, 0, err
	}
	want, err := exportRows(result)
	if err != nil {
		return false, 0, err
	}
	s, err := store.Open(tmp)
	if err != nil {
		return false, 0, err
	}
	for _, in := range inputs {
		if _, err := s.Merge(in); err != nil {
			_ = s.Close()
			return false, 0, err
		}
	}
	if err := s.Close(); err != nil {
		return false, 0, err
	}
	got, err := exportRows(tmp)
	return reflect.DeepEqual(got, want), len(got) - len(want), err
}

// indexOK opens a copy of the base, so the check cannot touch the tracked file, and compares the index with the live
// facts.
func indexOK(dir, base string) bool {
	tmp := filepath.Join(dir, "index-check.db")
	if err := copyFile(base, tmp); err != nil {
		return false
	}
	s, err := store.Open(tmp)
	if err != nil {
		return false
	}
	defer func() { _ = s.Close() }()
	st, err := s.IndexStats()
	return err == nil && st.Live == st.Rows && st.Live > 0
}

// textMerge merges the single-file JSONL of the base and both sides with git merge-file, as git's line merge would,
// and with --union, as merge=union would.
func textMerge(dir string, base, a, b []Row, want map[string]int) (int, string, error) {
	files := map[string][]Row{"base.jsonl": base, "a.jsonl": a, "b.jsonl": b}
	for n, rows := range files {
		if err := os.WriteFile(filepath.Join(dir, n), encodeJSONL(rows), 0o600); err != nil {
			return 0, "", err
		}
	}
	merge := func(extra ...string) ([]byte, int, error) {
		args := append([]string{"merge-file", "-p"}, extra...)
		args = append(args, filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "base.jsonl"), filepath.Join(dir, "b.jsonl"))
		out, err := exec.Command("git", args...).Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out, exit.ExitCode(), nil
		}
		return out, 0, err
	}
	_, conflicts, err := merge()
	if err != nil {
		return 0, "", err
	}
	out, _, err := merge("--union")
	if err != nil {
		return conflicts, "", err
	}
	rows, err := decodeJSONL(out)
	switch {
	case err != nil:
		return conflicts, "refused: " + ulid.ReplaceAllString(err.Error(), "<id>"), nil
	case !reflect.DeepEqual(counts(rows), want):
		return conflicts, "parses, but the versions differ from the expected union", nil
	case !contiguous(rows):
		return conflicts, "parses, versions not contiguous", nil
	}
	return conflicts, "ok", nil
}

// ulid matches a fact id, so that the published results carry no ids, not even synthetic ones.
var ulid = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)

func runScenario(root, memcue string, sc scenario, base func(*store.Store) (map[string]string, error)) ([]roundResult, error) {
	dir := filepath.Join(root, sc.name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	w := &world{dir: filepath.Join(dir, "w"), memcue: memcue}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return nil, err
	}
	ids, err := w.setup(base)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sc.name, err)
	}
	var out []roundResult
	for ri, rd := range sc.rounds {
		res := roundResult{Scenario: sc.name, Round: rd.name}
		common, err := exportRows(w.a.db())
		if err != nil {
			return nil, err
		}
		steps := []struct {
			m *machine
			f func(*store.Store, map[string]string) error
		}{{w.a, rd.a}, {w.b, rd.b}}
		if rd.bFirst {
			steps[0], steps[1] = steps[1], steps[0]
		}
		for _, st := range steps {
			if st.f == nil {
				continue
			}
			if err := st.m.write(func(s *store.Store) error { return st.f(s, ids) }); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("write on %s: %v", st.m.name, err))
				return append(out, res), nil
			}
		}
		for _, m := range []*machine{w.a, w.b} {
			if err := m.recordAndCommit(rd.name, EventMap{}); err != nil {
				return nil, err
			}
		}
		preA, err := exportRows(w.a.db())
		if err != nil {
			return nil, err
		}
		preB, err := exportRows(w.b.db())
		if err != nil {
			return nil, err
		}
		want := expected(preA, preB)
		tdir := filepath.Join(dir, fmt.Sprintf("text-%d", ri))
		if err := os.MkdirAll(tdir, 0o700); err != nil {
			return nil, err
		}
		if res.TextConflicts, res.TextUnion, err = textMerge(tdir, common, preA, preB, want); err != nil {
			return nil, err
		}

		// Order 2 runs on a copy of this state: B syncs first.
		copyDir := filepath.Join(dir, fmt.Sprintf("order2-%d", ri))
		if err := copyTree(w.dir, copyDir); err != nil {
			return nil, err
		}
		preAFile, preBFile := filepath.Join(dir, fmt.Sprintf("preA-%d.db", ri)), filepath.Join(dir, fmt.Sprintf("preB-%d.db", ri))
		if err := copyFile(w.a.db(), preAFile); err != nil {
			return nil, err
		}
		if err := copyFile(w.b.db(), preBFile); err != nil {
			return nil, err
		}
		a2, b2, err := w.machines(copyDir)
		if err != nil {
			return nil, err
		}
		o2 := runOrder(&world{dir: copyDir, memcue: memcue}, b2, a2)
		o1 := runOrder(w, w.a, w.b)
		if o1.Error != "" {
			res.Errors = append(res.Errors, "order A then B: "+o1.Error)
		}
		if o2.Error != "" {
			res.Errors = append(res.Errors, "order B then A: "+o2.Error)
		}
		res.SQLiteOrderEqual = o1.rows != nil && reflect.DeepEqual(o1.rows, o2.rows)
		res.EventsOrderEqual = o1.evRows != nil && reflect.DeepEqual(o1.evRows, o2.evRows)
		res.SQLiteVsEvents = o1.rows != nil && reflect.DeepEqual(o1.rows, o1.evRows)
		res.SQLiteNoLoss = reflect.DeepEqual(counts(o1.rows), want)
		res.EventsNoLoss = reflect.DeepEqual(counts(o1.evRows), want)
		res.Contiguous = contiguous(o1.rows) && contiguous(o1.evRows)
		res.IndexOK = indexOK(dir, w.a.db()) && indexOK(dir, w.a.derived)
		if sc.watch != "" {
			r, n := latest(o1.rows, ids[sc.watch])
			res.Watch = map[string]any{"fact": sc.watch, "versions": n, "status": r.Status, "last_author": r.Author}
		}
		same, extra, err := remerge(dir, w.a.db(), preAFile, preBFile)
		if err != nil {
			res.Errors = append(res.Errors, "remerge: "+err.Error())
		}
		res.Idempotent, res.RemergeExtra = &same, extra
		if !res.SQLiteVsEvents {
			// The event log is checked against the driver round by round; once they disagree, the later rounds of
			// this scenario would record events against a base the log did not produce.
			out = append(out, res)
			break
		}
		out = append(out, res)
	}
	return out, nil
}

func baseFacts(s *store.Store) (map[string]string, error) {
	g := newGen(7)
	ids := map[string]string{}
	for i := range 12 {
		in := g.input()
		f, err := s.AddWith(in.Content, in.Keywords, in.Origin, in.Expires, in.Meta)
		if err != nil {
			return nil, err
		}
		ids[fmt.Sprintf("f%d", i)] = f.ID
	}
	if _, err := s.SetQueries(ids["f0"], 1, []string{"first query", "second query"}); err != nil {
		return nil, err
	}
	if _, err := s.Retire(ids["f3"]); err != nil {
		return nil, err
	}
	return ids, nil
}

func correct(name, text string) func(*store.Store, map[string]string) error {
	return func(s *store.Store, ids map[string]string) error {
		cur, err := s.Get(ids[name])
		if err != nil {
			return err
		}
		_, err = s.Correct(ids[name], cur.Content+" "+text, cur.Keywords)
		return err
	}
}

func retire(name string) func(*store.Store, map[string]string) error {
	return func(s *store.Store, ids map[string]string) error { _, err := s.Retire(ids[name]); return err }
}

func restore(name string) func(*store.Store, map[string]string) error {
	return func(s *store.Store, ids map[string]string) error { _, err := s.Restore(ids[name]); return err }
}

func queries(name string, qs ...string) func(*store.Store, map[string]string) error {
	return func(s *store.Store, ids map[string]string) error {
		cur, err := s.Get(ids[name])
		if err != nil {
			return err
		}
		_, err = s.SetQueries(ids[name], cur.Version, qs)
		return err
	}
}

func relabel(name, kind, scope string) func(*store.Store, map[string]string) error {
	return func(s *store.Store, ids map[string]string) error {
		cur, err := s.Get(ids[name])
		if err != nil {
			return err
		}
		_, err = s.Relabel(ids[name], cur.Version, kind, scope, cur.Expires)
		return err
	}
}

func adds(n int, seed uint64) func(*store.Store, map[string]string) error {
	return func(s *store.Store, _ map[string]string) error {
		g := newGen(seed)
		for range n {
			in := g.input()
			if _, err := s.AddWith(in.Content, in.Keywords, in.Origin, in.Expires, in.Meta); err != nil {
				return err
			}
		}
		return nil
	}
}

func seq(fs ...func(*store.Store, map[string]string) error) func(*store.Store, map[string]string) error {
	return func(s *store.Store, ids map[string]string) error {
		for _, f := range fs {
			if err := f(s, ids); err != nil {
				return err
			}
		}
		return nil
	}
}

func scenarios() []scenario {
	return []scenario{
		{name: "independent-adds", rounds: []round{{name: "adds", a: adds(3, 11), b: adds(3, 12)}}},
		{name: "concurrent-correct", watch: "f1",
			rounds: []round{{name: "correct-both", a: correct("f1", "fixed on a"), b: correct("f1", "fixed on b")}}},
		{name: "retire-then-correct", watch: "f2",
			rounds: []round{{name: "a-retires-b-corrects-later", a: retire("f2"), b: correct("f2", "still true")}}},
		{name: "correct-then-retire", watch: "f2",
			rounds: []round{{name: "b-corrects-a-retires-later", bFirst: true, a: retire("f2"), b: correct("f2", "x")}}},
		{name: "reopen-vs-retire", watch: "f3",
			rounds: []round{{name: "a-reopens-b-retires-later", a: restore("f3"), b: retire("f3")}}},
		{name: "retire-vs-reopen", watch: "f3",
			rounds: []round{{name: "b-retires-a-reopens-later", bFirst: true, a: restore("f3"), b: retire("f3")}}},
		{name: "relabel-vs-queries", watch: "f5",
			rounds: []round{{name: "same-version", a: relabel("f5", "pitfall", "global"), b: queries("f5", "q on b")}}},
		{name: "relabel-revert", watch: "f6",
			rounds: []round{{name: "a-relabels-and-back", a: seq(relabel("f6", "event", "global"), func(s *store.Store,
				ids map[string]string) error {
				h, err := s.History(ids["f6"])
				if err != nil {
					return err
				}
				return relabel("f6", h[0].Kind, h[0].Scope)(s, ids)
			}), b: correct("f6", "b")}}},
		{name: "repeated", watch: "f7", rounds: []round{
			{name: "1-correct-both", a: correct("f7", "a1"), b: correct("f7", "b1")},
			{name: "2-a-retires-b-corrects-later", a: retire("f7"), b: seq(correct("f7", "b2"), queries("f8", "b2"))},
			{name: "3-a-retires-again-b-adds", a: retire("f7"), b: adds(2, 13)},
			{name: "4-no-writes"},
		}},
	}
}

// staleResult is the outcome of runStale.
type staleResult struct {
	Scenario       string   `json:"scenario"`
	RowsExpected   int      `json:"rows_expected"`
	SQLiteRows     int      `json:"sqlite_rows"`
	SQLiteDupes    int      `json:"sqlite_duplicate_versions"`
	SQLiteNoLoss   bool     `json:"sqlite_no_lost_or_invented_versions"`
	EventsRows     int      `json:"events_rows"`
	EventsNoLoss   bool     `json:"events_no_lost_or_invented_versions"`
	WatchedStatus  string   `json:"watched_fact_status"`
	WatchedHistory []string `json:"watched_fact_history"`
	Errors         []string `json:"errors,omitempty"`
}

// runStale is the sequence the round scenarios do not cover: both machines correct one fact, B later; B pushes; A
// merges (B's correction becomes version 3) and pushes; B, before it pulls, commits one more fact, so its pull is a
// real merge of B's pre-merge base with A's merged one, and the driver runs again.
func runStale(root, memcue string) (staleResult, error) {
	res := staleResult{Scenario: "stale-side-writes-again"}
	w := &world{dir: filepath.Join(root, "stale", "w"), memcue: memcue}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return res, err
	}
	ids, err := w.setup(baseFacts)
	if err != nil {
		return res, err
	}
	if err := w.a.write(func(s *store.Store) error { return correct("f9", "fixed on a")(s, ids) }); err != nil {
		return res, err
	}
	if err := w.b.write(func(s *store.Store) error { return correct("f9", "fixed on b, later")(s, ids) }); err != nil {
		return res, err
	}
	for _, m := range []*machine{w.a, w.b} {
		if err := m.recordAndCommit("correct f9", EventMap{}); err != nil {
			return res, err
		}
	}
	preA, err := exportRows(w.a.db())
	if err != nil {
		return res, err
	}
	preB, err := exportRows(w.b.db())
	if err != nil {
		return res, err
	}
	if _, err := w.b.sync(); err != nil {
		return res, err
	}
	if _, err := w.a.sync(); err != nil { // A merges: B's correction is renumbered to version 3
		return res, err
	}
	if _, err := w.a.materialize(); err != nil {
		return res, err
	}
	if err := w.b.write(func(s *store.Store) error { return adds(1, 21)(s, ids) }); err != nil {
		return res, err
	}
	if err := w.b.recordAndCommit("one more fact", EventMap{}); err != nil {
		return res, err
	}
	extra, err := exportRows(w.b.db())
	if err != nil {
		return res, err
	}
	if _, err := w.b.sync(); err != nil {
		res.Errors = append(res.Errors, "b sync: "+err.Error())
	}
	if _, err := w.a.sync(); err != nil {
		res.Errors = append(res.Errors, "a sync: "+err.Error())
	}
	for _, m := range []*machine{w.a, w.b} {
		if _, err := m.materialize(); err != nil {
			res.Errors = append(res.Errors, "materialize: "+err.Error())
		}
	}
	want := expected(expectedRows(preA, preB), extra)
	final, err := exportRows(w.a.db())
	if err != nil {
		return res, err
	}
	ev, err := exportRows(w.a.derived)
	if err != nil {
		return res, err
	}
	n := 0
	for _, c := range want {
		n += c
	}
	res.RowsExpected, res.SQLiteRows, res.EventsRows = n, len(final), len(ev)
	for _, c := range counts(final) {
		if c > 1 {
			res.SQLiteDupes += c - 1
		}
	}
	res.SQLiteNoLoss = reflect.DeepEqual(counts(final), want)
	res.EventsNoLoss = reflect.DeepEqual(counts(ev), want)
	for _, r := range final {
		if r.ID == ids["f9"] {
			res.WatchedStatus = r.Status
			res.WatchedHistory = append(res.WatchedHistory, fmt.Sprintf("v%d %s by %s", r.Version, r.Status, r.Author))
		}
	}
	return res, nil
}

// expectedRows turns the expected multiset of two sides back into rows, for a second application of expected.
func expectedRows(a, b []Row) []Row {
	want := expected(a, b)
	var out []Row
	for _, r := range append(append([]Row(nil), a...), b...) {
		k := sig(r)
		if want[k] > 0 {
			want[k]--
			out = append(out, r)
		}
	}
	return out
}

func runSemantics(args []string) error {
	fl := flag.NewFlagSet("semantics", flag.ContinueOnError)
	out := fl.String("out", "", "new scratch directory")
	memcue := fl.String("memcue", "", "memcue binary for the merge driver (built from this tree)")
	result := fl.String("result", "", "JSON file for the results")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *out == "" || *memcue == "" || *result == "" {
		return errors.New("semantics: -out, -memcue and -result are required")
	}
	if err := newDir(*out); err != nil {
		return err
	}
	var all []roundResult
	for _, sc := range scenarios() {
		rs, err := runScenario(*out, *memcue, sc, baseFacts)
		if err != nil {
			return err
		}
		all = append(all, rs...)
		fmt.Fprintf(os.Stderr, "%s: %d rounds\n", sc.name, len(rs))
	}
	stale, err := runStale(*out, *memcue)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(map[string]any{"rounds": all, "stale": stale}, "", " ")
	return writeResult(*result, append(b, '\n'))
}
