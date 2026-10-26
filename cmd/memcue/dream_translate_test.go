package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// fakeTranslate answers every translation with "translated <id>" and records the ids it got.
func fakeTranslate(t *testing.T) *[]string {
	t.Helper()
	var sent []string
	old := translateFn
	translateFn = func(input string) ([]extract.Translated, error) {
		var req struct {
			Facts []struct{ ID, Text string } `json:"facts"`
		}
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return nil, err
		}
		var r []extract.Translated
		for _, f := range req.Facts {
			sent = append(sent, f.ID)
			r = append(r, extract.Translated{ID: f.ID, Translation: "translated " + f.ID})
		}
		return r, nil
	}
	t.Cleanup(func() { translateFn = old })
	return &sent
}

// translateBase stores three unrelated facts that already have search queries; the last one has its translation too.
func translateBase(t *testing.T) (first, second, done string) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	add := func(c string, q ...string) string {
		f, err := s.AddWith(c, nil, "x 2026-10-19", "", store.Meta{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetQueries(f.ID, f.Version, q); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // a ULID orders by millisecond only
		return f.ID
	}
	first = add("Postgres демо-базы слушает порт 5432 на db1.", "база данных")
	second = add("Gradle в demo-app требует JAVA_HOME=/opt/jdk-21.", "сборка")
	done = add("Caddy проксирует vault.example.org.", "прокси", extract.TranslationPrefix+"Caddy proxies vault.example.org.")
	return
}

// The regular dream translates the facts that have no translation, keeps their search queries, and leaves a fact
// that has one alone; once every fact has one, the dream has nothing to do (#396).
func TestDreamTranslatesTheFactsOwedATranslation(t *testing.T) {
	dir := env(t)
	first, second, done := translateBase(t)
	sent := fakeTranslate(t)
	var out bytes.Buffer
	if err := dreamModel(nothingFresh(), &out); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 2 || slices.Contains(*sent, done) {
		t.Fatalf("sent %q, want %s and %s", *sent, first, second)
	}
	for id, q := range map[string]string{first: "база данных", second: "сборка"} {
		f := storedFact(t, id)
		if tr, ok := extract.Translation(f.Queries); !ok || tr != "translated "+id || !slices.Contains(f.Queries, q) {
			t.Errorf("fact %s queries %q", id, f.Queries)
		}
	}
	if f := storedFact(t, done); f.Version != 2 {
		t.Errorf("a translated fact got another version: %+v", f)
	}
	if !strings.Contains(out.String(), "translated 2 facts") {
		t.Errorf("out %q", out.String())
	}
	if r := readDreamLog(t, dir); r[len(r)-1].Translated != 2 {
		t.Errorf("record %+v", r[len(r)-1])
	}
	out.Reset()
	if err := dreamModel(nothingFresh(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing for the dream") {
		t.Errorf("the next dream still has work: %q", out.String())
	}
}

// A dream translates at most packetTranslate facts, the newest first; the next one takes the rest.
func TestDreamTranslatesAPacketAtATime(t *testing.T) {
	env(t)
	first, second, _ := translateBase(t)
	old := packetTranslate
	packetTranslate = 1
	t.Cleanup(func() { packetTranslate = old })
	sent := fakeTranslate(t)
	if err := dreamModel(nothingFresh(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*sent, []string{second}) {
		t.Fatalf("first dream sent %q, want the newest %s", *sent, second)
	}
	if err := dreamModel(nothingFresh(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*sent, []string{second, first}) {
		t.Errorf("second dream sent %q, want %s next", *sent, first)
	}
}

// A dream that fell behind still translates: the fresh facts beyond the packet's review are translated now, and they
// stay fresh, so a later packet still reviews them.
func TestDreamTranslatesTheFreshFactsItLeavesForLater(t *testing.T) {
	env(t)
	first, second, _ := translateBase(t)
	old := packetFresh
	packetFresh = 1
	t.Cleanup(func() { packetFresh = old })
	sent := fakeTranslate(t)
	since := []string{"-since", "2000-01-01"}
	path, err := prepareAnswer(since, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var p dreamPacket
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), packetFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Split) != 1 || len(*sent) != 1 {
		t.Fatalf("packet reviews %v and translates %q, want one of each", p.Split, *sent)
	}
	left := (*sent)[0]
	if _, ok := p.Split[left]; ok || left != first && left != second {
		t.Fatalf("translated %s, which the packet also reviews", left)
	}
	if err := runDream([]string{"-apply", path}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := storedFact(t, left); owesTranslation(f) {
		t.Fatalf("not translated: %q", f.Queries)
	}
	seen, err := readDreamSeen(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := seen[left]; ok && v == storedFact(t, left).Version {
		t.Errorf("the translation marked a fresh fact seen at version %d", v)
	}
}

// A fact without search queries is expanded first and translated by a later dream, so the two never race on one
// version; a translation for a fact the packet did not give is ignored, and one for a fact corrected since the packet
// is refused, since it describes the old text.
func TestDreamTranslationChecks(t *testing.T) {
	dir := env(t)
	first, second, done := translateBase(t)
	bare := addFact(t, "Nginx на edge1 отдаёт статику из /srv/www.")
	fakeExpand(t, nil)
	sent := fakeTranslate(t)
	path, err := prepareAnswer(nothingFresh(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(*sent, bare) {
		t.Errorf("a fact without queries was sent to translate: %q", *sent)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var a dreamAnswer
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	a.Translations = append(a.Translations, extract.Translated{ID: done, Translation: "not asked"})
	if err := writeJSONFile(path, a); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(first, "Postgres демо-базы слушает порт 5433 на db1.", nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := runDream([]string{"-apply", path}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f := storedFact(t, done); strings.Contains(strings.Join(f.Queries, " "), "not asked") {
		t.Errorf("a translation the packet did not ask for was written: %q", f.Queries)
	}
	if f := storedFact(t, first); !owesTranslation(f) {
		t.Errorf("a fact corrected since the packet got the old text's translation: %q", f.Queries)
	}
	if f := storedFact(t, second); owesTranslation(f) {
		t.Errorf("second was not translated: %q", f.Queries)
	}
	recs := readDreamLog(t, dir)
	if last := recs[len(recs)-1]; last.Translated != 1 || !slices.ContainsFunc(last.Refused, func(r string) bool {
		return strings.HasPrefix(r, "translate "+first)
	}) {
		t.Errorf("record %+v", last)
	}
}

// The session start says a dream is due when facts are owed a translation, even with no fresh fact.
func TestDreamHintNamesTheFactsOwedATranslation(t *testing.T) {
	dir := env(t)
	translateBase(t)
	state := filepath.Join(dir, "state", "memcue")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := markDream(state, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := writeDreamSeen(state, map[string]int{}); err != nil {
		t.Fatal(err)
	}
	hint := dreamHint()
	if !strings.Contains(hint, "2 facts lack a translation") || !strings.Contains(hint, "memcue-dream") {
		t.Errorf("hint %q", hint)
	}
}
