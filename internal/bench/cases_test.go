package bench

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The facts below are invented: acme projects, example.com repositories, no real host or person.
const (
	releaseFact = "Релиз виджета собирают из ветки release через make dist, сборку подписывают ключом команды."
	cacheFact   = "Кэш каталога acme-shop сбрасывают по хуку после деплоя."
	pluginFact  = "Плагин hledger-vscode версии v2.14.0 ломает подсветку журналов."
	queueFact   = "Очередь заданий acme-jobs хранит задачи в Redis, таймаут воркера тридцать секунд."
)

// one is the action of most cases: a single search with the case's options.
func one(query string, o searchOpts) func(t *testing.T, w *world) answer {
	return func(t *testing.T, w *world) answer { return search(t, w.b, query, o) }
}

// inRepo is one, run as a session working in the named repository.
func inRepo(name, query string, o searchOpts) func(t *testing.T, w *world) answer {
	return func(t *testing.T, w *world) answer {
		o.cwd = w.repo(t, name).dir
		return search(t, w.b, query, o)
	}
}

// wide is the search that leaves nothing out: the whole base off the shelves, expired facts, no floor, room for all.
var wide = searchOpts{in: "all", expired: true, minScore: "0", n: "100"}

func today() string    { return time.Now().Format(time.DateOnly) }
func tomorrow() string { return time.Now().AddDate(0, 0, 1).Format(time.DateOnly) }

// seedRelease stores the release fact next to an unrelated one, so a search that served everything would show.
func seedRelease(t *testing.T, w *world) {
	w.add(t, "release", releaseFact, addOpts{})
	w.add(t, "cache", cacheFact, addOpts{})
}

var cases = []benchCase{
	{
		id: "round-trip", stratum: rule,
		note:   "README: a fact you add yourself with `memcue add` is active at once; search finds it by its words.",
		setup:  seedRelease,
		action: one("релиз виджета ветка release", searchOpts{}),
		expect: expect{found: []string{"release"}, notFound: []string{"cache"}},
	},
	{
		id: "case-endings-keep-fact", stratum: rule,
		note:   "README: search is FTS5 with Snowball stemming for English and Russian, so another case of the word finds it.",
		setup:  seedRelease,
		action: one("релизом сборке", searchOpts{}),
		expect: expect{found: []string{"release"}, notFound: []string{"cache"}},
	},
	{
		id: "letter-case-keeps-fact", stratum: rule,
		note:   "README: search is lexical over stems; the letter case of a query is not part of a term.",
		setup:  seedRelease,
		action: one("РЕЛИЗ ВИДЖЕТА", searchOpts{}),
		expect: expect{found: []string{"release"}, notFound: []string{"cache"}},
	},
	{
		id: "identifier-is-exact-term", stratum: rule,
		note: "README, MEMCUE_NUMBER_TERMS: a query that names an identifier or a version finds the fact naming it.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "plugin", pluginFact, addOpts{})
			w.add(t, "cache", cacheFact, addOpts{})
		},
		action: one("hledger-vscode", searchOpts{}),
		expect: expect{found: []string{"plugin"}, notFound: []string{"cache"}},
	},
	{
		id: "version-is-exact-term", stratum: rule,
		note: "README, MEMCUE_NUMBER_TERMS: a version is one exact term (`1.27` never matches `1.20`), so of two " +
			"facts about neighbouring versions the query's version serves its own fact.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "v2.14", pluginFact, addOpts{})
			w.add(t, "v2.15", "Сборка v2.15.0 добавила экспорт отчётов в CSV.", addOpts{})
		},
		action: one("v2.14.0", wide),
		expect: expect{found: []string{"v2.14"}, notFound: []string{"v2.15"}},
	},
	{
		id: "neighbouring-version-not-served", stratum: rule,
		note: "README, MEMCUE_NUMBER_TERMS: `1.27` becomes a term that never matches `1.20`; a fact about v2.14.0 " +
			"is not served for v2.15.0, even with the floor off.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "plugin", pluginFact, addOpts{})
		},
		action: one("v2.15.0", wide),
		expect: expect{none: true},
	},
	{
		id: "foreign-project-does-not-outrank-own", stratum: rule,
		note: "README, Projects: in a session that works in a repository, search looks at that repository's facts, " +
			"this host's and the global ones by default; a fact of another repository still needs `-in all`. The " +
			"foreign fact here is the shorter one, so BM25 alone would put it first.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "own", queueFact, addOpts{cwd: w.repo(t, "widget").dir})
			w.add(t, "foreign", "Очередь acme-jobs живёт в Redis.", addOpts{cwd: w.repo(t, "gadget").dir})
		},
		action: inRepo("widget", "очередь acme-jobs redis", searchOpts{}),
		expect: expect{found: []string{"own"}, notFound: []string{"foreign"}},
	},
	{
		id: "project-before-global-on-tie", stratum: rule,
		note: "README: of facts with equal scores, one learnt in the session's project comes before a global one. " +
			"The global fact is added second, so the newer-first tie-break alone would put it first.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "own", queueFact, addOpts{cwd: w.repo(t, "widget").dir})
			w.add(t, "global", queueFact, addOpts{scope: "global"})
		},
		action: inRepo("widget", "очередь acme-jobs redis", searchOpts{}),
		expect: expect{order: [][2]string{{"own", "global"}}},
	},
	{
		id: "retired-fact-is-nowhere", stratum: rule,
		note: "README: `memcue retire ID` — no longer recalled; history keeps it. The widest search does not serve it.",
		setup: func(t *testing.T, w *world) {
			seedRelease(t, w)
			w.b.run(t, "", "retire", w.id(t, "release"))
		},
		action: one("релиз виджета ветка release", wide),
		expect: expect{notFound: []string{"release"}},
	},
	{
		id: "reopen-brings-fact-back", stratum: rule,
		note: "README: `memcue reopen ID` — undo a retire: back as it was before.",
		setup: func(t *testing.T, w *world) {
			seedRelease(t, w)
			w.b.run(t, "", "retire", w.id(t, "release"))
			w.b.run(t, "", "reopen", w.id(t, "release"))
		},
		action: one("релиз виджета ветка release", searchOpts{}),
		expect: expect{found: []string{"release"}, also: contentIs("release", releaseFact)},
	},
	{
		id: "correct-replaces-old-words", stratum: rule,
		note: "README: `memcue correct ID` writes a new version; the old text is no longer what search reads.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "billing", "Сервис acme-billing пишет счета в бакет invoices-archive раз в сутки.", addOpts{})
			w.b.run(t, "", "correct", w.id(t, "billing"), "Сервис acme-billing пишет счета в хранилище ledger-cold.")
		},
		action: one("invoices-archive", wide),
		expect: expect{notFound: []string{"billing"}},
	},
	{
		id: "correct-serves-new-text", stratum: rule,
		note: "README: `memcue correct ID \"…\"` — the corrected text is found, under the same id.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "billing", "Сервис acme-billing пишет счета в бакет invoices-archive раз в сутки.", addOpts{})
			w.b.run(t, "", "correct", w.id(t, "billing"), "Сервис acme-billing пишет счета в хранилище ledger-cold.")
		},
		action: one("ledger-cold", searchOpts{}),
		expect: expect{found: []string{"billing"},
			also: contentIs("billing", "Сервис acme-billing пишет счета в хранилище ledger-cold.")},
	},
	{
		id: "expired-fact-left-out", stratum: rule,
		note: "README: `-expired` includes facts past their expiry; without it they are left out (search's own help).",
		setup: func(t *testing.T, w *world) {
			w.add(t, "past", "Окно миграции кластера orchid закрыто до второго января.", addOpts{expires: "2026-01-02"})
			w.add(t, "future", "Окно миграции кластера orchid-two открыто весь год.", addOpts{expires: "2099-01-02"})
		},
		action: one("кластера orchid", searchOpts{minScore: "0"}),
		expect: expect{found: []string{"future"}, notFound: []string{"past"}},
	},
	{
		id: "expired-flag-serves-it", stratum: rule,
		note: "README: `memcue search -all-words -expired` — facts past their expiry included, with their `expires`.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "past", "Окно миграции кластера orchid закрыто до второго января.", addOpts{expires: "2026-01-02"})
		},
		action: one("кластера orchid", searchOpts{expired: true}),
		expect: expect{found: []string{"past"}, also: func(t *testing.T, w *world, a answer) {
			if h := a.hits[0]; h.Expires != "2026-01-02" {
				t.Errorf("expires %q, want 2026-01-02", h.Expires)
			}
		}},
	},
	{
		id: "n-1-returns-one", stratum: rule,
		note:   "README: at most -n facts; `-n 1` is one fact, the best.",
		setup:  seedAnsible,
		action: one("ansible", searchOpts{n: "1"}),
		expect: expect{most: 1, also: func(t *testing.T, w *world, a answer) {
			d := search(t, w.b, "ansible", searchOpts{})
			if len(a.hits) != 1 || len(d.hits) == 0 || a.hits[0].ID != d.hits[0].ID {
				t.Errorf("-n 1 served %s, want the first of %s", w.names(a.ids()), w.names(d.ids()))
			}
		}},
	},
	{
		id: "min-score-0-returns-up-to-n", stratum: rule,
		note: "README: `memcue search -min-score 0` — every fact up to -n, the floor off. The default floor leaves " +
			"the facts that hold one of the two words out; with it off they all come, and still no more than -n.",
		setup:  seedAnsible,
		action: one("ansible бастион", searchOpts{minScore: "0", n: "4"}),
		expect: expect{most: 4, found: []string{"bastion-only", "bastion-cert"}, also: func(t *testing.T, w *world, a answer) {
			if len(a.hits) != 4 {
				t.Errorf("%d facts with the floor off, want -n 4", len(a.hits))
			}
			if d := search(t, w.b, "ansible бастион", searchOpts{n: "4"}); len(d.hits) >= len(a.hits) {
				t.Errorf("the default floor served %d facts, no fewer than the floor off: the case tests nothing",
					len(d.hits))
			}
		}},
	},
	{
		id: "kind-keeps-only-that-kind", stratum: rule,
		note:   "README: `memcue search -kind rule,decision` — only facts of those kinds.",
		setup:  seedAnsible,
		action: one("ansible", searchOpts{kind: "decision", minScore: "0", n: "20"}),
		expect: expect{found: []string{"ops-repo"}, most: 1},
	},
	{
		id: "since-keeps-facts-of-that-day", stratum: rule,
		note:  "README: `-since 2026-10-01` — only facts learnt since that day, the day itself included.",
		setup: seedRelease,
		action: func(t *testing.T, w *world) answer {
			return search(t, w.b, "релиз виджета", searchOpts{since: today()})
		},
		expect: expect{found: []string{"release"}},
	},
	{
		id: "since-drops-earlier-facts", stratum: rule,
		note: "README: `-since` — only facts learnt since that day. A fact added today is earlier than tomorrow; the " +
			"CLI cannot backdate a fact, so the day after is the earlier side.",
		setup: seedRelease,
		action: func(t *testing.T, w *world) answer {
			return search(t, w.b, "релиз виджета", searchOpts{since: tomorrow(), minScore: "0"})
		},
		expect: expect{none: true},
	},
	{
		id: "in-all-searches-whole-base", stratum: rule,
		note: "README, Projects: `-in all` searches every repository; a fact of another repository is found there.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "own", queueFact, addOpts{cwd: w.repo(t, "widget").dir})
			w.add(t, "foreign", "Очередь acme-jobs живёт в Redis.", addOpts{cwd: w.repo(t, "gadget").dir})
		},
		action: inRepo("widget", "очередь acme-jobs redis", searchOpts{in: "all"}),
		expect: expect{found: []string{"own", "foreign"}},
	},
	{
		id: "same-query-same-order", stratum: rule,
		note: "docs/research/scope-filter-2026-10.md: recall without a model is deterministic. The same query twice " +
			"returns the same ids in the same order, ties included.",
		setup: func(t *testing.T, w *world) {
			seedAnsible(t, w)
			w.add(t, "twin", "Ansible запускают только с бастиона.", addOpts{})
		},
		action: one("ansible бастион", searchOpts{minScore: "0", n: "20"}),
		expect: expect{also: func(t *testing.T, w *world, a answer) {
			for range 3 {
				if again := search(t, w.b, "ansible бастион", searchOpts{minScore: "0", n: "20"}); !slices.Equal(again.ids(), a.ids()) {
					t.Errorf("second answer %s, first %s", w.names(again.ids()), w.names(a.ids()))
				}
			}
		}},
	},
	{
		id: "json-shape", stratum: rule,
		note: "README: `search -json` carries each fact's `scope`; searchFact in cmd/memcue/facts.go is the contract: " +
			"id, content and created always, keywords, origin, scope and expires when set, nothing else.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "full", queueFact, addOpts{cwd: w.repo(t, "widget").dir, scope: "repo", kind: "environment",
				keywords: "очередь, redis", expires: "2099-12-31"})
			w.add(t, "bare", "Очередь acme-jobs живёт в Redis.", addOpts{})
		},
		action: inRepo("widget", "очередь acme-jobs redis", searchOpts{in: "all"}),
		expect: expect{found: []string{"full", "bare"}, shape: true, also: func(t *testing.T, w *world, a answer) {
			for _, h := range a.hits {
				if h.ID != w.id(t, "full") {
					continue
				}
				r := w.repo(t, "widget")
				if h.Scope != "repo:"+r.key || !strings.HasPrefix(h.Origin, r.key+" ") || h.Expires != "2099-12-31" ||
					len(h.Keywords) != 2 {
					t.Errorf("full fact %+v", h)
				}
			}
		}},
	},
	{
		id: "one-word-needs-shared-term", stratum: rule,
		note: "README: search is FTS5 over the query's terms; a fact that shares no term with a one-word query is " +
			"not served, even with the floor off.",
		setup:  seedRelease,
		action: one("деплоя", searchOpts{minScore: "0", n: "20"}),
		expect: expect{found: []string{"cache"}, notFound: []string{"release"}},
	},
	{
		id: "paraphrase-served-next-to-original", stratum: characterization,
		note: "Records that search does not merge duplicates: a paraphrase and its original come back side by " +
			"side. Removing duplicates is the dream's and the review's job (README, Dream), not search's.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "original", "Сборка acme-widget падает без переменной WIDGET_HOME, её задают в Makefile.", addOpts{})
			w.add(t, "paraphrase", "Без WIDGET_HOME сборка acme-widget не проходит: переменную выставляет Makefile.", addOpts{})
			w.add(t, "other", "Тесты acme-widget гоняют через make check.", addOpts{})
		},
		action: one("WIDGET_HOME сборка", searchOpts{}),
		expect: expect{found: []string{"original", "paraphrase"}, notFound: []string{"other"}},
	},
	{
		id: "empty-base-answers-quietly", stratum: characterization,
		note: "Records what a search of an empty base does: exit 0, nothing on stderr, `{\"facts\":[]}` with -json, and a " +
			"one-line message on stdout without it. The message is on stdout, not stderr: a host shows it to the " +
			"agent as the answer.",
		action: one("релиз виджета", searchOpts{}),
		expect: expect{none: true, also: func(t *testing.T, w *world, a answer) {
			if strings.TrimSpace(a.stdout) != `{"facts":[]}` || a.stderr != "" {
				t.Errorf("-json stdout %q, stderr %q; want no facts, no hints and nothing on stderr", a.stdout, a.stderr)
			}
			stdout, stderr := w.b.run(t, "", "search", "релиз виджета")
			if strings.TrimSpace(stdout) == "" || stderr != "" {
				t.Errorf("stdout %q, stderr %q; want a message on stdout and nothing on stderr", stdout, stderr)
			}
		}},
	},
	{
		id: "no-match-gives-hint", stratum: characterization,
		note: "Records that a query nothing fits returns no fact, and a hint that holds no fact: on stdout without " +
			"-json, in `hints` with it (#453). Inside a repository the hint names `-in all`; only that it is there " +
			"is asserted, not its wording.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "own", queueFact, addOpts{cwd: w.repo(t, "widget").dir})
		},
		action: inRepo("widget", "кофеварка", searchOpts{}),
		expect: expect{none: true, also: func(t *testing.T, w *world, a answer) {
			stdout, _ := w.b.run(t, w.repo(t, "widget").dir, "search", "кофеварка")
			if strings.TrimSpace(stdout) == "" || strings.Contains(stdout, w.id(t, "own")) {
				t.Errorf("stdout %q; want a hint and no fact", stdout)
			}
			if len(a.hints) != 1 || !strings.Contains(a.hints[0], "-in all") {
				t.Errorf("-json hints %q, want the one that names -in all", a.hints)
			}
		}},
	},
	{
		id: "no-match-shelf-json", stratum: rule,
		note: "README (#433): a search that finds nothing while its words fit a shelved fact says so. #453: the " +
			"hosts parse -json, so the hint is in `hints` there too, and the fact itself stays on the shelf.",
		setup: func(t *testing.T, w *world) {
			w.add(t, "shelved", "Векторный поиск: bge-m3 на 255 фактах даёт AUC 0.91 для склейки дублей.",
				addOpts{scope: "shelf:research"})
		},
		action: one("bge-m3 AUC склейки дублей", searchOpts{}),
		expect: expect{none: true, also: func(t *testing.T, w *world, a answer) {
			if len(a.hints) != 1 || !strings.Contains(a.hints[0], "-in shelf:research") {
				t.Errorf("-json hints %q, want the shelf named", a.hints)
			}
		}},
	},
}

// seedAnsible stores facts that share the word ansible to different degrees, one of them a decision, the rest rules.
func seedAnsible(t *testing.T, w *world) {
	w.add(t, "deploy", "Деплой витрины acme-shop идёт через ansible, плейбук deploy.yml.", addOpts{kind: "rule"})
	w.add(t, "playbook", "Плейбук ansible для acme-shop лежит в каталоге ops.", addOpts{kind: "rule"})
	w.add(t, "vault", "Ключи ansible хранятся в vault команды.", addOpts{kind: "rule"})
	w.add(t, "bastion-only", "Ansible запускают только с бастиона.", addOpts{kind: "rule"})
	w.add(t, "bastion-cert", "Бастион ansible пускает по сертификату.", addOpts{kind: "rule"})
	w.add(t, "ops-repo", "Решили держать ansible в отдельном репозитории ops.", addOpts{kind: "decision"})
	w.add(t, "cache", cacheFact, addOpts{kind: "rule"})
}

// contentIs checks that a served fact carries this text, so a fact that came back is the one expected and not an
// older version of it.
func contentIs(name, text string) func(t *testing.T, w *world, a answer) {
	return func(t *testing.T, w *world, a answer) {
		for _, h := range a.hits {
			if h.ID == w.id(t, name) && h.Content != text {
				t.Errorf("fact %q content %q, want %q", name, h.Content, text)
			}
		}
	}
}

func TestCases(t *testing.T) {
	runCases(t, cases)
}
