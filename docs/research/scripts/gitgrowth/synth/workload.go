//go:build research

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/juev/memcue/internal/store"
)

// gen makes synthetic fact text: words from a fixed pseudo-vocabulary (Latin and Cyrillic syllables) drawn with a
// Zipf distribution, at the average lengths of the inspected base (content 234, queries 128, keywords 57 characters).
// It is deterministic for a seed. Nothing in it comes from a real base.
type gen struct {
	r     *rand.Rand
	words []string
	zipf  *rand.Zipf
}

func newGen(seed uint64) *gen {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	latin := []string{"ka", "ro", "mi", "te", "lu", "san", "dor", "vel", "pri", "con", "ex", "sta", "ber", "nix",
		"tal", "go", "ser", "ver", "ma", "qui", "ld", "ent", "ion", "ar", "ux"}
	cyr := []string{"ка", "ро", "ми", "те", "лу", "сан", "дор", "вел", "при", "кон", "ста", "бер", "тал", "го",
		"сер", "вер", "ма", "ние", "ова", "ать", "ить", "ост"}
	seen := map[string]bool{}
	var words []string
	for len(words) < 8000 {
		syl := latin
		if r.IntN(10) < 3 {
			syl = cyr
		}
		var b strings.Builder
		for n := 1 + r.IntN(4); n > 0; n-- {
			b.WriteString(syl[r.IntN(len(syl))])
		}
		if w := b.String(); !seen[w] {
			seen[w] = true
			words = append(words, w)
		}
	}
	return &gen{r: r, words: words, zipf: rand.NewZipf(r, 1.1, 2, uint64(len(words)-1))}
}

func (g *gen) word() string { return g.words[g.zipf.Uint64()] }

func (g *gen) text(mean, sd, lo, hi float64) string {
	n := int(g.r.NormFloat64()*sd + mean)
	n = max(int(lo), min(int(hi), n))
	var b strings.Builder
	for b.Len() < n {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(g.word())
	}
	return b.String()
}

func (g *gen) list(items, words int) []string {
	out := make([]string, 0, items)
	for range items {
		parts := make([]string, 1+g.r.IntN(words))
		for i := range parts {
			parts[i] = g.word()
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

// edit changes about a fifth of the words of s: a correction rewrites part of a fact, not all of it.
func (g *gen) edit(s string) string {
	ws := strings.Fields(s)
	for i := range ws {
		if g.r.IntN(5) == 0 {
			ws[i] = g.word()
		}
	}
	return strings.Join(ws, " ")
}

var (
	kinds   = []string{"rule", "decision", "pitfall", "environment", "procedure", "event"}
	sources = []string{"agent", "agent", "agent", "user", "user_confirmed", "tool", "memory", "memory"}
)

func (g *gen) meta() store.Meta {
	m := store.Meta{Kind: kinds[g.r.IntN(len(kinds))], Source: sources[g.r.IntN(len(sources))]}
	switch n := g.r.IntN(10); {
	case n < 3:
		m.Scope = "global"
	case n < 9:
		m.Scope = fmt.Sprintf("repo:proj-%02d", g.r.IntN(30))
	default:
		m.Scope = fmt.Sprintf("host:host-%d", g.r.IntN(5))
	}
	if g.r.IntN(20) == 0 {
		m.Evidence = g.text(170, 40, 40, 400)
	}
	return m
}

func (g *gen) input() store.FactInput {
	in := store.FactInput{Content: g.text(234, 70, 60, 600), Keywords: g.list(5, 1),
		Origin: fmt.Sprintf("proj-%02d/%s %s", g.r.IntN(30), g.word(), time.Now().UTC().Format(time.DateOnly)),
		Meta:   g.meta()}
	if g.r.IntN(10) == 0 {
		in.Expires = time.Now().AddDate(0, 0, 7+g.r.IntN(90)).Format(time.DateOnly)
	}
	return in
}

// facts tracks what the workload may change: the current version of every fact and whether it is retired.
type facts struct {
	live, retired []string
	version       map[string]int
}

func (f *facts) set(fact store.Fact) {
	if _, ok := f.version[fact.ID]; !ok {
		f.live = append(f.live, fact.ID)
	}
	f.version[fact.ID] = fact.Version
}

func move(from, to *[]string, id string) {
	for i, x := range *from {
		if x == id {
			*from = append((*from)[:i], (*from)[i+1:]...)
			break
		}
	}
	*to = append(*to, id)
}

// The mix of writes between two syncs, calibrated on the inspected base: about 136 new versions per commit, of which
// adds give the growth in ids (2 400 at the first commit, about 4 700 at the last), retirements about a fifth of the
// ids, and the rest new versions of existing facts (queries and labels from the dream, corrections).
var mix = []struct {
	op     string
	weight int
}{{"add", 14}, {"queries", 45}, {"relabel", 20}, {"correct", 13}, {"retire", 7}, {"restore", 1}}

func (g *gen) op() string {
	n := g.r.IntN(100)
	for _, m := range mix {
		if n < m.weight {
			return m.op
		}
		n -= m.weight
	}
	return "add"
}

// apply runs one write on s through the store API, the same calls memcue add, correct, retire, restore and the dream
// make. It returns the operation it ran (a write that has no target falls back to an add).
func (g *gen) apply(s *store.Store, fs *facts) (string, error) {
	op := g.op()
	if (op == "restore" && len(fs.retired) == 0) || (op != "add" && op != "restore" && len(fs.live) == 0) {
		op = "add"
	}
	var (
		f   store.Fact
		err error
	)
	switch op {
	case "add":
		in := g.input()
		f, err = s.AddWith(in.Content, in.Keywords, in.Origin, in.Expires, in.Meta)
	case "queries":
		id := fs.live[g.r.IntN(len(fs.live))]
		f, err = s.SetQueries(id, fs.version[id], g.list(4, 4))
	case "relabel":
		id := fs.live[g.r.IntN(len(fs.live))]
		m := g.meta()
		f, err = s.Relabel(id, fs.version[id], m.Kind, m.Scope, "")
	case "correct":
		id := fs.live[g.r.IntN(len(fs.live))]
		var cur store.Fact
		if cur, err = s.Get(id); err == nil {
			f, err = s.Correct(id, g.edit(cur.Content), cur.Keywords)
		}
	case "retire":
		id := fs.live[g.r.IntN(len(fs.live))]
		if f, err = s.Retire(id); err == nil {
			move(&fs.live, &fs.retired, id)
		}
	case "restore":
		id := fs.retired[g.r.IntN(len(fs.retired))]
		if f, err = s.Restore(id); err == nil {
			move(&fs.retired, &fs.live, id)
		}
	}
	if err != nil {
		return op, fmt.Errorf("%s: %w", op, err)
	}
	fs.set(f)
	return op, nil
}

// repo is a scratch git repository that one encoding is committed to.
type repo struct {
	name    string
	dir     string
	commits int
	export  time.Duration // time to produce the encoding at each sync, not counting git
	commit  time.Duration // time of git add and git commit
}

func newRepo(root, name string) (*repo, error) {
	r := &repo{name: name, dir: filepath.Join(root, name)}
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return nil, err
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "gc.auto", "0"},
		{"config", "core.fsync", "none"}, {"config", "core.autocrlf", "false"}} {
		if err := git(r.dir, args...); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=bench", "GIT_AUTHOR_EMAIL=bench@example.invalid",
		"GIT_COMMITTER_NAME=bench", "GIT_COMMITTER_EMAIL=bench@example.invalid", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

func (r *repo) commitAll(msg string) error {
	t0 := time.Now()
	defer func() { r.commit += time.Since(t0) }()
	if err := git(r.dir, "add", "--all", "."); err != nil {
		return err
	}
	if err := git(r.dir, "commit", "-q", "--allow-empty", "--no-verify", "-m", msg); err != nil {
		return err
	}
	r.commits++
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

// writePerFact writes facts/<xx>/<id>.jsonl, the versions of one fact per file, and removes nothing: facts are never
// deleted. Only files whose bytes changed are rewritten.
func writePerFact(root string, rows []Row) error {
	byID := map[string][]Row{}
	for _, r := range rows {
		byID[r.ID] = append(byID[r.ID], r)
	}
	for id, rs := range byID {
		p := filepath.Join(root, "facts", id[len(id)-2:], id+".jsonl")
		b := encodeJSONL(rs)
		if old, err := os.ReadFile(p); err == nil && string(old) == string(b) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

type workloadResult struct {
	Seed           uint64                    `json:"seed"`
	Initial        int                       `json:"initial_facts"`
	Commits        int                       `json:"commits"`
	WritesPerSync  int                       `json:"writes_per_commit"`
	Ops            map[string]int            `json:"ops"`
	Rows           int                       `json:"rows"`
	IDs            int                       `json:"ids"`
	RetiredIDs     int                       `json:"retired_ids"`
	FileBytes      int64                     `json:"sqlite_file_bytes"`
	VacuumBytes    int64                     `json:"sqlite_vacuum_bytes"`
	TextBytes      int                       `json:"text_single_bytes"`
	WriteSeconds   float64                   `json:"write_seconds"`
	Repos          map[string]map[string]any `json:"repos"`
	FactsUnusedPct float64                   `json:"facts_table_unused_pct"`
}

// runWorkload writes the synthetic history: the initial facts in one commit, then commits sync points with writes
// between them, and after each sync point commits the base in every encoding to its own repository.
func runWorkload(args []string) error {
	fl := flag.NewFlagSet("workload", flag.ContinueOnError)
	out := fl.String("out", "", "new directory for the repositories")
	seed := fl.Uint64("seed", 530, "random seed")
	initial := fl.Int("initial", 2400, "facts at the first commit")
	commits := fl.Int("commits", 122, "commits after the first")
	writes := fl.Int("writes", 136, "writes between two commits")
	result := fl.String("result", "", "JSON file for the workload summary")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *out == "" || *result == "" {
		return fmt.Errorf("workload: -out and -result are required")
	}
	if err := newDir(*out); err != nil {
		return err
	}
	g := newGen(*seed)
	repos := map[string]*repo{}
	for _, n := range []string{"sqlite-every", "sqlite-batch5", "sqlite-batch20", "sqlite-vacuum", "text-single",
		"text-perfact", "text-events"} {
		r, err := newRepo(*out, n)
		if err != nil {
			return err
		}
		repos[n] = r
	}
	base := filepath.Join(repos["sqlite-every"].dir, "memory", "facts.db")
	s, err := store.Open(base)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	fs := &facts{version: map[string]int{}}
	ops := map[string]int{}
	t0 := time.Now()
	inputs := make([]store.FactInput, *initial)
	for i := range inputs {
		inputs[i] = g.input()
	}
	added, err := s.AddBatch(inputs)
	if err != nil {
		return err
	}
	for _, f := range added {
		fs.set(f)
	}
	ops["initial"] = len(added)
	var writeTime time.Duration
	writeTime += time.Since(t0)
	events := EventMap{}
	tmp := filepath.Join(*out, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	for c := 0; c <= *commits; c++ {
		if c > 0 {
			t := time.Now()
			for range *writes {
				op, err := g.apply(s, fs)
				if err != nil {
					return err
				}
				ops[op]++
			}
			writeTime += time.Since(t)
		}
		if err := syncPoint(repos, base, tmp, c, *commits, events); err != nil {
			return err
		}
		if c%10 == 0 {
			fmt.Fprintf(os.Stderr, "commit %d/%d\n", c, *commits)
		}
	}
	rows, err := exportRows(base)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	st, _ := os.Stat(base)
	vac := filepath.Join(tmp, "vacuum.db")
	vst, _ := os.Stat(vac)
	res := workloadResult{Seed: *seed, Initial: *initial, Commits: *commits, WritesPerSync: *writes, Ops: ops,
		Rows: len(rows), IDs: len(ids), RetiredIDs: len(fs.retired), FileBytes: st.Size(), VacuumBytes: vst.Size(),
		TextBytes: len(encodeJSONL(rows)), WriteSeconds: writeTime.Seconds(), Repos: map[string]map[string]any{}}
	if db, err := openRaw(base, true); err == nil {
		var used, unused float64
		if db.QueryRow(`SELECT sum(pgsize), sum(unused) FROM dbstat WHERE name = 'facts'`).Scan(&used, &unused) == nil &&
			used > 0 {
			res.FactsUnusedPct = 100 * unused / used
		}
		_ = db.Close()
	}
	names := make([]string, 0, len(repos))
	for n := range repos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r := repos[n]
		res.Repos[n] = map[string]any{"commits": r.commits, "export_seconds": r.export.Seconds(),
			"commit_seconds": r.commit.Seconds()}
	}
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(res, "", " ")
	return writeResult(*result, append(b, '\n'))
}

// syncPoint commits the base after commit c in every encoding. The base is idle here (no transaction is open), so the
// file on disk is whole and git may read it, which is what the owner's setup does at the end of a session.
func syncPoint(repos map[string]*repo, base, tmp string, c, last int, events EventMap) error {
	msg := fmt.Sprintf("sync %d", c)
	if err := git(repos["sqlite-every"].dir, "add", "memory/facts.db"); err != nil {
		return err
	}
	if err := repos["sqlite-every"].commitAll(msg); err != nil {
		return err
	}
	for _, b := range []struct {
		name  string
		every int
	}{{"sqlite-batch5", 5}, {"sqlite-batch20", 20}} {
		if c%b.every != 0 && c != last {
			continue
		}
		r := repos[b.name]
		t := time.Now()
		if err := copyFile(base, filepath.Join(r.dir, "memory", "facts.db")); err != nil {
			return err
		}
		r.export += time.Since(t)
		if err := r.commitAll(msg); err != nil {
			return err
		}
	}
	r := repos["sqlite-vacuum"]
	t := time.Now()
	vac := filepath.Join(tmp, "vacuum.db")
	_ = os.Remove(vac)
	db, err := openRaw(base, true)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`VACUUM INTO ?`, vac); err != nil {
		_ = db.Close()
		return err
	}
	_ = db.Close()
	if err := copyFile(vac, filepath.Join(r.dir, "memory", "facts.db")); err != nil {
		return err
	}
	r.export += time.Since(t)
	if err := r.commitAll(msg); err != nil {
		return err
	}

	t = time.Now()
	rows, err := exportRows(base)
	if err != nil {
		return err
	}
	export := time.Since(t)
	r = repos["text-single"]
	t = time.Now()
	if err := os.WriteFile(filepath.Join(r.dir, "facts.jsonl"), encodeJSONL(rows), 0o600); err != nil {
		return err
	}
	r.export += export + time.Since(t)
	if err := r.commitAll(msg); err != nil {
		return err
	}
	r = repos["text-perfact"]
	t = time.Now()
	if err := writePerFact(r.dir, rows); err != nil {
		return err
	}
	r.export += export + time.Since(t)
	if err := r.commitAll(msg); err != nil {
		return err
	}
	r = repos["text-events"]
	t = time.Now()
	if _, err := record(r.dir, rows, events); err != nil {
		return err
	}
	r.export += export + time.Since(t)
	return r.commitAll(msg)
}
