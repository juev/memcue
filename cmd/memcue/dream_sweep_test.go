package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
)

// nothingFresh makes every fact of the base one the dream has seen: only the merge sweep can group them.
func nothingFresh() []string {
	return []string{"-since", time.Now().Add(time.Hour).Format(time.RFC3339)}
}

// findGroup is the record of the group holding both facts, if the dream grouped them.
func findGroup(rec dreamRecord, a, b string) (dreamGroupRecord, bool) {
	for _, g := range rec.Grouped {
		if slices.Contains(g.Facts, a) && slices.Contains(g.Facts, b) {
			return g, true
		}
	}
	return dreamGroupRecord{}, false
}

func readCursor(t *testing.T, dir string) string {
	t.Helper()
	c, err := readMergeCursor(filepath.Join(dir, "state", "memcue"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A duplicate pair both of whose copies predate the dream is grouped by the sweep, reaches the model unmarked as new,
// and the larger id is retired in favour of the smaller; the record names the pair and what came of it.
func TestDreamSweepRetiresAnOldDuplicate(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	calls := fakeDream(t, func(groups string) []extract.Action {
		if !strings.Contains(groups, a) || !strings.Contains(groups, b) {
			return nil
		}
		return []extract.Action{{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "covered by " + b}}
	})
	if err := dreamModel(nothingFresh(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || strings.Contains((*calls)[0], ", новый") {
		t.Fatalf("calls %q", *calls)
	}
	if f := vetStatus(t, a); f.Status != store.Retired {
		t.Errorf("larger id not retired: %+v", f)
	}
	if f := vetStatus(t, b); f.Status != store.Active {
		t.Errorf("smaller id not kept: %+v", f)
	}
	recs := readDreamLog(t, dir)
	rec := recs[len(recs)-1]
	g, ok := findGroup(rec, a, b)
	if !ok || g.Outcome != "acted" || !slices.Equal(g.Acted, []string{a}) || len(g.Fresh) != 0 {
		t.Errorf("record %+v", rec)
	}
	if rec.Swept == 0 || rec.Fresh != 0 || rec.Groups != 1 {
		t.Errorf("record %+v", rec)
	}
}

// The cursor in dream.merge walks the facts the dream has seen, mergeSweep a dream, and starts over after the newest.
func TestDreamSweepCursorAdvancesAndWraps(t *testing.T) {
	dir := env(t)
	old, a, b, other := dreamBase(t)
	ids := []string{old, a, b, other}
	slices.Sort(ids)
	prev := mergeSweep
	mergeSweep = 3
	t.Cleanup(func() { mergeSweep = prev })
	fakeDream(t, func(string) []extract.Action { return nil })

	// A packet that was never applied moves nothing.
	if err := runDream(append([]string{"-prepare"}, nothingFresh()...), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if c := readCursor(t, dir); c != "" {
		t.Fatalf("prepare alone moved the cursor to %s", c)
	}
	for i, want := range []string{ids[2], ids[1], ids[0]} {
		if err := dreamModel(nothingFresh(), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		recs := readDreamLog(t, dir)
		if c := readCursor(t, dir); c != want || recs[len(recs)-1].Swept != 3 {
			t.Fatalf("dream %d: cursor %s swept %d, want %s and 3", i+1, c, recs[len(recs)-1].Swept, want)
		}
	}
}

func TestSweepFactsStartAfterTheCursor(t *testing.T) {
	active := []store.Fact{{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "D"}}
	fresh := map[string]bool{"C": true}
	for cursor, want := range map[string]string{"": "ABD", "A": "BDA", "B": "DAB", "C": "DAB", "D": "ABD", "Z": "ABD"} {
		var got string
		for _, f := range sweepFacts(active, fresh, cursor) {
			got += f.ID
		}
		if got != want {
			t.Errorf("cursor %q: %s, want %s", cursor, got, want)
		}
	}
}

// A dream adds at most mergeGroups sweep groups, and none when fresh groups fill the packet.
func TestDreamSweepIsBounded(t *testing.T) {
	dir := env(t)
	pg, a, b, _ := dreamBase(t)
	pg2 := addFact(t, "Postgres демо-базы на db1 держит порт 5432.", "postgres", "порт")
	prev := mergeGroups
	mergeGroups = 1
	t.Cleanup(func() { mergeGroups = prev })
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	for _, pair := range [][2]string{{pg, pg2}, {b, a}} {
		*calls = nil
		if err := dreamModel(nothingFresh(), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || strings.Contains((*calls)[0], "## Группа 2") ||
			!strings.Contains((*calls)[0], pair[0]) || !strings.Contains((*calls)[0], pair[1]) {
			t.Fatalf("want the one group %v, sent %q", pair, *calls)
		}
		recs := readDreamLog(t, dir)
		if g, ok := findGroup(recs[len(recs)-1], pair[0], pair[1]); !ok || g.Outcome != "kept" {
			t.Errorf("record %+v", recs[len(recs)-1])
		}
	}

	oldGroups := packetGroups
	packetGroups = 1
	t.Cleanup(func() { packetGroups = oldGroups })
	pg3 := addFact(t, "Postgres демо-базы на db1 слушает порт 5432 для всех клиентов.", "postgres", "порт")
	*calls = nil
	if err := dreamModel([]string{"-since", time.Now().Add(-time.Second).Format(time.RFC3339Nano)}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], pg3) || strings.Contains((*calls)[0], a) {
		t.Fatalf("the fresh group must fill the packet alone: %q", *calls)
	}
	if recs := readDreamLog(t, dir); recs[len(recs)-1].Swept != 0 {
		t.Errorf("swept with no room: %+v", recs[len(recs)-1])
	}
}

// Two facts on different attributes of one job are grouped by their shared words; the model leaves such a group
// alone, as its instructions say, and the dream changes nothing but records the group as kept.
func TestDreamGroupOfDifferentAttributesChangesNothing(t *testing.T) {
	dir := env(t)
	id := addFact(t, "Cron-задача backup-db на хосте host1 имеет job_id 7f3a9c.", "cron", "backup-db", "job_id")
	when := addFact(t, "Cron-задача backup-db на хосте host1 запускается ежедневно в 03:00.", "cron", "backup-db", "расписание")
	calls := fakeDream(t, func(string) []extract.Action { return nil })
	if err := dreamModel(nothingFresh(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls %q", *calls)
	}
	for _, f := range []string{id, when} {
		if got := vetStatus(t, f); got.Status != store.Active || got.Version != 1 {
			t.Errorf("fact changed: %+v", got)
		}
	}
	recs := readDreamLog(t, dir)
	rec := recs[len(recs)-1]
	if g, ok := findGroup(rec, id, when); !ok || g.Outcome != "kept" || len(rec.Actions) != 0 {
		t.Errorf("record %+v", rec)
	}
}

// A dream with nothing to merge still writes its record: no groups, no actions.
func TestDreamWithNothingToMergeIsRecorded(t *testing.T) {
	dir := env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"Postgres демо-базы слушает порт 5432 на db1.", "Gradle в demo-app требует JAVA_HOME=/opt/jdk-21."} {
		f, err := s.Add(c, nil, "x 2026-10-19")
		if err != nil {
			t.Fatal(err)
		}
		// queries and a translation: nothing is owed to the dream either (#396)
		if _, err := s.SetQueries(f.ID, f.Version, []string{"query", extract.TranslationPrefix + "translation"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	var out bytes.Buffer
	if err := runDream(append([]string{"-prepare"}, nothingFresh()...), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing for the dream") {
		t.Fatalf("out %q", out.String())
	}
	recs := readDreamLog(t, dir)
	if len(recs) != 1 || recs[0].Groups != 0 || len(recs[0].Actions) != 0 || recs[0].Swept != 2 {
		t.Errorf("record %+v", recs)
	}
}

// -list and -prepare show sweep groups without marking their facts new and change nothing; -apply refuses a sweep
// group whose fact changed since the packet, as it does a fresh one.
func TestDreamSweepGroupDryRunAndChangedGroup(t *testing.T) {
	dir := env(t)
	_, a, b, _ := dreamBase(t)
	var list bytes.Buffer
	if err := runDream(append([]string{"-list"}, nothingFresh()...), &list); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.String(), a) || !strings.Contains(list.String(), b) || strings.Contains(list.String(), ", новый") {
		t.Fatalf("list %q", list.String())
	}
	fakeDream(t, func(groups string) []extract.Action {
		return []extract.Action{{Op: "retire", ID: a, By: b, Kind: "duplicate", Reason: "covered by " + b}}
	})
	path, err := prepareAnswer(nothingFresh(), &bytes.Buffer{})
	if err != nil || path == "" {
		t.Fatalf("prepare: %q %v", path, err)
	}
	if f := vetStatus(t, a); f.Status != store.Active || f.Version != 1 {
		t.Fatalf("prepare changed a fact: %+v", f)
	}
	if c := readCursor(t, dir); c != "" {
		t.Fatalf("prepare moved the cursor to %s", c)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(b, "Workflow nightly-sync.yml подписывает bot на репозитории example/nightly-sync.", nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	var out bytes.Buffer
	if err := runDream([]string{"-apply", path}, &out); err != nil {
		t.Fatal(err)
	}
	if f := vetStatus(t, a); f.Status != store.Active {
		t.Errorf("retired against a changed group: %+v", f)
	}
	recs := readDreamLog(t, dir)
	rec := recs[len(recs)-1]
	if g, ok := findGroup(rec, a, b); !ok || g.Outcome != "refused" || len(rec.Refused) == 0 {
		t.Errorf("record %+v", rec)
	}
}

// tokenBase stores a short note of the command that issues a token and, later, a longer fact that restates it with
// the host and the full command, plus facts on other sides of the same tool.
func tokenBase(t *testing.T, s *store.Store) (short, long, password, data store.Fact) {
	t.Helper()
	add := func(c string, k ...string) store.Fact {
		f, err := s.Add(c, k, "x 2026-10-19")
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
		return f
	}
	short = add("Токен Forgejo: docker exec -u git forgejo admin user generate-access-token -u <user> -t <name> --scopes ...",
		"forgejo", "token")
	data = add("Forgejo на git.example работает в контейнере docker, данные лежат в /var/lib/gitea.", "forgejo", "docker")
	password = add("Пароль пользователя Forgejo меняется командой docker exec -u git forgejo admin user change-password -u <user>.",
		"forgejo", "пароль")
	long = add("Токен Forgejo на хосте git.example выпускается командой ssh git.example 'docker exec -u git -e GITEA_WORK_DIR=/var/lib/gitea forgejo admin user generate-access-token --username bot --token-name ci --scopes write:repository', токен печатается один раз.",
		"forgejo", "токен", "generate-access-token")
	return
}

// A fresh fact that restates an older, shorter one in other words is grouped with it, though the short one holds
// too few of the long one's terms for the coverage gate alone.
func TestDreamGroupsARestatement(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	short, long, _, _ := tokenBase(t, s)
	active, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	p := dreamNeighbours
	p.Seen = map[string]bool{long.ID: true}
	hits, err := recall.Recall(s, long.Content+" "+strings.Join(long.Keywords, " "), p)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(hits, func(h recall.Hit) bool { return h.ID == short.ID }) {
		t.Fatal("the coverage gate alone finds the short copy: the base no longer shows the case")
	}
	groups, err := groupFacts(s, active, []store.Fact{long})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || !slices.ContainsFunc(groups[0], func(f store.Fact) bool { return f.ID == short.ID }) {
		t.Fatalf("restatement not grouped: %+v", groups)
	}
}

// Facts on other sides of the same tool share its words but each holds its own detail: they are not grouped with
// the token fact.
func TestDreamDoesNotGroupDifferentDetails(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	_, long, password, data := tokenBase(t, s)
	active, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	groups, err := groupFacts(s, active, []store.Fact{long})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		for _, f := range g {
			if f.ID == password.ID || f.ID == data.ID {
				t.Errorf("a fact of another detail grouped with the token fact: %+v", g)
			}
		}
	}
}

// A group the packet bounds leave out is in the record as deferred, with its fresh facts.
func TestDreamRecordsADeferredGroup(t *testing.T) {
	dir := env(t)
	pg, a, b, _ := dreamBase(t)
	pg2 := addFact(t, "Postgres демо-базы на db1 держит порт 5432.", "postgres", "порт")
	old := packetGroups
	packetGroups = 1
	t.Cleanup(func() { packetGroups = old })
	fakeDream(t, func(string) []extract.Action { return nil })
	if err := dreamModel([]string{"-all"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	recs := readDreamLog(t, dir)
	rec := recs[len(recs)-1]
	kept, ok1 := findGroup(rec, pg, pg2)
	deferred, ok2 := findGroup(rec, a, b)
	if !ok1 || !ok2 || kept.Outcome != "kept" || deferred.Outcome != "deferred" || len(deferred.Fresh) != 2 {
		t.Errorf("record %+v", rec)
	}
}
