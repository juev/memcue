package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func factHistory(t *testing.T, id string) []store.Fact {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	h, err := s.History(id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func markPastDream(t *testing.T) string {
	t.Helper()
	dir, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := markBase(dir); err != nil {
		t.Fatal(err)
	}
	if err := markDream(dir, time.Now().Add(-dreamEvery-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnglishDefaultPrepareHasNoTranslationTask(t *testing.T) {
	env(t)
	first, second, legacy := translateBase(t)
	noSweep(t)
	before := map[string][]store.Fact{}
	for _, id := range []string{first, second, legacy} {
		before[id] = factHistory(t, id)
	}
	var out bytes.Buffer
	if err := runDream([]string{"-prepare", "-all"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"## Данные: перевод", "## Задача 4. Перевод", `"translations"`} {
		if strings.Contains(out.String(), forbidden) {
			t.Errorf("packet requests translation: %q", forbidden)
		}
	}
	// With all facts already reviewed, lack of translation alone gives no work.
	markPastDream(t)
	out.Reset()
	if err := runDream(append([]string{"-prepare"}, nothingFresh()...), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing for the dream") {
		t.Error("translation-only work still prepared")
	}
	for id, h := range before {
		if !reflect.DeepEqual(h, factHistory(t, id)) {
			t.Errorf("prepare rewrote history of %s", id)
		}
	}
}

func TestEnglishDefaultLegacyTranslationAnswersAreInert(t *testing.T) {
	for _, boundary := range []string{"packet", "plan"} {
		t.Run(boundary, func(t *testing.T) {
			env(t)
			first, _, legacy := translateBase(t)
			before := factHistory(t, first)
			oldLegacy := factHistory(t, legacy)
			dir, err := stateDir()
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if boundary == "packet" {
				// A pre-upgrade saved packet explicitly authorized translation. The new apply must not do it.
				p := dreamPacket{ID: "legacy-packet", Start: time.Now(), Seen: map[string]int{},
					Translate: map[string]int{first: 2, legacy: 2}}
				if err := writeJSONFile(filepath.Join(dir, packetFile), p); err != nil {
					t.Fatal(err)
				}
				a := dreamAnswer{Packet: p.ID, Translations: []extract.Translated{
					{ID: first, Translation: "new ongoing translation"}, {ID: legacy, Translation: "replacement"}}}
				path := filepath.Join(dir, "old-answer.json")
				if err := writeJSONFile(path, a); err != nil {
					t.Fatal(err)
				}
				if err := runDream([]string{"-apply", path}, &out); err == nil {
					t.Fatal("legacy auxiliary work accepted")
				}
			} else {
				plan := dreamPlan{Start: time.Now(), Seen: map[string]int{}, Translations: []dreamPlanTranslation{
					{ID: first, Version: 2, Translation: "new ongoing translation"},
					{ID: legacy, Version: 2, Translation: "replacement"}}}
				if _, err := applyPlan(dir, plan, dreamRecord{}, &out); err == nil {
					t.Fatal("legacy auxiliary plan accepted")
				}
			}
			if !reflect.DeepEqual(before, factHistory(t, first)) || !reflect.DeepEqual(oldLegacy, factHistory(t, legacy)) {
				t.Error("legacy translation answer changed existing facts/history")
			}
			if strings.Contains(out.String(), "translated") {
				t.Errorf("translation in status: %s", out.String())
			}
		})
	}
}

func TestEnglishDefaultNoTranslationOnlyDebtOrCalls(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "background"}[background], func(t *testing.T) {
			env(t)
			first, _, legacy := translateBase(t)
			before := factHistory(t, first)
			dir := markPastDream(t)
			fresh, _, err := dreamDue(dir, dreamEvery)
			if err != nil || fresh != 0 {
				t.Errorf("unexpected maintenance debt: fresh=%d err=%v", fresh, err)
			}
			for _, hint := range []string{dreamHint(), dreamToolsHint()} {
				if hint != "" {
					t.Errorf("translation-only due hint: %s", hint)
				}
			}
			withAPI(t, "https://api.example.test")
			spawns := countSpawns(t)
			startDream()
			if *spawns != 0 {
				t.Errorf("translation-only work spawned %d dreams", *spawns)
			}
			calls := fakeDreamRun(t, nil)
			args := []string{"-run", "-j", "1"}
			if background {
				args = append(args, "-background")
			}
			var out bytes.Buffer
			if err := runDream(args, &out); err != nil {
				t.Fatal(err)
			}
			if *calls != 0 || out.String() != "dream: nothing due\n" {
				t.Errorf("calls=%d status=%s", *calls, out.String())
			}
			if !reflect.DeepEqual(before, factHistory(t, first)) {
				t.Error("translation-only run rewrote history")
			}
			// Only the Russian legacy content is searchable; auxiliary translations are removed.
			out.Reset()
			if err := runSearch([]string{"-in", "all", "-json", "проксирует"}, &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), legacy) {
				t.Errorf("legacy translation lost from index: %s", out.String())
			}
		})
	}
}

func TestEnglishDefaultRunMaintenanceTerminatesWithoutTranslations(t *testing.T) {
	for _, mode := range []string{"all", "automatic", "background"} {
		t.Run(mode, func(t *testing.T) {
			dir := env(t)
			_, duplicate, kept, _ := dreamBase(t)
			compound, _ := splitBase(t)
			fakeSplit(t, splitByText)

			acted := false
			fakeDream(t, func(string) []extract.Action {
				if acted {
					return nil
				}
				acted = true
				return []extract.Action{
					{Op: "retire", ID: duplicate, By: kept, Kind: "duplicate"},
					{Op: "correct", ID: kept, Content: "The nightly-sync.yml workflow subscribes bot to non-fork repositories."},
				}
			})
			translations := fakeTranslate(t)
			calls := fakeDreamRun(t, nil)
			args := []string{"-run", "-j", "1"}
			if mode == "all" {
				args = append(args, "-all")
			} else {
				state, err := stateDir()
				if err != nil {
					t.Fatal(err)
				}
				if err := markDream(state, time.Now().Add(-dreamEvery-time.Hour)); err != nil {
					t.Fatal(err)
				}
				if mode == "background" {
					args = append(args, "-background")
				} else {
					withAPI(t, "https://api.example.test")
				}
			}
			var out bytes.Buffer
			if err := runDream(args, &out); err != nil {
				t.Fatal(err)
			}
			if *calls == 0 {
				t.Fatal("remaining maintenance made no calls")
			}
			if mode != "all" {
				if err := runDream([]string{"-run", "-all", "-j", "1"}, &out); err != nil {
					t.Fatal(err)
				}
			}
			if len(*translations) != 0 {
				t.Errorf("translation requested for %q", *translations)
			}
			if !strings.Contains(out.String(), "dream -all: done after") || strings.Contains(out.String(), "lack a translation") || strings.Contains(out.String(), "translated") {
				t.Errorf("did not finish normally: %s", out.String())
			}
			if storedFact(t, compound).Status != store.Retired || storedFact(t, duplicate).Status != store.Retired {
				t.Error("split/duplicate maintenance failed")
			}
			if f := storedFact(t, kept); !strings.HasPrefix(f.Content, "The nightly") {
				t.Errorf("corrected fact not expanded: %+v", f)
			}
			for _, r := range readDreamLog(t, dir) {
				if r.Translated != 0 {
					t.Errorf("translated %d facts", r.Translated)
				}
				for _, c := range r.Calls {
					if c.Task == "translate" {
						t.Error("translation model call logged")
					}
				}
			}
		})
	}
}

func TestEnglishDefaultCLIStillAcceptsNonEnglishFacts(t *testing.T) {
	env(t)
	var out bytes.Buffer
	if err := runAdd([]string{"--", "Сервис слушает порт 5432."}, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.Fields(out.String())[0]
	if f := storedFact(t, id); f.Content != "Сервис слушает порт 5432." {
		t.Errorf("add translated content: %s", f.Content)
	}
	if err := runCorrect([]string{id, "Сервис слушает порт 5433."}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	h := factHistory(t, id)
	if len(h) != 2 || h[0].Content != "Сервис слушает порт 5432." || h[1].Content != "Сервис слушает порт 5433." {
		t.Errorf("CLI history: %+v", h)
	}
}

// Keep a JSON-level assertion on packets; an omitted section must not hide queued translation work.
func TestEnglishDefaultSavedPacketHasNoTranslationWork(t *testing.T) {
	env(t)
	translateBase(t)
	noSweep(t)
	addFact(t, "The synthetic service uses socket activation.")
	var out bytes.Buffer
	if err := runDream([]string{"-prepare", "-all"}, &out); err != nil {
		t.Fatal(err)
	}
	dir, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, packetFile))
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if len(p["translate"]) != 0 {
		t.Errorf("saved translation work: %s", p["translate"])
	}
}

func TestEnglishDefaultCJKHintStartsWithEnglish(t *testing.T) {
	env(t)
	addFact(t, "The synthetic service supports Unicode.")
	var out bytes.Buffer
	if err := runSearch([]string{"-json", "中文"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "search first in English") {
		t.Errorf("hint: %s", out.String())
	}
}

func TestEnglishDefaultJoinedAnswerRejectsLegacyTranslationField(t *testing.T) {
	env(t)
	old := dreamRunFn
	dreamRunFn = func(context.Context, string) (extract.DreamReply, error) {
		return extract.DreamReply{Answer: []byte(`{"packet":"legacy-reply","translations":[{"id":"synthetic","translation":"unsolicited"}]}`)}, nil
	}
	t.Cleanup(func() { dreamRunFn = old })
	r, recs, err := askAll(context.Background(), []dreamCall{{text: "synthetic packet", rec: dreamCallRecord{Task: taskReview}}})
	if err == nil {
		t.Fatal("legacy translation reply was silently normalized instead of refused")
	}
	if len(r.Answer) != 0 || len(recs) != 1 || recs[0].Error == "" {
		t.Fatalf("refused reply produced an applicable answer or lost failure evidence: %s %+v", r.Answer, recs)
	}
	if recs[0].Proposed != 0 {
		t.Errorf("legacy translation counted as work: %+v", recs[0])
	}
}
