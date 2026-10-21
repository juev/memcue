package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

func reviewCandidate(t *testing.T, s *store.Store, content, kind, source string) store.Fact {
	t.Helper()
	f, err := s.AddWith(content, nil, "review-test 2026-10-20", "", store.Meta{Kind: kind, Source: source,
		Scope: "host:test", Review: store.Candidate})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func reviewPost(t *testing.T, rs *reviewServer, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Host = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	rs.ServeHTTP(rec, req)
	return rec
}

func reviewGet(t *testing.T, rs *reviewServer, target string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	rs.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(b)
}

func readDecisions(t *testing.T, path string) []reviewDecision {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []reviewDecision
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var d reviewDecision
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		out = append(out, d)
	}
	return out
}

func TestReviewApproveRejectUndoAndLog(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a := reviewCandidate(t, s, "Кандидат A про порт 1000.", "environment", "tool")
	b := reviewCandidate(t, s, "Кандидат B про правило.", "rule", "user")
	logPath := filepath.Join(dir, "review.log")
	rs, err := newReviewServer(s, logPath)
	if err != nil {
		t.Fatal(err)
	}

	code, body := reviewGet(t, rs, "/")
	if code != http.StatusOK || !strings.Contains(body, "Кандидат A") || !strings.Contains(body, "Кандидат B") ||
		!strings.Contains(body, "approve selected") || !strings.Contains(body, "reject selected") {
		t.Fatalf("page %d: %q", code, body)
	}
	if !strings.Contains(body, rs.token) {
		t.Fatal("the form has no token")
	}

	rec := reviewPost(t, rs, "/approve", url.Values{"token": {rs.token}, "id": {a.ID}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("approve: %d %q", rec.Code, rec.Body.String())
	}
	got, _ := s.Get(a.ID)
	if got.Status != store.Active || got.Review != "" || got.Version != 2 {
		t.Errorf("approved fact %+v", got)
	}
	got, _ = s.Get(b.ID)
	if got.Review != store.Candidate {
		t.Errorf("an unselected fact changed: %+v", got)
	}

	rec = reviewPost(t, rs, "/reject", url.Values{"token": {rs.token}, "id": {b.ID}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reject: %d %q", rec.Code, rec.Body.String())
	}
	got, _ = s.Get(b.ID)
	if got.Status != store.Retired || got.Version != 2 {
		t.Errorf("rejected fact %+v", got)
	}
	// A double click must not retire the same fact again.
	rec = reviewPost(t, rs, "/reject", url.Values{"token": {rs.token}, "id": {b.ID}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "failed") {
		t.Errorf("second reject %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got, _ = s.Get(b.ID); got.Version != 2 {
		t.Errorf("a second reject wrote a version: %+v", got)
	}

	decisions := readDecisions(t, logPath)
	if len(decisions) != 2 || decisions[0].Decision != "approve" || decisions[1].Decision != "reject" ||
		decisions[1].Reason != "rejected in review" {
		t.Errorf("log %+v", decisions)
	}

	rec = reviewPost(t, rs, "/undo", url.Values{"token": {rs.token}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("undo: %d %q", rec.Code, rec.Body.String())
	}
	got, _ = s.Get(b.ID)
	if got.Status != store.Active || got.Review != store.Candidate || got.Version != 3 {
		t.Errorf("undo of reject %+v", got)
	}
	decisions = readDecisions(t, logPath)
	if len(decisions) != 3 || decisions[2].Decision != "undo" {
		t.Errorf("log after undo %+v", decisions)
	}

	// A second undo has nothing to do and must not rewrite anything.
	rec = reviewPost(t, rs, "/undo", url.Values{"token": {rs.token}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "nothing+to+undo") {
		t.Errorf("second undo %d %q", rec.Code, rec.Header().Get("Location"))
	}
	got, _ = s.Get(b.ID)
	if got.Version != 3 {
		t.Errorf("second undo wrote a version: %+v", got)
	}
}

func TestReviewFiltersAndEscapesHTML(t *testing.T) {
	dir := env(t)
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	reviewCandidate(t, s, "Правило пользователя.", "rule", "user")
	reviewCandidate(t, s, "<script>alert(1)</script> выдумка", "environment", "tool")
	rs, err := newReviewServer(s, filepath.Join(dir, "review.log"))
	if err != nil {
		t.Fatal(err)
	}

	code, body := reviewGet(t, rs, "/?kind=rule&source=user")
	if code != http.StatusOK || !strings.Contains(body, "Правило пользователя") || strings.Contains(body, "выдумка") {
		t.Errorf("filtered page %d: %q", code, body)
	}
	code, body = reviewGet(t, rs, "/?kind=environment")
	if code != http.StatusOK || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") ||
		strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("escaping %d: %q", code, body)
	}
}

func TestReviewRejectsBadToken(t *testing.T) {
	dir := env(t)
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	f := reviewCandidate(t, s, "Кандидат.", "rule", "user")
	rs, err := newReviewServer(s, filepath.Join(dir, "review.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range []url.Values{{"id": {f.ID}}, {"token": {"wrong"}, "id": {f.ID}}} {
		rec := reviewPost(t, rs, "/approve", form)
		if rec.Code != http.StatusForbidden {
			t.Errorf("bad token %v: %d", form, rec.Code)
		}
	}
	got, _ := s.Get(f.ID)
	if got.Review != store.Candidate {
		t.Errorf("a bad token changed the fact: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "review.log")); !os.IsNotExist(err) {
		t.Errorf("a bad token wrote a log: %v", err)
	}
}

func TestReviewUndoRefusesAnEditedFact(t *testing.T) {
	dir := env(t)
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	f := reviewCandidate(t, s, "Кандидат, который потом поправят.", "rule", "user")
	rs, err := newReviewServer(s, filepath.Join(dir, "review.log"))
	if err != nil {
		t.Fatal(err)
	}
	reviewPost(t, rs, "/approve", url.Values{"token": {rs.token}, "id": {f.ID}})
	if _, err := s.Correct(f.ID, "Уже другая формулировка.", nil); err != nil {
		t.Fatal(err)
	}
	rec := reviewPost(t, rs, "/undo", url.Values{"token": {rs.token}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "changed+since") {
		t.Fatalf("undo %d %q", rec.Code, rec.Header().Get("Location"))
	}
	got, _ := s.Get(f.ID)
	if got.Review != "" || got.Version != 3 {
		t.Errorf("an undo overwrote the edit: %+v", got)
	}
}

// A candidate approved after the page was rendered is not retired by the stale rejection: RetireCandidate re-checks
// the state under the write, and the page reports the failure as before.
func TestReviewRejectAfterConcurrentApproval(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f := reviewCandidate(t, s, "Кандидат, которого одобрят до отказа.", "rule", "user")
	logPath := filepath.Join(dir, "review.log")
	rs, err := newReviewServer(s, logPath)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := reviewGet(t, rs, "/"); code != http.StatusOK {
		t.Fatalf("page %d", code)
	}
	approved, err := s.Approve(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := reviewPost(t, rs, "/reject", url.Values{"token": {rs.token}, "id": {f.ID}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "failed") {
		t.Fatalf("stale reject %d %q", rec.Code, rec.Header().Get("Location"))
	}
	got, _ := s.Get(f.ID)
	if got.Status != store.Active || got.Review != "" || got.Version != approved.Version {
		t.Errorf("a stale reject changed the approved fact: %+v", got)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("a refused reject wrote a log: %v", err)
	}
}

func TestReviewRejectsForeignHost(t *testing.T) {
	dir := env(t)
	s, _ := store.Open(factsPath())
	defer func() { _ = s.Close() }()
	reviewCandidate(t, s, "Кандидат.", "rule", "user")
	rs, err := newReviewServer(s, filepath.Join(dir, "review.log"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.example:80"
	rec := httptest.NewRecorder()
	rs.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign Host got %d", rec.Code)
	}
	for _, host := range []string{"127.0.0.1:1234", "localhost:1234", "[::1]:1234"} {
		if !loopbackHost(host) {
			t.Errorf("%q rejected", host)
		}
	}
	for _, host := range []string{"", "evil.example:80", "192.0.2.1:80"} {
		if loopbackHost(host) {
			t.Errorf("%q accepted", host)
		}
	}
}

func TestReviewLoopbackOnly(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "127.0.0.1:8080", "localhost:0", "[::1]:0"} {
		if err := loopbackAddr(addr); err != nil {
			t.Errorf("%q rejected: %v", addr, err)
		}
	}
	for _, addr := range []string{":0", "0.0.0.0:0", "192.0.2.1:0", "example.com:0", "127.0.0.1"} {
		if err := loopbackAddr(addr); err == nil {
			t.Errorf("%q accepted", addr)
		}
	}
}
