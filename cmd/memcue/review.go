package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/juev/memcue/internal/store"
)

// reviewServer is the local page where the owner approves or rejects candidates. It is deliberately small: one
// process, one SQLite connection, no external assets, and the browser talks only to 127.0.0.1.
type reviewServer struct {
	store   *store.Store
	logPath string
	token   string // CSRF token of the forms; a page on another origin cannot read it
	now     func() time.Time

	mu   sync.Mutex
	last *reviewAction
}

// reviewAction remembers one approval or rejection so it can be undone as a whole.
type reviewAction struct {
	kind    string // approve or reject
	entries []reviewEntry
}

type reviewEntry struct {
	id      string
	version int // the version the action wrote; an undo must not overwrite a later edit
}

// reviewDecision is one line of review.log: what the owner did with a fact, and when.
type reviewDecision struct {
	Time     string `json:"time"`
	Fact     string `json:"fact"`
	Decision string `json:"decision"` // approve, reject or undo
	Reason   string `json:"reason,omitempty"`
	Version  int    `json:"version"`
}

// newReviewServer makes a page over s. logPath is usually $XDG_STATE_HOME/memcue/review.log.
func newReviewServer(s *store.Store, logPath string) (*reviewServer, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("review: token: %w", err)
	}
	return &reviewServer{store: s, logPath: logPath, token: hex.EncodeToString(b), now: time.Now}, nil
}

// reviewData is what the template renders.
type reviewData struct {
	Token      string
	Kind       string
	Source     string
	Kinds      []string
	Sources    []string
	Candidates []store.Fact
	Message    string
	Error      string
	LastKind   string
	LastCount  int
}

func (rs *reviewServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The page is only for a browser that reached this machine by a loopback address. Refusing another Host also
	// closes the DNS-rebinding route to the CSRF token.
	if !loopbackHost(r.Host) {
		http.Error(w, "memcue review is only available on this machine", http.StatusForbidden)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		rs.index(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/approve":
		rs.decide(w, r, "approve")
	case r.Method == http.MethodPost && r.URL.Path == "/reject":
		rs.decide(w, r, "reject")
	case r.Method == http.MethodPost && r.URL.Path == "/undo":
		rs.undo(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (rs *reviewServer) index(w http.ResponseWriter, r *http.Request) {
	all, err := rs.store.Candidates()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	kind, source := r.URL.Query().Get("kind"), r.URL.Query().Get("source")
	data := reviewData{Token: rs.token, Kind: kind, Source: source, Message: r.URL.Query().Get("msg"),
		Error: r.URL.Query().Get("err")}
	seenKind, seenSource := map[string]bool{}, map[string]bool{}
	for _, f := range all {
		if f.Kind != "" && !seenKind[f.Kind] {
			seenKind[f.Kind] = true
			data.Kinds = append(data.Kinds, f.Kind)
		}
		if f.Source != "" && !seenSource[f.Source] {
			seenSource[f.Source] = true
			data.Sources = append(data.Sources, f.Source)
		}
	}
	sort.Strings(data.Kinds)
	sort.Strings(data.Sources)
	for _, f := range all {
		if kind != "" && f.Kind != kind {
			continue
		}
		if source != "" && f.Source != source {
			continue
		}
		data.Candidates = append(data.Candidates, f)
	}
	// Candidates come newest first from the store; the explicit sort also keeps pages stable for equal timestamps.
	sort.SliceStable(data.Candidates, func(i, j int) bool {
		if !data.Candidates[i].Created.Equal(data.Candidates[j].Created) {
			return data.Candidates[i].Created.After(data.Candidates[j].Created)
		}
		return data.Candidates[i].ID > data.Candidates[j].ID
	})
	rs.mu.Lock()
	if rs.last != nil {
		data.LastKind, data.LastCount = rs.last.kind, len(rs.last.entries)
	}
	rs.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	if err := reviewTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (rs *reviewServer) decide(w http.ResponseWriter, r *http.Request, kind string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !rs.validToken(r.FormValue("token")) {
		http.Error(w, "bad review token", http.StatusForbidden)
		return
	}
	ids := selected(r)
	if len(ids) == 0 {
		rs.redirect(w, r, "no facts selected", "")
		return
	}
	action := reviewAction{kind: kind}
	var done []string
	var failures []string
	for _, id := range ids {
		var (
			f   store.Fact
			err error
		)
		if kind == "approve" {
			f, err = rs.store.Approve(id)
		} else {
			// RetireCandidate re-checks the fact under the write, so an approval made after this page was rendered
			// is not undone by a stale rejection; ErrNotCandidate lands in failures as before.
			f, err = rs.store.RetireCandidate(id)
		}
		if err != nil {
			failures = append(failures, id+": "+err.Error())
			continue
		}
		reason := ""
		if kind == "reject" {
			reason = "rejected in review"
		}
		if err := rs.log(reviewDecision{Time: rs.now().UTC().Format(time.RFC3339), Fact: id, Decision: kind,
			Reason: reason, Version: f.Version}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		action.entries = append(action.entries, reviewEntry{id: id, version: f.Version})
		done = append(done, id)
	}
	if len(action.entries) > 0 {
		rs.mu.Lock()
		rs.last = &action
		rs.mu.Unlock()
	}
	msg := fmt.Sprintf("%s: %d", kind, len(done))
	if len(failures) > 0 {
		msg += "; failed: " + strings.Join(failures, "; ")
	}
	rs.redirect(w, r, msg, "")
}

func (rs *reviewServer) undo(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !rs.validToken(r.FormValue("token")) {
		http.Error(w, "bad review token", http.StatusForbidden)
		return
	}
	rs.mu.Lock()
	last := rs.last
	rs.mu.Unlock()
	if last == nil {
		rs.redirect(w, r, "", "nothing to undo")
		return
	}
	var done int
	var failures []string
	for _, e := range last.entries {
		cur, err := rs.store.Get(e.id)
		if err == nil && cur.Version != e.version {
			err = errors.New("changed since the action")
		}
		if err == nil {
			_, err = rs.store.Reopen(e.id)
		}
		if err != nil {
			failures = append(failures, e.id+": "+err.Error())
			continue
		}
		if err := rs.log(reviewDecision{Time: rs.now().UTC().Format(time.RFC3339), Fact: e.id, Decision: "undo",
			Version: e.version + 1}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		done++
	}
	rs.mu.Lock()
	if len(failures) == 0 {
		rs.last = nil
	}
	rs.mu.Unlock()
	msg := fmt.Sprintf("undo %s: %d", last.kind, done)
	if len(failures) > 0 {
		msg += "; failed: " + strings.Join(failures, "; ")
	}
	rs.redirect(w, r, msg, "")
}

func (rs *reviewServer) validToken(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(rs.token)) == 1
}

func (rs *reviewServer) redirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	q := url.Values{}
	if msg != "" {
		q.Set("msg", msg)
	}
	if errMsg != "" {
		q.Set("err", errMsg)
	}
	for _, k := range []string{"kind", "source"} {
		if v := r.FormValue(k); v != "" {
			q.Set(k, v)
		}
	}
	target := "/"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (rs *reviewServer) log(rec reviewDecision) error {
	return appendJSON(rs.logPath, rec)
}

// selected returns the fact ids of the checkboxes, without empty values.
func selected(r *http.Request) []string {
	var out []string
	for _, id := range r.Form["id"] {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// runReview serves the review page: memcue review [-addr 127.0.0.1:0] [-no-open].
func runReview(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "loopback address to listen on")
	noOpen := fs.Bool("no-open", false, "print the URL instead of opening a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: memcue review [-addr 127.0.0.1:0] [-no-open]")
	}
	if err := loopbackAddr(*addr); err != nil {
		return err
	}
	s, err := store.Open(factsPath())
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	dir, err := stateDir()
	if err != nil {
		return err
	}
	rs, err := newReviewServer(s, filepath.Join(dir, "review.log"))
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String() + "/"
	if _, err := fmt.Fprintf(out, "memcue review: %s\n", url); err != nil {
		_ = ln.Close()
		return err
	}
	if !*noOpen {
		if err := openBrowser(url); err != nil {
			_, _ = fmt.Fprintf(out, "open it in a browser: %v\n", err)
		}
	}
	srv := &http.Server{Handler: rs, ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}

// loopbackHost reports whether an HTTP Host header names this machine's loopback interface.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loopbackAddr refuses anything that would expose the page beyond this machine.
func loopbackAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("review: -addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("review: -addr %q is not a loopback address", addr)
	}
	return nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("no browser opener for %s", runtime.GOOS)
	}
	return cmd.Start()
}

// reviewTemplate is one page: candidates, filters, and the three actions. It has no scripts and no external assets,
// so it works offline and inside a minimal container.
var reviewTemplate = template.Must(template.New("review").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>memcue review</title>
<style>
:root { color-scheme: light dark; }
body { font: 16px/1.45 system-ui, sans-serif; margin: 0 auto; max-width: 980px; padding: 1.5rem; }
h1 { font-size: 1.4rem; margin: 0 0 .25rem; }
p.lead { margin: 0 0 1rem; opacity: .75; }
form.filters, form.actions { display: flex; gap: .6rem; flex-wrap: wrap; align-items: center; margin: 1rem 0; }
select, button { font: inherit; padding: .35rem .6rem; }
button { cursor: pointer; }
button.reject { color: #b00020; }
table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; vertical-align: top; padding: .6rem .5rem; border-top: 1px solid #8884; }
th { font-size: .85rem; opacity: .75; }
td.pick { width: 2rem; }
.fact { font-weight: 600; }
.meta { font-size: .85rem; opacity: .8; }
blockquote { margin: .35rem 0 0; padding-left: .6rem; border-left: 2px solid #8886; white-space: pre-wrap; }
.msg { padding: .5rem .7rem; background: #2a82; border-radius: .35rem; }
.err { padding: .5rem .7rem; background: #b0002022; border-radius: .35rem; }
.empty { padding: 2rem 0; opacity: .7; }
</style>
</head>
<body>
<h1>memcue review</h1>
<p class="lead">Extracted and mirrored facts wait for a decision. Approved facts enter recall; rejected ones stay in the history.</p>
{{if .Message}}<p class="msg">{{.Message}}</p>{{end}}
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form class="filters" method="get" action="/">
<label>kind <select name="kind"><option value="">all</option>{{range .Kinds}}<option value="{{.}}"{{if eq . $.Kind}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<label>source <select name="source"><option value="">all</option>{{range .Sources}}<option value="{{.}}"{{if eq . $.Source}} selected{{end}}>{{.}}</option>{{end}}</select></label>
<button type="submit">show</button>
<a href="/">reset</a>
</form>
{{if not .Candidates}}<p class="empty">No candidates.</p>{{else}}
<form method="post" action="/approve">
<input type="hidden" name="token" value="{{.Token}}">
<input type="hidden" name="kind" value="{{.Kind}}">
<input type="hidden" name="source" value="{{.Source}}">
<table>
<thead><tr><th class="pick"></th><th>Fact</th><th>Kind and scope</th><th>Source</th></tr></thead>
<tbody>
{{range .Candidates}}<tr>
<td class="pick"><input type="checkbox" name="id" value="{{.ID}}" aria-label="select"></td>
<td><div class="fact">{{.Content}}</div>
{{if .Evidence}}<blockquote>{{.Evidence}}</blockquote>{{end}}
<div class="meta">{{.Origin}}{{if .Created}} · {{.Created.Format "2006-01-02 15:04"}}{{end}}</div></td>
<td>{{if .Kind}}{{.Kind}}{{else}}—{{end}}{{if .Scope}}<br>{{.Scope}}{{end}}</td>
<td>{{if .Source}}{{.Source}}{{else}}—{{end}}</td>
</tr>{{end}}
</tbody>
</table>
<div class="actions">
<button type="submit">approve selected</button>
<button type="submit" class="reject" formaction="/reject">reject selected</button>
</div>
</form>
{{end}}
{{if .LastCount}}<form class="actions" method="post" action="/undo">
<input type="hidden" name="token" value="{{.Token}}">
<input type="hidden" name="kind" value="{{.Kind}}">
<input type="hidden" name="source" value="{{.Source}}">
<button type="submit">undo last action ({{.LastKind}}, {{.LastCount}})</button>
</form>{{end}}
</body>
</html>
`))
