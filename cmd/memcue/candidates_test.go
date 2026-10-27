package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
)

const gradleFact = "Gradle вне make требует JAVA_HOME=/opt/homebrew/opt/openjdk@21, иначе сборка не находит JDK."

// searched is what memcue search prints for query.
func searched(t *testing.T, query string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runSearch([]string{query}, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// legacyCandidate writes a fact the way memcue 0.20.0 and older stored a candidate: a row with review=candidate and
// no index row. The next open finds the row missing from the index and indexes it (#514).
func legacyCandidate(t *testing.T, id, content, kind, scope, source string) {
	t.Helper()
	legacyRow(t, id, content, kind, scope, source, store.Candidate, "")
}

// legacyRow writes version 1 of a fact as an older memcue left it, with its review state and expiry, past the store:
// this memcue never writes a review state.
func legacyRow(t *testing.T, id, content, kind, scope, source, review, expires string) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	db, err := sql.Open("sqlite", factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO facts(id, version, content, keywords, origin, created, status, author, kind,
		scope, source, review, expires) VALUES (?, 1, ?, '', 'hermes 2026-10-20', '2026-10-20T12:00:00Z', 'active', 'old',
		?, ?, ?, ?, ?)`, id, content, kind, scope, source, review, expires); err != nil {
		t.Fatal(err)
	}
}

// The dream reads every active fact, expired ones included, as it always read an expired approved fact: a legacy
// candidate or provisional fact is read on the same terms whatever its review state, and a correction, which drops
// that state, changes nothing about whether the dream reads it (#514).
func TestDreamReadsExpiredFactsWhateverTheirReview(t *testing.T) {
	env(t)
	past := time.Now().AddDate(0, -1, 0).Format(time.DateOnly)
	future := time.Now().AddDate(0, 1, 0).Format(time.DateOnly)
	const (
		liveCand    = "01JDREAMLEGACY000000000001"
		expiredCand = "01JDREAMLEGACY000000000002"
		liveProv    = "01JDREAMLEGACY000000000003"
		expiredProv = "01JDREAMLEGACY000000000004"
	)
	legacyRow(t, liveCand, "Кандидат про порт nginx 8080.", "environment", "", "tool", store.Candidate, future)
	legacyRow(t, expiredCand, "Кандидат про порт redis 6380.", "environment", "", "tool", store.Candidate, past)
	legacyRow(t, liveProv, "Provisional про порт webforge 3000.", "environment", "", "tool", store.Provisional, future)
	legacyRow(t, expiredProv, "Provisional про порт minio 9000.", "environment", "", "tool", store.Provisional, past)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	expiredApproved, err := s.AddWith("Одобренный факт про порт grafana 3001.", nil, "x 2026-10-19", past, store.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	all := []string{liveCand, expiredCand, liveProv, expiredProv, expiredApproved.ID}
	read := func(when string) {
		t.Helper()
		var out bytes.Buffer
		if err := runDream([]string{"-prepare", "-all"}, &out); err != nil {
			t.Fatal(err)
		}
		for _, id := range all {
			if !strings.Contains(out.String(), id) {
				t.Errorf("%s: the dream lacks %s:\n%s", when, id, out.String())
			}
		}
		if n, err := owedTranslations(); err != nil || n != len(all) {
			t.Errorf("%s: owed translations %d %v, want %d", when, n, err, len(all))
		}
	}
	read("before a correction")

	s, err = store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Correct(expiredCand, "Кандидат про порт redis 6381.", nil)
	_ = s.Close()
	if err != nil || f.Review != "" || f.Expires != past {
		t.Fatalf("corrected %+v %v", f, err)
	}
	read("after a correction")
}

// A fact add writes is searchable at once, whatever its source (#514); add has no -candidate any more, and the
// commands of the approval workflow say they are gone.
func TestAddIsSearchableAtOnce(t *testing.T) {
	env(t)
	var out bytes.Buffer
	if err := runAdd([]string{"-source", "memory", "-kind", "environment", "-keywords", "gradle,jdk", gradleFact},
		&out); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out.String())
	if ctx := searched(t, gradlePrompt); !strings.Contains(ctx, "openjdk@21") {
		t.Fatalf("a new fact is not found at once: %q", ctx)
	}
	if f := storedFact(t, id); f.Review != "" || f.Source != "memory" || f.Kind != "environment" {
		t.Errorf("stored %+v", f)
	}
	if err := runAdd([]string{"-candidate", "Кандидат."}, &bytes.Buffer{}); err == nil {
		t.Error("add accepted -candidate")
	}
	for _, cmd := range []string{"candidates", "approve"} {
		if err := errNoApproval(cmd); !strings.Contains(err.Error(), cmd+" is removed (#514)") {
			t.Errorf("%s: %v", cmd, err)
		}
	}

	out.Reset()
	if err := runAdd([]string{"Пользователь пишет коммиты по-английски."}, &out); err != nil {
		t.Fatal(err)
	}
	if f := storedFact(t, strings.TrimSpace(out.String())); f.Review != "" || f.Source != "user" {
		t.Errorf("explicit add %+v", f)
	}
	if err := runAdd([]string{"-source", "boss", "x"}, &bytes.Buffer{}); err == nil {
		t.Error("an unknown source was accepted")
	}
}

// A candidate an older memcue left is found by search, listed by facts and carried by the card (#514).
func TestLegacyCandidateIsSearchedListedAndCarried(t *testing.T) {
	env(t)
	legacyCandidate(t, "01JLEGACYCMD00000000000001", gradleFact, "environment", "", "tool")
	legacyCandidate(t, "01JLEGACYCMD00000000000002", "Правило-кандидат: отвечать коротко.", "rule", "global", "user")
	if ctx := searched(t, gradlePrompt); !strings.Contains(ctx, "openjdk@21") {
		t.Errorf("a legacy candidate is not found: %q", ctx)
	}
	var out bytes.Buffer
	if err := runFacts(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "01JLEGACYCMD00000000000001") || !strings.Contains(out.String(), "01JLEGACYCMD00000000000002") {
		t.Errorf("facts %q", out.String())
	}
	if f := storedFact(t, "01JLEGACYCMD00000000000001"); f.Review != store.Candidate || f.Version != 1 {
		t.Errorf("the legacy row changed: %+v", f)
	}
	if ctx := sessionStartContext(t, t.TempDir(), "startup", "s1"); !strings.Contains(ctx, "отвечать коротко") {
		t.Errorf("the card lacks a legacy candidate rule: %q", ctx)
	}
}

// A provisional fact the removed vet left is found by search, listed by facts and, for a global rule, carried by the
// card, with its expiry, until that expiry passes (#514).
func TestLegacyProvisionalIsSearchedListedAndCarried(t *testing.T) {
	env(t)
	day := future()
	legacyRow(t, "01JLEGACYCMD00000000000011", "Provisional правило: писать тесты первыми quebec.", "rule", "global",
		"tool", store.Provisional, day)
	legacyRow(t, "01JLEGACYCMD00000000000012", "Provisional правило: просроченное romeo.", "rule", "global",
		"tool", store.Provisional, time.Now().AddDate(0, -1, 0).Format(time.DateOnly))
	if ctx := searched(t, "тесты первыми quebec"); !strings.Contains(ctx, "01JLEGACYCMD00000000000011") {
		t.Errorf("a legacy provisional fact is not found: %q", ctx)
	}
	if ctx := searched(t, "просроченное romeo"); strings.Contains(ctx, "01JLEGACYCMD00000000000012") {
		t.Errorf("an expired provisional fact is found: %q", ctx)
	}
	var out bytes.Buffer
	if err := runFacts(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "01JLEGACYCMD00000000000011 v1 hermes 2026-10-20 [] expires="+day) {
		t.Errorf("facts %q", out.String())
	}
	ctx := sessionStartContext(t, t.TempDir(), "startup", "s1")
	if !strings.Contains(ctx, "писать тесты первыми") || strings.Contains(ctx, "просроченное") {
		t.Errorf("the card: %q", ctx)
	}
}

// Every imported fact is live, whatever its record says about review: the field made candidates before #514. What the
// fact is and rests on is kept.
func TestImportStoresEveryFactLive(t *testing.T) {
	dir := env(t)
	path := filepath.Join(dir, "x.json")
	body := `{"note":"note","origin":"app 2026-10-20","prompt_version":3,"review":false,"facts":[{"content":"Факт заметки.","keywords":["note"],"kind":"environment"}]}` + "\n" +
		`{"note":"session","origin":"app 2026-10-20","review":true,"facts":[{"content":"Факт сессии без kind.","keywords":["session"]}]}` + "\n" +
		`{"note":"old","origin":"app 2026-10-20","facts":[{"content":"Старый файл с kind.","keywords":["old"],"kind":"environment","scope":"host:db1","source":"tool","evidence":"db1"}]}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runImport([]string{path}, &out); err != nil || out.String() != "imported 3 facts\n" {
		t.Fatalf("import %q: %v", out.String(), err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil || len(active) != 3 {
		t.Fatalf("active %+v %v", active, err)
	}
	got := map[string]store.Fact{}
	for _, f := range active {
		if f.Review != "" || f.Origin != "app 2026-10-20" {
			t.Errorf("imported %+v", f)
		}
		got[f.Content] = f
	}
	if f := got["Старый файл с kind."]; f.Kind != "environment" || f.Scope != "host:db1" || f.Source != "tool" ||
		f.Evidence != "db1" {
		t.Errorf("labels lost: %+v", f)
	}
	if f := got["Факт заметки."]; f.PromptVersion != 3 {
		t.Errorf("prompt version lost: %+v", f)
	}
	for _, q := range []string{"note", "session", "old"} {
		hits, err := s.Search([]string{q}, 5)
		if err != nil || len(hits) != 1 {
			t.Errorf("search %q: %+v %v", q, hits, err)
		}
	}
}

// kind and scope are what put a fact on the session card, so add rejects values the card cannot use.
func TestAddValidatesKindAndScope(t *testing.T) {
	env(t)
	for _, args := range [][]string{
		{"-kind", "repo", "x"},
		{"-kind", "planet", "x"},
		{"-scope", "planet", "x"},
		{"-scope", "host:", "x"},
		{"-scope", "repo:a b", "x"},
	} {
		if err := runAdd(args, &bytes.Buffer{}); err == nil {
			t.Errorf("add accepted %v", args)
		}
	}
	var out bytes.Buffer
	if err := runAdd([]string{"-kind", "rule", "-scope", "global", "Всегда отвечай по-русски."}, &out); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Get(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind != "rule" || f.Scope != "global" {
		t.Errorf("stored %+v", f)
	}
}
