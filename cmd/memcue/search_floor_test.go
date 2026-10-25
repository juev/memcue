package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// search hands over the facts that fit (#407): one fact the query names comes alone, -min-score 0 hands over every
// fact up to -n as before, and a query nothing fits says so instead of printing nothing.
func TestSearchHandsOverWhatFits(t *testing.T) {
	env(t)
	nats := addFactForTest(t, "-origin", "x 2026-10-25", "-keywords", "nats,брокер",
		"Брокер nats на db1 слушает порт 4222, мониторинг nats на 8222, кластер nats из трёх узлов.")
	for _, c := range []string{"Порт postgres на db1 — 5432.", "На db1 стоит node_exporter.", "db1 перезагружали в среду."} {
		addFactForTest(t, "-origin", "x 2026-10-25", c)
	}
	count := func(args ...string) []string {
		t.Helper()
		var out bytes.Buffer
		if err := runSearch(append([]string{"-json"}, args...), &out); err != nil {
			t.Fatal(err)
		}
		var facts []struct{ ID string }
		if err := json.Unmarshal(out.Bytes(), &facts); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, f := range facts {
			ids = append(ids, f.ID)
		}
		return ids
	}
	if got := count("брокер nats db1"); len(got) != 1 || got[0] != nats {
		t.Errorf("default: %q, want the nats fact alone", got)
	}
	if got := count("-min-score", "0", "брокер nats db1"); len(got) != 4 {
		t.Errorf("-min-score 0: %d facts, want all four", len(got))
	}
	var out bytes.Buffer
	if err := runSearch([]string{"kubernetes"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "No stored facts match." {
		t.Errorf("a query nothing fits printed %q", out.String())
	}
}

// The flags of #409 reach the search: outside a repository the default is the whole base, -kind and -in narrow it,
// -all-words requires each word, and a wrong value fails before the base is opened.
func TestSearchFlags(t *testing.T) {
	env(t)
	rule := addFactForTest(t, "-origin", "x 2026-10-25", "-kind", "rule", "-scope", "global", "nats держим на 4222.")
	pit := addFactForTest(t, "-origin", "x 2026-10-25", "-kind", "pitfall", "-scope", "repo:example.com/them/lib",
		"nats на db1 падает без памяти.")
	ids := func(args ...string) []string {
		t.Helper()
		var out bytes.Buffer
		if err := runSearch(append([]string{"-json"}, args...), &out); err != nil {
			t.Fatal(err)
		}
		var facts []struct{ ID string }
		if err := json.Unmarshal(out.Bytes(), &facts); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, f := range facts {
			ids = append(ids, f.ID)
		}
		return ids
	}
	if got := ids("-min-score", "0", "nats"); len(got) != 2 {
		t.Errorf("default outside a repository: %q, want both facts", got)
	}
	if got := ids("-min-score", "0", "-in", "repo:example.com/them/lib", "nats"); len(got) != 1 || got[0] != pit {
		t.Errorf("-in repo:KEY: %q, want the pitfall", got)
	}
	if got := ids("-min-score", "0", "-in", "global", "nats"); len(got) != 1 || got[0] != rule {
		t.Errorf("-in global: %q, want the rule", got)
	}
	if got := ids("-min-score", "0", "-in", "all", "-kind", "pitfall", "nats"); len(got) != 1 || got[0] != pit {
		t.Errorf("-in all -kind pitfall: %q, want the pitfall", got)
	}
	if got := ids("-in", "all", "-all-words", "nats db1"); len(got) != 1 || got[0] != pit {
		t.Errorf("-all-words: %q, want the fact with both words", got)
	}
	for _, bad := range [][]string{{"-in", "everywhere"}, {"-kind", "wish"}, {"-since", "yesterday"}} {
		if err := runSearch(append(bad, "nats"), &bytes.Buffer{}); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

// A query in Chinese, Japanese or Korean gets the hint to search in the facts' languages, with facts or without
// (#410); a Russian or English query does not.
func TestSearchHintsOnCJK(t *testing.T) {
	env(t)
	addFactForTest(t, "-origin", "x 2026-10-25", "Headscale на сервере обновляют через docker compose pull.")
	run := func(query string) string {
		t.Helper()
		var out bytes.Buffer
		if err := runSearch([]string{query}, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := run("如何更新 headscale"); !strings.Contains(got, "Headscale на сервере") || !strings.Contains(got, cjkHint) {
		t.Errorf("CJK query with a hit: %q", got)
	}
	if got := run("如何更新"); !strings.Contains(got, "No stored facts match.") || !strings.Contains(got, cjkHint) {
		t.Errorf("CJK query without a hit: %q", got)
	}
	if got := run("headscale обновить"); strings.Contains(got, cjkHint) {
		t.Errorf("a Russian query got the hint: %q", got)
	}
}

// In a repository, search finds the repository's, this host's and the global facts without -in, and an answer with
// none of them names -in all (#418).
func TestSearchDefaultInARepository(t *testing.T) {
	dir := env(t)
	repo := filepath.Join(dir, "shop")
	key := gitRepo(t, repo)
	t.Chdir(repo)
	own := addFactForTest(t, "-origin", "x 2026-10-26", "-scope", "repo:"+key, "nats магазина слушает 4222.")
	host := addFactForTest(t, "-origin", "x 2026-10-26", "-scope", "host:"+currentHost(), "nats на этом хосте в docker.")
	global := addFactForTest(t, "-origin", "x 2026-10-26", "-scope", "global", "nats держим на 4222 везде.")
	addFactForTest(t, "-origin", "x 2026-10-26", "-scope", "repo:example.com/them/lib", "nats библиотеки на 4223.")
	var out bytes.Buffer
	if err := runSearch([]string{"-min-score", "0", "nats"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{own, host, global} {
		if !strings.Contains(out.String(), id) {
			t.Errorf("default search lacks %s: %q", id, out.String())
		}
	}
	if strings.Contains(out.String(), "библиотеки") {
		t.Errorf("default search found another repository's fact: %q", out.String())
	}
	out.Reset()
	if err := runSearch([]string{"4223"}, &out); err != nil {
		t.Fatal(err)
	}
	want := "No facts of this repository, this host or global match; -in all searches every repository."
	if strings.TrimSpace(out.String()) != want {
		t.Errorf("an empty default answer printed %q, want %q", out.String(), want)
	}
}

// -in repo:KEY takes the end of a stored key, and a key nothing ends with fails with the stored keys named (#418).
func TestSearchResolvesARepositorySuffix(t *testing.T) {
	env(t)
	lib := addFactForTest(t, "-origin", "x 2026-10-26", "-scope", "repo:example.com/them/lib", "nats библиотеки на 4223.")
	addFactForTest(t, "-origin", "x 2026-10-26", "-scope", "repo:example.com/them/app", "nats приложения на 4224.")
	var out bytes.Buffer
	if err := runSearch([]string{"-in", "repo:lib", "nats"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), lib) || strings.Contains(out.String(), "приложения") {
		t.Errorf("-in repo:lib printed %q, want the library's fact alone", out.String())
	}
	err := runSearch([]string{"-in", "repo:libs", "nats"}, &bytes.Buffer{})
	want := `-in repo:libs: no stored repository key ends with "libs"; known keys: example.com/them/app, example.com/them/lib`
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}
