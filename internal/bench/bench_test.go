// Package bench drives the built memcue binary the way an agent does, through `memcue search -json`, and holds it to
// what README and docs promise (#451). It is a black box on purpose: the unit tests of cmd/memcue call runSearch
// inside the test process, so a flag that is parsed differently, an environment variable that leaks or a default that
// only main applies never reaches them. Here every fact goes in through `memcue add` and comes back out through the
// JSON a caller parses, never through the human line, whose key is the fact's origin and not its scope.
//
// The binary is MEMCUE_BIN when it is set, so the same cases can judge a release or another branch's build; otherwise
// it is built once from this tree.
package bench

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// moduleRoot is the tree the binary is built from: a test runs in its package's directory, two levels below.
var moduleRoot = filepath.Join("..", "..")

var (
	binOnce sync.Once
	binPath string
	binErr  error
	// buildDir holds the binary this run built; TestMain removes it after the last test.
	buildDir string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// binary returns the memcue under test. It runs inside a test rather than in TestMain because go test records the
// files a test reads only after m.Run starts, and those files are what keeps a cached pass honest: the bench imports
// none of memcue's packages, so without them a change to cmd/ or internal/ would be served the result of the old
// binary.
func binary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		if bin := os.Getenv("MEMCUE_BIN"); bin != "" {
			// A stat is enough for the cache: a rebuilt binary changes its size or its time.
			if _, binErr = os.Stat(bin); binErr == nil {
				binPath = bin
			}
			return
		}
		if binErr = readSources(); binErr != nil {
			return
		}
		if buildDir, binErr = os.MkdirTemp("", "memcue-bench-"); binErr != nil {
			return
		}
		binPath = filepath.Join(buildDir, "memcue")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/memcue")
		cmd.Dir = moduleRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = &buildError{err: err, out: out}
		}
	})
	if binErr != nil {
		t.Fatalf("memcue binary: %v", binErr)
	}
	return binPath
}

type buildError struct {
	err error
	out []byte
}

func (e *buildError) Error() string { return "go build: " + e.err.Error() + "\n" + string(e.out) }

// readSources reads every file the binary is built from, so go test keys its cache on them (see binary). The
// prompts under internal/extract are embedded, which is why every file counts and not only the Go ones.
func readSources() error {
	for _, name := range []string{"go.mod", "go.sum"} {
		if _, err := os.ReadFile(filepath.Join(moduleRoot, name)); err != nil {
			return err
		}
	}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(moduleRoot, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return err
			}
			_, err = os.ReadFile(path)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// base is one fact base with everything a memcue process reads around it: the file, the state directory, the Claude
// config directory and a home of its own.
type base struct {
	dir string
	env []string
}

// newBase makes an empty base in a temporary directory. The child's environment is the test's own without a single
// MEMCUE_ variable: the shell that runs the suite may be a gateway's, whose MEMCUE_SEARCH_LOG, MEMCUE_NUMBER_TERMS or
// MEMCUE_API_URL would change what a search returns or make add call a model over the network (the reasoning of
// env(t) in cmd/memcue). What the bench needs is then set explicitly: the review off, a fixed host name, so the host
// tie-break never depends on the machine, and HOME inside the base, so the default ~/.claude is never touched.
func newBase(t *testing.T) *base {
	t.Helper()
	dir := t.TempDir()
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "MEMCUE_") || strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "HOME=") ||
			strings.HasPrefix(kv, "XDG_STATE_HOME=") || strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			continue
		}
		env = append(env, kv)
	}
	b := &base{dir: dir, env: append(env,
		"HOME="+dir,
		"MEMCUE_FACTS="+filepath.Join(dir, "facts.db"),
		"XDG_STATE_HOME="+filepath.Join(dir, "state"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(dir, "claude"),
		"MEMCUE_VET=0",
		"MEMCUE_EXTRACT=0",
		"MEMCUE_AUTO_INSTALL=0",
		"MEMCUE_HOST=bench-host",
	)}
	return b
}

// gitEnv keeps a nested git away from the user's configuration and from the repository the suite runs in: a GIT_DIR
// inherited from a hook would point git init at that repository instead of the new one (see gitEnv in cmd/memcue).
func (b *base) gitEnv() []string {
	return append(slices.Clone(b.env), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1")
}

// repo is a repository a session can work in: its directory and the project key memcue names it by.
type repo struct {
	dir string
	key string
}

// newRepo makes a git repository in the base's directory whose origin is git@example.com:team/<name>.git, so its
// project key is example.com/team/<name> (#343). memcue reads the remote from the files and never runs git, but a
// repository git itself made is the one a session meets, so it is made by git.
func newRepo(t *testing.T, b *base, name string) repo {
	t.Helper()
	dir := filepath.Join(b.dir, name)
	for _, args := range [][]string{
		{"init", "-q", dir},
		{"-C", dir, "remote", "add", "origin", "git@example.com:team/" + name + ".git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = b.gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo{dir: dir, key: "example.com/team/" + name}
}

// run runs memcue in dir (the base's own directory when empty, which names no project) and returns its stdout and
// stderr. A non-zero exit fails the test: every command the bench runs is expected to succeed.
func (b *base) run(t *testing.T, dir string, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	cmd.Env = b.env
	cmd.Dir = dir
	if dir == "" {
		cmd.Dir = b.dir
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("memcue %q: %v\nstdout: %s\nstderr: %s", args, err, out.String(), errOut.String())
	}
	return out.String(), errOut.String()
}

// addOpts are the flags of `memcue add` the cases use; empty is the flag left out.
type addOpts struct {
	cwd      string // where add runs, so the default origin is that directory's project
	scope    string
	kind     string
	keywords string
	expires  string
}

// add stores a fact and returns its id, the one line add prints.
func add(t *testing.T, b *base, text string, o addOpts) string {
	t.Helper()
	args := []string{"add"}
	for _, f := range [][2]string{{"-scope", o.scope}, {"-kind", o.kind}, {"-keywords", o.keywords},
		{"-expires", o.expires}} {
		if f[1] != "" {
			args = append(args, f[0], f[1])
		}
	}
	out, _ := b.run(t, o.cwd, append(args, "--", text)...)
	id := strings.TrimSpace(out)
	if len(id) != 26 {
		t.Fatalf("add printed %q, want a fact id", out)
	}
	return id
}

// searchOpts are the flags of `memcue search`; empty is the flag left out, so the default is what is tested.
type searchOpts struct {
	cwd      string // the session's directory
	n        string
	minScore string
	in       string
	kind     string
	since    string
	expired  bool
}

func (o searchOpts) args() []string {
	var args []string
	for _, f := range [][2]string{{"-n", o.n}, {"-min-score", o.minScore}, {"-in", o.in}, {"-kind", o.kind},
		{"-since", o.since}} {
		if f[1] != "" {
			args = append(args, f[0], f[1])
		}
	}
	if o.expired {
		args = append(args, "-expired")
	}
	return args
}

// hit is one fact of `search -json`, by the field names of searchFact in cmd/memcue/facts.go. Raw keeps the object
// as printed, for the shape check.
type hit struct {
	ID       string                     `json:"id"`
	Content  string                     `json:"content"`
	Keywords []string                   `json:"keywords"`
	Origin   string                     `json:"origin"`
	Scope    string                     `json:"scope"`
	Created  string                     `json:"created"`
	Expires  string                     `json:"expires"`
	Raw      map[string]json.RawMessage `json:"-"`
}

// answer is what one search printed: the facts and the raw streams.
type answer struct {
	hits   []hit
	stdout string
	stderr string
}

func (a answer) ids() []string {
	ids := make([]string, 0, len(a.hits))
	for _, h := range a.hits {
		ids = append(ids, h.ID)
	}
	return ids
}

// search runs `memcue search -json` and parses its answer.
func search(t *testing.T, b *base, query string, o searchOpts) answer {
	t.Helper()
	args := append(append([]string{"search", "-json"}, o.args()...), "--", query)
	stdout, stderr := b.run(t, o.cwd, args...)
	var raws []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raws); err != nil {
		t.Fatalf("search -json %q printed %q: %v", query, stdout, err)
	}
	a := answer{stdout: stdout, stderr: stderr}
	for _, raw := range raws {
		obj, _ := json.Marshal(raw)
		var h hit
		if err := json.Unmarshal(obj, &h); err != nil {
			t.Fatalf("search -json %q: %v", query, err)
		}
		h.Raw = raw
		a.hits = append(a.hits, h)
	}
	return a
}

// world is a case's base and the names it gave its facts and repositories, so an expectation reads "own before
// global" rather than two ULIDs.
type world struct {
	b     *base
	facts map[string]string // name -> id
	repos map[string]repo
}

func newWorld(t *testing.T) *world {
	return &world{b: newBase(t), facts: map[string]string{}, repos: map[string]repo{}}
}

// add stores a fact under a name.
func (w *world) add(t *testing.T, name, text string, o addOpts) {
	t.Helper()
	w.facts[name] = add(t, w.b, text, o)
}

// repo makes a repository once and returns it on every later call.
func (w *world) repo(t *testing.T, name string) repo {
	t.Helper()
	if r, ok := w.repos[name]; ok {
		return r
	}
	r := newRepo(t, w.b, name)
	w.repos[name] = r
	return r
}

// id returns the id of a named fact, failing on a name the setup never gave.
func (w *world) id(t *testing.T, name string) string {
	t.Helper()
	id, ok := w.facts[name]
	if !ok {
		t.Fatalf("no fact named %q in the setup", name)
	}
	return id
}

// Strata of a case. A rule case holds the binary to a sentence of README or docs and is red only on a defect. A
// characterization case records what the binary does today on purpose, so a change to it is a decision someone sees
// rather than a drift no one notices.
const (
	rule             = "rule"
	characterization = "characterization"
)

// benchCase is one row of the table. Setup fills the base, action asks it, expect judges the answer. Facts are named
// in the setup and named again in expect.
type benchCase struct {
	id      string
	stratum string
	// note is the sentence a rule case holds the binary to, or what a characterization case records and why.
	note   string
	setup  func(t *testing.T, w *world)
	action func(t *testing.T, w *world) answer
	expect expect
}

// expect is what an answer must hold. Every field is optional; the zero value checks nothing.
type expect struct {
	found    []string    // facts that must be served
	notFound []string    // facts that must not be served
	order    [][2]string // pairs of facts: the first must be served before the second
	// most caps the number of facts served; zero is no cap, so "none at all" is spelled with none.
	most  int
	none  bool // the answer holds no fact
	shape bool // every fact has the fields of search -json, and nothing else
	// also is a check of its own, for what the fields above cannot say: two searches agreeing, or a stream the case
	// records.
	also func(t *testing.T, w *world, a answer)
}

// check judges an answer and reports every failed expectation, not only the first: a red case should say all it
// knows.
func (e expect) check(t *testing.T, w *world, a answer) {
	t.Helper()
	got := a.ids()
	pos := func(name string) int { return slices.Index(got, w.id(t, name)) }
	for _, name := range e.found {
		if pos(name) < 0 {
			t.Errorf("fact %q not served; got %s", name, w.names(got))
		}
	}
	for _, name := range e.notFound {
		if pos(name) >= 0 {
			t.Errorf("fact %q served; got %s", name, w.names(got))
		}
	}
	for _, p := range e.order {
		if i, j := pos(p[0]), pos(p[1]); i < 0 || j < 0 || i > j {
			t.Errorf("want %q before %q; got %s", p[0], p[1], w.names(got))
		}
	}
	if e.most > 0 && len(got) > e.most {
		t.Errorf("%d facts served, want at most %d: %s", len(got), e.most, w.names(got))
	}
	if e.none && len(got) > 0 {
		t.Errorf("want no fact; got %s", w.names(got))
	}
	if e.shape {
		checkShape(t, a)
	}
	if e.also != nil {
		e.also(t, w, a)
	}
}

// names turns served ids back into the setup's names, for a failure message a person can read.
func (w *world) names(ids []string) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		name := id
		for n, fid := range w.facts {
			if fid == id {
				name = n
			}
		}
		out = append(out, name)
	}
	return "[" + strings.Join(out, " ") + "]"
}

// jsonFields are the fields searchFact in cmd/memcue/facts.go prints; the always ones carry no omitempty.
var (
	jsonFields       = []string{"id", "content", "keywords", "origin", "scope", "created", "expires"}
	jsonFieldsAlways = []string{"id", "content", "created"}
)

// checkShape holds every fact of an answer to the -json contract a host parses: the fields it always has, no field
// it does not know, a ULID for the id and an RFC 3339 time in UTC for created.
func checkShape(t *testing.T, a answer) {
	t.Helper()
	if len(a.hits) == 0 {
		t.Errorf("shape: no fact to check")
	}
	for _, h := range a.hits {
		for _, f := range jsonFieldsAlways {
			if _, ok := h.Raw[f]; !ok {
				t.Errorf("shape: fact %s lacks %q", h.ID, f)
			}
		}
		for f := range h.Raw {
			if !slices.Contains(jsonFields, f) {
				t.Errorf("shape: fact %s has unknown field %q", h.ID, f)
			}
		}
		if len(h.ID) != 26 {
			t.Errorf("shape: id %q is not a ULID", h.ID)
		}
		if h.Content == "" {
			t.Errorf("shape: fact %s has empty content", h.ID)
		}
		if len(h.Created) != len("2006-01-02T15:04:05Z") || !strings.HasSuffix(h.Created, "Z") {
			t.Errorf("shape: fact %s created %q, want RFC 3339 in UTC", h.ID, h.Created)
		}
	}
}

// runCases runs a table with one subtest per case, so one red case does not hide the others. Every case has its own
// base, which is what lets them run in parallel.
func runCases(t *testing.T, cases []benchCase) {
	t.Helper()
	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.id] {
			t.Fatalf("case id %q is used twice", c.id)
		}
		seen[c.id] = true
		if c.stratum != rule && c.stratum != characterization {
			t.Fatalf("case %q: stratum %q", c.id, c.stratum)
		}
		if c.note == "" {
			t.Fatalf("case %q has no note", c.id)
		}
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			if c.setup != nil {
				c.setup(t, w)
			}
			c.expect.check(t, w, c.action(t, w))
		})
	}
}
