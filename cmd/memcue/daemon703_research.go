//go:build research

// The daemon prototype of #703: a long-lived process keeps a local SQLite replica of the fact base current and
// answers the hot read paths over a Unix socket. Built only with -tags research; not part of memcue.
//
//	memcue daemon703 serve -sock S -replica :memory:|FILE -source pg|text [-text DIR] [-poll 2s] [-notify]
//	memcue daemon703 call -sock S -op search|pitfalls|precommand|add|stats [-cwd DIR] [-- words]
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tailnet"
	"github.com/juev/memcue/internal/tier"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "daemon703" {
		os.Exit(runDaemon703(os.Args[2:]))
	}
}

func runDaemon703(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "daemon703: serve or call")
		return 2
	}
	var err error
	switch args[0] {
	case "serve":
		err = serve703(args[1:])
	case "call":
		var code int
		code, err = call703(args[1:])
		if err == nil {
			return code
		}
	default:
		err = fmt.Errorf("unknown daemon703 command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "daemon703:", err)
		return 3
	}
	return 0
}

type request703 struct {
	Op    string   `json:"op"`
	Cwd   string   `json:"cwd"`
	Words []string `json:"words"`
	Kind  string   `json:"kind,omitempty"`
	Scope string   `json:"scope,omitempty"`
}

type reply703 struct {
	Out string `json:"out"`
	Err string `json:"err,omitempty"`
}

type daemon703 struct {
	mu       sync.Mutex
	replica  *store.Store
	versions map[string]int
	source   string
	dsn      string
	textDir  string
	sync     *pgx.Conn
	writer   *store.Store
	inproc   func(ctx context.Context, network, address string) (net.Conn, error) // variant 4: own tsnet node
	sum      [2]int64                                                             // count and sum(version) at the last poll
	offsetMs float64                                                              // server clock minus local clock
	log      *json.Encoder
	logMu    sync.Mutex
}

func (d *daemon703) event(fields map[string]any) {
	fields["t_ms"] = float64(time.Now().UnixMicro()) / 1000
	d.logMu.Lock()
	_ = d.log.Encode(fields)
	d.logMu.Unlock()
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }

func memory703() map[string]any {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	maxrss := float64(ru.Maxrss) / (1 << 20) // bytes on macOS
	if runtime.GOOS == "linux" {
		maxrss = float64(ru.Maxrss) / 1024 // kilobytes on Linux
	}
	return map[string]any{"heap_inuse_mb": float64(m.HeapInuse) / (1 << 20), "go_sys_mb": float64(m.Sys) / (1 << 20),
		"max_rss_mb": maxrss}
}

func serve703(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	sock := fs.String("sock", "", "socket path")
	replica := fs.String("replica", ":memory:", ":memory: (variant 1) or a SQLite file kept across restarts (variants 2 and 3)")
	source := fs.String("source", "pg", "pg (MEMCUE_POSTGRES_DSN) or text")
	textDir := fs.String("text", "", "text history directory for -source text")
	poll := fs.Duration("poll", 0, "poll the change aggregate this often (0: off)")
	notify := fs.Bool("notify", false, "LISTEN memcue_facts for changes (needs the research trigger)")
	inproc := fs.Bool("inproc", false, "variant 4: run the tailnet node in this process (stop the helper first); no writer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	start := time.Now()
	d := &daemon703{source: *source, dsn: os.Getenv("MEMCUE_POSTGRES_DSN"), textDir: *textDir, log: json.NewEncoder(os.Stderr)}
	ctx := context.Background()
	var inprocMs float64
	if *inproc {
		cfg, err := store.PostgresConfig(d.dsn)
		if err != nil {
			return err
		}
		c, err := tailnet.Settings(cfg.Host, strconv.Itoa(int(cfg.Port)))
		if err != nil {
			return err
		}
		t := time.Now()
		up, cancel := context.WithTimeout(ctx, 60*time.Second)
		dial, closeNode, err := tailnet.ResearchDialer(up, c)
		cancel()
		if err != nil {
			return err
		}
		defer closeNode()
		d.inproc, inprocMs = dial, msSince(t)
	}
	warm := *replica != ":memory:" && exists703(*replica)
	t := time.Now()
	r, err := store.Open(*replica)
	if err != nil {
		return err
	}
	d.replica = r
	openMs := msSince(t)
	if d.versions, err = store.ResearchVersions(r); err != nil {
		return err
	}
	stats := map[string]any{"event": "ready", "replica": *replica, "source": *source, "warm": warm, "open_ms": openMs,
		"inproc": *inproc, "node_up_ms": inprocMs}
	switch *source {
	case "pg":
		t = time.Now()
		if d.sync, err = d.connect(ctx); err != nil {
			return err
		}
		stats["connect_ms"] = msSince(t)
		if err := d.clockOffset(ctx); err != nil {
			return err
		}
		t = time.Now()
		var n int
		if len(d.versions) == 0 {
			n, err = d.fullLoad(ctx)
			stats["load"] = "full"
		} else {
			n, err = d.delta(ctx)
			stats["load"] = "delta"
		}
		if err != nil {
			return err
		}
		stats["load_ms"], stats["loaded"] = msSince(t), n
		if err := d.aggregate(ctx); err != nil {
			return err
		}
		if !*inproc { // the store's writer dials through the helper, which variant 4 replaces
			t = time.Now()
			if d.writer, err = store.OpenPostgres(d.dsn); err != nil {
				return err
			}
			stats["writer_open_ms"] = msSince(t)
		}
	case "text":
		t = time.Now()
		cache := filepath.Join(filepath.Dir(*sock), "text-cache.db")
		src, err := store.OpenText(*textDir, cache)
		if err != nil {
			return err
		}
		stats["text_open_ms"] = msSince(t)
		t = time.Now()
		facts, err := src.Current()
		_ = src.Close()
		if err != nil {
			return err
		}
		if err := store.ResearchPut(r, facts); err != nil {
			return err
		}
		stats["load_ms"], stats["loaded"] = msSince(t), len(facts)
	default:
		return fmt.Errorf("unknown source %q", *source)
	}
	runtime.GC()
	for k, v := range memory703() {
		stats[k] = v
	}
	stats["cold_ms"] = msSince(start)
	_ = os.Remove(*sock)
	ln, err := net.Listen("unix", *sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(*sock, 0o600); err != nil {
		return err
	}
	stats["listen_ms"] = msSince(start)
	d.event(stats)
	if *poll > 0 {
		go d.pollLoop(ctx, *poll)
	}
	if *notify {
		go d.notifyLoop(ctx)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go d.handle(c)
	}
}

func exists703(path string) bool { _, err := os.Stat(path); return err == nil }

func (d *daemon703) connect(ctx context.Context) (*pgx.Conn, error) {
	cfg, err := store.PostgresConfig(d.dsn)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if d.inproc != nil {
		host := cfg.Host
		cfg.LookupFunc = func(context.Context, string) ([]string, error) { return []string{host}, nil }
		cfg.DialFunc = d.inproc
	} else if err := tailnet.Configure(cctx, cfg); err != nil {
		return nil, err
	}
	// statement_timeout of the DSN would end a LISTEN wait; the sync connection waits as long as it must.
	cfg.RuntimeParams["statement_timeout"] = "0"
	return pgx.ConnectConfig(cctx, cfg)
}

// clockOffset estimates the server clock against this machine's, so staleness can compare a fact's Created time,
// written by another machine, with the local time it was applied. Both writers here use NTP-synced clocks.
func (d *daemon703) clockOffset(ctx context.Context) error {
	best := 1e18
	for range 5 {
		before := time.Now()
		var server float64
		if err := d.sync.QueryRow(ctx, "SELECT extract(epoch from clock_timestamp()) * 1000").Scan(&server); err != nil {
			return err
		}
		after := time.Now()
		rtt := float64(after.Sub(before).Microseconds()) / 1000
		if rtt < best {
			best = rtt
			mid := float64(before.UnixMicro())/1000 + rtt/2
			d.offsetMs = server - mid
		}
	}
	d.event(map[string]any{"event": "clock", "offset_ms": d.offsetMs, "rtt_ms": best})
	return nil
}

func (d *daemon703) fullLoad(ctx context.Context) (int, error) {
	rows, err := d.sync.Query(ctx, "SELECT payload FROM memcue.facts ORDER BY id")
	if err != nil {
		return 0, err
	}
	facts, err := decodeRows703(rows)
	if err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := store.ResearchPut(d.replica, facts); err != nil {
		return 0, err
	}
	for _, f := range facts {
		d.versions[f.ID] = f.Version
	}
	return len(facts), nil
}

func decodeRows703(rows pgx.Rows) ([]store.Fact, error) {
	defer rows.Close()
	var facts []store.Fact
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		f, err := store.ResearchDecode(payload)
		if err != nil {
			return nil, err
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

// delta compares every (id, version) of the source with the replica and fetches the facts that differ: schema 2
// has no change sequence, so this is the cheapest exact catch-up it allows.
func (d *daemon703) delta(ctx context.Context) (int, error) {
	rows, err := d.sync.Query(ctx, "SELECT id, version FROM memcue.facts")
	if err != nil {
		return 0, err
	}
	var changed []string
	for rows.Next() {
		var id string
		var v int
		if err := rows.Scan(&id, &v); err != nil {
			rows.Close()
			return 0, err
		}
		if d.versions[id] != v {
			changed = append(changed, id)
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return 0, rows.Err()
	}
	if len(changed) == 0 {
		return 0, nil
	}
	return d.fetch(ctx, changed)
}

func (d *daemon703) fetch(ctx context.Context, ids []string) (int, error) {
	rows, err := d.sync.Query(ctx, "SELECT payload FROM memcue.facts WHERE id = ANY($1::text[])", ids)
	if err != nil {
		return 0, err
	}
	facts, err := decodeRows703(rows)
	if err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := store.ResearchPut(d.replica, facts); err != nil {
		return 0, err
	}
	now := float64(time.Now().UnixMicro()) / 1000
	for _, f := range facts {
		d.versions[f.ID] = f.Version
		created := float64(f.Created.UnixMicro()) / 1000
		d.event(map[string]any{"event": "applied", "id": f.ID, "version": f.Version,
			"staleness_ms": now - (created - d.offsetMs)})
	}
	return len(facts), nil
}

func (d *daemon703) aggregate(ctx context.Context) error {
	var n, sum int64
	if err := d.sync.QueryRow(ctx, "SELECT count(*), coalesce(sum(version), 0) FROM memcue.facts").Scan(&n, &sum); err != nil {
		return err
	}
	d.sum = [2]int64{n, sum}
	return nil
}

func (d *daemon703) pollLoop(ctx context.Context, every time.Duration) {
	for range time.Tick(every) {
		before := d.sum
		if err := d.aggregate(ctx); err != nil {
			d.event(map[string]any{"event": "poll_error", "error": err.Error()})
			continue
		}
		if d.sum != before {
			t := time.Now()
			n, err := d.delta(ctx)
			ev := map[string]any{"event": "delta", "facts": n, "ms": msSince(t)}
			if err != nil {
				ev["error"] = err.Error()
			}
			d.event(ev)
		}
	}
}

func (d *daemon703) notifyLoop(ctx context.Context) {
	c, err := d.connect(ctx)
	if err != nil {
		d.event(map[string]any{"event": "notify_error", "error": err.Error()})
		return
	}
	if _, err := c.Exec(ctx, "LISTEN memcue_facts"); err != nil {
		d.event(map[string]any{"event": "notify_error", "error": err.Error()})
		return
	}
	for {
		n, err := c.WaitForNotification(ctx)
		if err != nil {
			d.event(map[string]any{"event": "notify_error", "error": err.Error()})
			return
		}
		id, version, _ := strings.Cut(n.Payload, ":")
		if v, _ := strconv.Atoi(version); d.versions[id] == v {
			continue // our own write, already applied
		}
		if _, err := d.fetch(ctx, []string{id}); err != nil {
			d.event(map[string]any{"event": "notify_error", "error": err.Error()})
		}
	}
}

func (d *daemon703) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	var req request703
	if err := json.NewDecoder(io.LimitReader(c, 1<<20)).Decode(&req); err != nil {
		return
	}
	out, err := d.answer(req)
	rep := reply703{Out: out}
	if err != nil {
		rep.Err = err.Error()
	}
	_ = json.NewEncoder(c).Encode(rep)
}

func (d *daemon703) answer(req request703) (string, error) {
	ctx := context.Background()
	switch req.Op {
	case "search":
		// search -json -in all WORDS, as the hooks and the benchmark run it.
		q := recall.Query{Text: strings.Join(req.Words, " "), N: 5, MinScore: recall.MinScore,
			Project: tier.Keys(req.Cwd, claudeDir(), ctx), Host: currentHost(), In: []string{"all"}}
		d.mu.Lock()
		hits, err := recall.Search(d.replica, q)
		d.mu.Unlock()
		if err != nil {
			return "", err
		}
		return encode703(searchAnswer{Facts: searchJSON(hits)})
	case "pitfalls":
		// The store read of session-start: the active pitfalls of the cwd's repository.
		keys := tier.RepositoryKeys(req.Cwd, claudeDir(), ctx)
		scopes := make([]string, len(keys))
		for i, k := range keys {
			scopes[i] = tier.Scope(k)
		}
		d.mu.Lock()
		facts, err := d.replica.ActiveIn("pitfall", scopes)
		d.mu.Unlock()
		if err != nil {
			return "", err
		}
		lines := make([]string, len(facts))
		for i, f := range facts {
			lines[i] = f.Content
		}
		return encode703(map[string]any{"additionalContext": strings.Join(lines, "\n\n"), "repositoryPitfallCount": len(facts)})
	case "precommand":
		// The store read of pre-command, without its receipt state file.
		q := recall.Query{OriginOnlyUnscoped: true, Text: strings.Join(req.Words, " "), N: 256, MinScore: recall.MinScore,
			Project: tier.Keys(req.Cwd, claudeDir(), ctx), Host: currentHost(), In: []string{"project", "host", "global"},
			Kinds: []string{"rule", "decision", "pitfall", "environment", "procedure"}, Recurrence: map[string]int{}}
		d.mu.Lock()
		hits, err := recall.Search(d.replica, q)
		d.mu.Unlock()
		if err != nil {
			return "", err
		}
		return encode703(map[string]any{"facts": searchJSON(hits)})
	case "add":
		if d.writer == nil {
			return "", errors.New("add needs -source pg")
		}
		origin := ""
		if keys := tier.Keys(req.Cwd, claudeDir(), ctx); len(keys) > 0 {
			origin = keys[0] + " " + time.Now().Format(time.DateOnly)
		}
		facts, err := d.writer.AddBatch([]store.FactInput{{Content: strings.Join(req.Words, " "), Origin: origin,
			Meta: store.Meta{Kind: req.Kind, Scope: req.Scope, Source: "user"}}})
		if err != nil {
			return "", err
		}
		d.mu.Lock()
		err = store.ResearchPut(d.replica, facts)
		for _, f := range facts {
			d.versions[f.ID] = f.Version
		}
		d.mu.Unlock()
		if err != nil {
			return "", err
		}
		return facts[0].ID + "\n", nil
	case "stats":
		runtime.GC()
		m := memory703()
		m["facts"] = len(d.versions)
		return encode703(m)
	}
	return "", fmt.Errorf("unknown op %q", req.Op)
}

// encode703 is one JSON line, as the CLI's encoder writes it.
func encode703(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b) + "\n", err
}

func call703(args []string) (int, error) {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	sock := fs.String("sock", "", "socket path")
	op := fs.String("op", "search", "search, pitfalls, precommand, add or stats")
	cwd := fs.String("cwd", ".", "working directory")
	kind := fs.String("kind", "", "kind for add")
	scope := fs.String("scope", "", "scope for add")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	abs, err := filepath.Abs(*cwd)
	if err != nil {
		return 0, err
	}
	c, err := net.DialTimeout("unix", *sock, 2*time.Second)
	if err != nil {
		return 0, err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(4 * time.Second))
	if err := json.NewEncoder(c).Encode(request703{Op: *op, Cwd: abs, Words: fs.Args(), Kind: *kind, Scope: *scope}); err != nil {
		return 0, err
	}
	var rep reply703
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&rep); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(os.Stdout, rep.Out); err != nil {
		return 0, err
	}
	if rep.Err != "" {
		fmt.Fprintln(os.Stderr, "daemon703:", rep.Err)
		return 1, nil
	}
	return 0, nil
}
