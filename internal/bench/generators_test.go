package bench

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// The generators derive subtests from a fixed seed set instead of listing each one by hand: the rule they test is
// "every form keeps the fact", and a hand-written list holds only the forms someone thought of. Each subtest is
// named by the input it mutates, so a red one says which form lost the fact.

// seed is a fact and a query that finds it as written.
type seed struct {
	name  string
	fact  string
	query string
}

// seeds are the facts the generators mutate queries for, stored together so a mutated query has neighbours to lose
// its fact to.
var seeds = []seed{
	{"release", releaseFact, "релиз виджета ветка release"},
	{"cache", cacheFact, "кэш каталога acme-shop хук деплоя"},
	{"plugin", pluginFact, "плагин hledger-vscode подсветка журналов"},
	{"queue", queueFact, "очередь заданий acme-jobs redis таймаут"},
	{"backup", "Бэкап базы acme-crm снимают pg_dump в три часа ночи и держат неделю.", "бэкап базы acme-crm pg_dump"},
}

// seedWorld stores every seed fact in a new base.
func seedWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	for _, s := range seeds {
		w.add(t, s.name, s.fact, addOpts{})
	}
	return w
}

// served reports whether a search serves the named fact.
func served(t *testing.T, w *world, query, name string, o searchOpts) bool {
	t.Helper()
	return slices.Contains(search(t, w.b, query, o).ids(), w.id(t, name))
}

// TestCaseForms: Snowball stems Russian, so the cases of a noun and the forms of a verb find a fact that holds
// another form of the same word (README: FTS5 with Snowball stemming for English and Russian).
func TestCaseForms(t *testing.T) {
	w := seedWorld(t)
	forms := map[string][]string{
		"релиз":     {"релиза", "релизом", "релизу", "релизе", "релизы", "релизов"},
		"сборка":    {"сборке", "сборки", "сборкой", "сборку"},
		"собирать":  {"собирают", "собирает", "собирали", "собираем"},
		"подписать": {"подписывают", "подписывать"},
		"ветка":     {"ветке", "веткой", "ветки"},
	}
	for _, lemma := range sortedKeys(forms) {
		for _, form := range forms[lemma] {
			t.Run(lemma+"/"+form, func(t *testing.T) {
				if !served(t, w, form, "release", searchOpts{}) {
					t.Errorf("%q does not find the fact that holds a form of %q", form, lemma)
				}
			})
		}
	}
}

// TestIdentifierMutation: letter case never changes a term, a cut identifier is still its prefix, and a version is
// one exact term (README, MEMCUE_NUMBER_TERMS), so a neighbouring version is not served as the fact that names
// v2.14.0. The other version is searched alone, with the floor off: with the plugin's name beside it, the name
// rightly finds the fact.
func TestIdentifierMutation(t *testing.T) {
	w := seedWorld(t)
	for _, q := range []string{"hledger-vscode", "HLEDGER-VSCODE", "Hledger-VSCode", "hledger-vscod", "hledger",
		"v2.14.0", "V2.14.0", "HLEDGER-VSCODE v2.14.0"} {
		t.Run("keeps/"+q, func(t *testing.T) {
			if !served(t, w, q, "plugin", searchOpts{}) {
				t.Errorf("%q does not find the fact about hledger-vscode v2.14.0", q)
			}
		})
	}
	for _, q := range []string{"v2.15.0", "v2.14.1", "v2.13.0", "v3.14.0", "v2.14", "2.14.0.1"} {
		t.Run("other-version/"+q, func(t *testing.T) {
			if served(t, w, q, "plugin", searchOpts{minScore: "0", n: "100"}) {
				t.Errorf("%q serves the fact about v2.14.0", q)
			}
		})
	}
}

// padding is the pool of unrelated words a query is padded with: none of them is in a seed fact.
var padding = strings.Fields("погода зонтик апельсин гитара велосипед марафон пианино кактус лимонад чайник " +
	"телескоп пингвин ромашка карусель самовар черника фонарь шахматы ковёр бабочка облако кружка веер палатка")

// TestTruncation: a query cut to its first two words, or padded with twenty unrelated words, still serves its fact
// in the top -n. The padded query reads as a message (#417), so it meets the message floor and the rule that a fact
// holds two of its terms; the padding is drawn from a fixed seed, so a red subtest replays.
func TestTruncation(t *testing.T) {
	w := seedWorld(t)
	rng := rand.New(rand.NewPCG(451, 0))
	for _, s := range seeds {
		words := strings.Fields(s.query)
		cut := strings.Join(words[:2], " ")
		t.Run("cut/"+cut, func(t *testing.T) {
			if !served(t, w, cut, s.name, searchOpts{}) {
				t.Errorf("%q does not find %s", cut, s.name)
			}
		})
		pad := slices.Clone(padding)
		rng.Shuffle(len(pad), func(i, j int) { pad[i], pad[j] = pad[j], pad[i] })
		padded := strings.Join(append(pad[:20], words...), " ")
		t.Run("padded/"+s.query, func(t *testing.T) {
			if !served(t, w, padded, s.name, searchOpts{}) {
				t.Errorf("%q does not find %s", padded, s.name)
			}
		})
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestLatency: README says a search takes about 10 ms. On 1000 facts the 95th percentile of 20 searches, process
// start included, must stay under 250 ms: a budget wide enough that a slow CI machine stays green and a search that
// scans the base row by row does not.
func TestLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("latency: skipped under -short")
	}
	w := newWorld(t)
	importFacts(t, w.b, syntheticFacts(1000))
	queries := []string{"сервис acme-api", "очередь redis", "таймаут воркера", "бэкап базы", "деплой витрины",
		"кэш каталога", "сборка релиза", "ключи vault", "порт postgres", "логи nginx"}
	var took []time.Duration
	for i := range 20 {
		start := time.Now()
		a := search(t, w.b, queries[i%len(queries)], searchOpts{})
		took = append(took, time.Since(start))
		if len(a.hits) == 0 {
			t.Fatalf("%q found nothing in 1000 facts: the base was not imported", queries[i%len(queries)])
		}
	}
	slices.Sort(took)
	p95 := took[len(took)*95/100-1]
	t.Logf("p95 of %d searches over 1000 facts: %v (median %v)", len(took), p95, took[len(took)/2])
	if p95 > 250*time.Millisecond {
		t.Errorf("p95 %v, want under 250ms", p95)
	}
}

// syntheticFacts builds n facts from word pools, the same on every run: a base of varied facts whose terms repeat
// as a real base's do, so BM25 has common and rare terms to weigh.
func syntheticFacts(n int) []string {
	rng := rand.New(rand.NewPCG(1000, 0))
	subjects := strings.Fields("сервис очередь кэш бэкап деплой сборка ключи порт логи витрина воркер планировщик")
	systems := strings.Fields("acme-api acme-shop acme-jobs acme-crm redis postgres nginx vault kafka minio")
	verbs := strings.Fields("хранит пишет читает сбрасывает поднимает ротирует проверяет держит")
	objects := strings.Fields("каталога релиза таймаут базы журналы сертификаты задачи метрики счета индексы")
	facts := make([]string, n)
	for i := range facts {
		facts[i] = fmt.Sprintf("%s %s %s %s, вариант %d.", subjects[rng.IntN(len(subjects))],
			systems[rng.IntN(len(systems))], verbs[rng.IntN(len(verbs))], objects[rng.IntN(len(objects))], i)
	}
	return facts
}

// importFacts stores facts in one process through `memcue import`: a thousand `memcue add` runs would time the
// process start, not the search. review false takes them into recall at once, as add would.
func importFacts(t *testing.T, b *base, facts []string) {
	t.Helper()
	type fact struct {
		Content  string   `json:"content"`
		Keywords []string `json:"keywords"`
	}
	rec := struct {
		Note   string `json:"note"`
		Origin string `json:"origin"`
		Facts  []fact `json:"facts"`
		Review bool   `json:"review"`
	}{Note: "bench", Origin: "bench 2026-10-26"}
	for _, f := range facts {
		rec.Facts = append(rec.Facts, fact{Content: f})
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.dir, "import.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	b.run(t, "", "import", path)
}
