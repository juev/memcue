package store

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/juev/memcue/internal/hlc"
)

const devA, devB = "00000000000000aa", "00000000000000bb"

func stamp(ms int64, dev string) string { return hlc.Stamp{Millis: ms, Device: dev}.String() }

var created = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func createChange(id, content string, at string) Change {
	return Change{OpID: "op-" + at, ID: id, Create: true, Created: created, Source: "agent", Stamp: at,
		Values: map[string]string{RegContent: content, RegKind: "pitfall", RegScope: "repo:x", RegExpires: "",
			RegStatus: Active, RegOrigin: "x 2026-11-10"}}
}

// permutations calls f with every order of the indexes 0..n-1.
func permutations(n int, f func([]int)) {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	var rec func(int)
	rec = func(k int) {
		if k == n {
			f(append([]int(nil), idx...))
			return
		}
		for i := k; i < n; i++ {
			idx[k], idx[i] = idx[i], idx[k]
			rec(k + 1)
			idx[k], idx[i] = idx[i], idx[k]
		}
	}
	rec(0)
}

func TestAnyOrderOfChangesGivesTheSameRegisters(t *testing.T) {
	base := stamp(100, devA)
	changes := []Change{
		{OpID: "1", ID: "F", Stamp: stamp(200, devA), Base: base, Values: map[string]string{RegContent: "from A"}},
		{OpID: "2", ID: "F", Stamp: stamp(210, devB), Base: base, Values: map[string]string{RegScope: "global"}},
		{OpID: "3", ID: "F", Stamp: stamp(205, devB), Base: base, Values: map[string]string{RegContent: "from B"}},
		{OpID: "4", ID: "F", Stamp: stamp(220, devA), Values: map[string]string{RegStatus: Retired}},
		{OpID: "5", ID: "F", Stamp: stamp(150, devB), Values: map[string]string{RegKind: "rule", RegExpires: "2027-01-01"}},
		createChange("F", "original", base), // a replayed create must not undo anything
	}
	var want *Row
	var wantLosers []string
	permutations(len(changes), func(order []int) {
		var row Row
		if _, err := ApplyChange(&row, false, createChange("F", "original", base)); err != nil {
			t.Fatal(err)
		}
		var losers []string
		for _, i := range order {
			m, err := ApplyChange(&row, true, changes[i])
			if err != nil {
				t.Fatalf("order %v, change %d: %v", order, i, err)
			}
			if m.HasLoser {
				losers = append(losers, m.Loser)
			}
		}
		sort.Strings(losers)
		row.Version, row.ContentVersion = 0, 0 // both count the changes applied, so they follow the order
		if want == nil {
			want, wantLosers = &row, losers
			return
		}
		if !reflect.DeepEqual(row, *want) {
			t.Fatalf("order %v: %+v\nwant %+v", order, row, *want)
		}
		if !reflect.DeepEqual(losers, wantLosers) {
			t.Fatalf("order %v: losers %q, want %q", order, losers, wantLosers)
		}
	})
	if want.Content != "from B" || want.Scope != "global" || want.Status != Retired || want.Kind != "rule" ||
		want.Expires != "2027-01-01" {
		t.Fatalf("unexpected winner: %+v", want.Fact)
	}
	if !reflect.DeepEqual(wantLosers, []string{"from A"}) {
		t.Fatalf("losers %q, want the content of A, which lost to the later B", wantLosers)
	}
}

func TestConcurrentContentKeepsTheLoser(t *testing.T) {
	base := stamp(100, devA)
	var row Row
	mustApply(t, &row, false, createChange("F", "original", base))
	a := Change{OpID: "a", ID: "F", Stamp: stamp(300, devA), Base: base, Values: map[string]string{RegContent: "A"}}
	b := Change{OpID: "b", ID: "F", Stamp: stamp(200, devB), Base: base, Values: map[string]string{RegContent: "B"}}
	if m := mustApply(t, &row, true, a); m.HasLoser {
		t.Fatalf("the first correction from the stored content is sequential: %+v", m)
	}
	m, err := ApplyChange(&row, true, b)
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasLoser || m.Loser != "B" || row.Content != "A" || m.Changed {
		t.Fatalf("B is older and concurrent: it loses and is kept; got %+v, row %q", m, row.Content)
	}
}

func TestSequentialCorrectionsKeepNoLoser(t *testing.T) {
	s1 := stamp(100, devA)
	var row Row
	mustApply(t, &row, false, createChange("F", "v1", s1))
	s2 := stamp(200, devB)
	if m := mustApply(t, &row, true, Change{OpID: "2", ID: "F", Stamp: s2, Base: s1,
		Values: map[string]string{RegContent: "v2"}}); m.HasLoser {
		t.Fatal("v2 was made from v1")
	}
	if m := mustApply(t, &row, true, Change{OpID: "3", ID: "F", Stamp: stamp(300, devA), Base: s2,
		Values: map[string]string{RegContent: "v3"}}); m.HasLoser || row.Content != "v3" || row.Version != 3 {
		t.Fatalf("v3 was made from v2: %+v, row %+v", m, row)
	}
}

func TestRetireBeatsAnEarlierCorrectionAndKeepsIt(t *testing.T) {
	base := stamp(100, devA)
	var row Row
	mustApply(t, &row, false, createChange("F", "old", base))
	retire := Change{OpID: "r", ID: "F", Stamp: stamp(300, devA), Values: map[string]string{RegStatus: Retired}}
	correct := Change{OpID: "c", ID: "F", Stamp: stamp(200, devB), Base: base, Values: map[string]string{RegContent: "new"}}
	mustApply(t, &row, true, retire)
	if m := mustApply(t, &row, true, correct); m.HasLoser {
		t.Fatal("a correction beside a retire is not a content conflict")
	}
	if row.Status != Retired || row.Content != "new" {
		t.Fatalf("got %+v", row.Fact)
	}
	reopen := Change{OpID: "o", ID: "F", Stamp: stamp(400, devB), Values: map[string]string{RegStatus: Active}}
	mustApply(t, &row, true, reopen)
	if row.Status != Active || row.Content != "new" {
		t.Fatalf("reopen must bring back the corrected text: %+v", row.Fact)
	}
}

func TestCreateOfAHeldIDIsRefused(t *testing.T) {
	var row Row
	mustApply(t, &row, false, createChange("F", "one", stamp(100, devA)))
	other := createChange("F", "two", stamp(200, devB))
	other.Created = created.Add(time.Second)
	if _, err := ApplyChange(&row, true, other); !errors.Is(err, ErrIDTaken) {
		t.Fatalf("error %v, want ErrIDTaken", err)
	}
	if row.Content != "one" {
		t.Fatal("a refused create must change nothing")
	}
}

func TestChangeOfAnUnknownFact(t *testing.T) {
	var row Row
	_, err := ApplyChange(&row, false, Change{OpID: "1", ID: "F", Stamp: stamp(1, devA),
		Values: map[string]string{RegStatus: Retired}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error %v", err)
	}
}

func TestMalformedChanges(t *testing.T) {
	ok := Change{OpID: "1", ID: "F", Stamp: stamp(1, devA), Values: map[string]string{RegKind: "rule"}}
	bad := map[string]func(*Change){
		"no stamp":     func(c *Change) { c.Stamp = "" },
		"bad stamp":    func(c *Change) { c.Stamp = "x" },
		"bad base":     func(c *Change) { c.Base = "x" },
		"no values":    func(c *Change) { c.Values = nil },
		"unknown":      func(c *Change) { c.Values = map[string]string{"author": "x"} },
		"status":       func(c *Change) { c.Values = map[string]string{RegStatus: "gone"} },
		"empty text":   func(c *Change) { c.Values = map[string]string{RegContent: ""} },
		"partial make": func(c *Change) { c.Create, c.Created = true, created },
		"no time":      func(c *Change) { *c = createChange("F", "x", stamp(1, devA)); c.Created = time.Time{} },
	}
	for name, spoil := range bad {
		c := ok
		c.Values = map[string]string{RegKind: "rule"}
		spoil(&c)
		var row Row
		if _, err := ApplyChange(&row, false, c); !errors.Is(err, ErrChange) {
			t.Errorf("%s: error %v, want ErrChange", name, err)
		}
	}
}

func mustApply(t *testing.T, row *Row, exists bool, ch Change) Merged {
	t.Helper()
	m, err := ApplyChange(row, exists, ch)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDiff(t *testing.T) {
	a := Fact{Content: "x", Status: Active, Meta: Meta{Kind: "rule", Evidence: "e"}}
	b := a
	b.Content, b.Kind, b.Evidence = "y", "pitfall", "other"
	if got := Diff(a, b); !reflect.DeepEqual(got, map[string]string{RegContent: "y", RegKind: "pitfall"}) {
		t.Fatalf("Diff = %v; evidence is not a register", got)
	}
}
