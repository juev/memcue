package store

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/text"
)

// A provisional fact the removed vet left is live like any other (#514): Active lists it, so memcue facts, the card
// and the dream read it, and it keeps its expiry until a person changes it. A correction keeps that expiry and drops
// the state; clearing the expiry makes it a fact like any other, still listed. Expired or retired, it stays out.
func TestLegacyProvisionalIsLive(t *testing.T) {
	today := TodayAt(time.Now())
	future := TodayAt(time.Now().AddDate(0, 1, 0))
	past := TodayAt(time.Now().AddDate(0, -1, 0))
	s, _ := open(t)
	prov := Meta{Kind: "rule", Scope: "global", Source: "tool", Review: Provisional}
	const (
		liveID    = "01JPROVISIONAL000000000001"
		expiredID = "01JPROVISIONAL000000000002"
		retiredID = "01JPROVISIONAL000000000003"
	)
	legacyRows(t, s,
		Fact{ID: liveID, Version: 1, Content: "Provisional kilo rule.", Expires: future, Meta: prov},
		Fact{ID: expiredID, Version: 1, Content: "Provisional lima rule.", Expires: past, Meta: prov},
		Fact{ID: retiredID, Version: 1, Content: "Provisional mike rule.", Expires: future, Meta: prov},
		Fact{ID: retiredID, Version: 2, Content: "Provisional mike rule.", Expires: future, Status: Retired, Meta: prov},
	)
	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	activeIDs := func() []string {
		t.Helper()
		active, err := s.Active()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range active {
			out = append(out, f.ID)
		}
		return out
	}
	if got := activeIDs(); !slices.Equal(got, []string{liveID, expiredID}) {
		t.Errorf("active %v, want the live and the expired provisional fact", got)
	}
	card, _, err := s.CardFacts(nil, today)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(card, func(f Fact) bool { return f.ID == liveID }) {
		t.Error("a live provisional global rule is not on the card")
	}
	if slices.ContainsFunc(card, func(f Fact) bool { return f.ID == expiredID || f.ID == retiredID }) {
		t.Errorf("an expired or retired provisional rule is on the card: %+v", card)
	}

	f, err := s.Correct(liveID, "Provisional kilo rule, corrected.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.Review != "" || f.Expires != future {
		t.Errorf("a corrected provisional fact %+v, want approved with its expiry %s", f, future)
	}
	if got, _ := s.Get(liveID); got.Review != "" || got.Expires != future {
		t.Errorf("stored %+v", got)
	}
	never := ""
	if f, err = s.CorrectExpiring(liveID, "Provisional kilo rule, kept.", nil, &never); err != nil {
		t.Fatal(err)
	}
	if f.Review != "" || f.Expires != "" {
		t.Errorf("after -no-expires %+v", f)
	}
	if got := activeIDs(); !slices.Contains(got, liveID) {
		t.Errorf("a provisional fact made permanent is not listed: %v", got)
	}
	if _, ok := ids(t, s, "kilo kept")[liveID]; !ok {
		t.Error("a provisional fact made permanent is not found")
	}
	if _, err := s.Correct(retiredID, "Provisional mike rule, back.", nil); err == nil {
		t.Error("a retired provisional fact was corrected")
	}
}

// Two machines change the same fact apart: one retires it, the other corrects it. The merge keeps both versions and
// the later one by creation time is current, as for any two changes made apart, whatever the review state the fact
// had (#514 does not change the merge). A correction made after the retire therefore brings the fact back; one made
// before it does not. Both sides get the same base, the index follows the current version, and merging a side again
// changes nothing.
func TestMergeOfARetireAndACorrectionKeepsTheLatest(t *testing.T) {
	for _, meta := range []struct {
		name string
		meta Meta
	}{
		{"approved", Meta{Kind: "environment"}},
		{"legacy candidate", Meta{Kind: "environment", Review: Candidate}},
		{"legacy provisional", Meta{Kind: "environment", Review: Provisional}},
	} {
		for _, order := range []struct {
			name             string
			retire, correct  time.Duration
			status           string
			currentIsCorrect bool
		}{
			{"retire later", time.Hour, time.Minute, Retired, false},
			{"correction later", time.Minute, time.Hour, Active, true},
		} {
			t.Run(meta.name+"/"+order.name, func(t *testing.T) {
				b := filepath.Join(t.TempDir(), "facts.db")
				v1 := Fact{ID: "01JMERGERETIRE000000000001", Version: 1, Content: "Retire zulu on db1.",
					Keywords: []string{"zulu"}, Origin: "x 2026-10-19", Author: "mac", Created: t0, Meta: meta.meta}
				s0 := openAt(t, b, "mac", t0)
				legacyRows(t, s0, v1)
				if err := s0.Rebuild(); err != nil {
					t.Fatal(err)
				}
				_ = s0.Close()
				retiring, correcting := clone(t, b), clone(t, b)
				r := openAt(t, retiring, "mac", t0.Add(order.retire))
				if _, err := r.Retire(v1.ID); err != nil {
					t.Fatal(err)
				}
				_ = r.Close()
				c := openAt(t, correcting, "builder", t0.Add(order.correct))
				if _, err := c.Correct(v1.ID, "Retire zulu on db1, corrected apart.", nil); err != nil {
					t.Fatal(err)
				}
				_ = c.Close()

				a, z := clone(t, retiring), clone(t, correcting)
				sa := openAt(t, a, "mac", t0.Add(2*time.Hour))
				defer func() { _ = sa.Close() }()
				if _, err := sa.Merge(correcting); err != nil {
					t.Fatal(err)
				}
				sz := openAt(t, z, "builder", t0.Add(2*time.Hour))
				defer func() { _ = sz.Close() }()
				if _, err := sz.Merge(retiring); err != nil {
					t.Fatal(err)
				}
				merged := dump(t, sa)
				if dz := dump(t, sz); merged != dz {
					t.Errorf("merge depends on side:\n%s\nvs\n%s", merged, dz)
				}
				for _, s := range []*Store{sa, sz} {
					got, err := s.Get(v1.ID)
					if err != nil || got.Status != order.status || got.Version != 3 {
						t.Errorf("merged %+v %v, want %s at version 3", got, err, order.status)
					}
					if order.currentIsCorrect && (got.Content != "Retire zulu on db1, corrected apart." || got.Review != "") {
						t.Errorf("current %+v, want the approved correction", got)
					}
					h, _ := s.History(v1.ID)
					if len(h) != 3 || !slices.ContainsFunc(h, func(f Fact) bool { return f.Status == Retired }) ||
						!slices.ContainsFunc(h, func(f Fact) bool { return strings.Contains(f.Content, "corrected apart") }) {
						t.Errorf("history lost a version: %+v", h)
					}
					_, found := ids(t, s, "zulu")[v1.ID]
					active, _ := s.Active()
					listed := slices.ContainsFunc(active, func(f Fact) bool { return f.ID == v1.ID })
					if want := order.status == Active; found != want || listed != want {
						t.Errorf("found %v, listed %v, want %v", found, listed, want)
					}
				}
				// A replay may repeat a version in the history (the merge appends a side whose versions are not a
				// prefix of the merged ones), but the current version is the latest of the same set and stays.
				want, _ := sa.Get(v1.ID)
				for _, side := range []string{retiring, correcting} {
					if _, err := sa.Merge(side); err != nil {
						t.Fatal(err)
					}
					got, _ := sa.Get(v1.ID)
					got.Version, got.Created = want.Version, want.Created
					if !same(got, want) {
						t.Errorf("merging %s again changed the current version: %+v, want %+v", side, got, want)
					}
					if _, found := ids(t, sa, "zulu")[v1.ID]; found != (order.status == Active) {
						t.Errorf("merging %s again: found %v", side, found)
					}
				}
			})
		}
	}
}

// A fact retired by a merge and reopened afterwards stays open when the branch that was merged before comes in
// again: its versions are already common, so the replay adds nothing and cannot undo the reopen.
func TestMergeReplayKeepsALaterReopen(t *testing.T) {
	b, f := base(t)
	corrected := clone(t, b)
	c := openAt(t, corrected, "builder", t0.Add(time.Minute))
	if _, err := c.Correct(f.ID, "Hermes работает на deepseek-pro", nil); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	s := openAt(t, b, "mac", t0.Add(time.Hour))
	defer func() { _ = s.Close() }()
	if _, err := s.Retire(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Merge(corrected); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(f.ID); got.Status != Retired {
		t.Fatalf("after the first merge %+v, want the later retire", got)
	}
	s.now = func() time.Time { return t0.Add(2 * time.Hour) }
	if _, err := s.Restore(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Merge(corrected); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(f.ID)
	// The retire copied the text its machine saw, so the reopen carries that text, not the correction made elsewhere.
	if err != nil || got.Status != Active || got.Version != 4 || got.Content != f.Content {
		t.Errorf("after the replay %+v %v, want the reopen at version 4", got, err)
	}
	if len(ids(t, s, "hermes deepseek")) != 1 {
		t.Error("the reopened fact is not found after the replay")
	}
}

// A reopen on one machine merged with a correction made elsewhere: the latest version decides.
func TestMergeOfAReopenAndACorrectionKeepsTheLatest(t *testing.T) {
	b, f := base(t)
	reopened, corrected := clone(t, b), clone(t, b)
	r := openAt(t, reopened, "mac", t0.Add(time.Minute))
	if _, err := r.Retire(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restore(f.ID); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	c := openAt(t, corrected, "builder", t0.Add(time.Hour))
	if _, err := c.Correct(f.ID, "Hermes работает на deepseek-pro", nil); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	s := openAt(t, reopened, "mac", t0.Add(2*time.Hour))
	defer func() { _ = s.Close() }()
	if _, err := s.Merge(corrected); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(f.ID)
	if err != nil || got.Status != Active || got.Content != "Hermes работает на deepseek-pro" || got.Version != 4 {
		t.Errorf("merged %+v %v", got, err)
	}
}

// olderRebuild does what Rebuild of memcue 0.20.0 does to a base it holds open: the index without the facts whose
// latest version is a candidate, stamped under the name that memcue reads.
func olderRebuild(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{
		`DELETE FROM fact_index`,
		`INSERT INTO fact_fts(fact_fts) VALUES ('delete-all')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query(`SELECT f.id, f.content, f.keywords, f.queries FROM facts f
		WHERE f.version = (SELECT max(version) FROM facts WHERE id = f.id) AND f.status = 'active'
			AND f.review <> 'candidate'`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ id, text string }
	var live []row
	for rows.Next() {
		var id, content, keywords, queries string
		if err := rows.Scan(&id, &content, &keywords, &queries); err != nil {
			t.Fatal(err)
		}
		live = append(live, row{id, content + "\n" + keywords + "\n" + queries})
	}
	_ = rows.Close()
	for _, r := range live {
		stems := strings.Join(text.Stems(r.text), " ")
		res, err := s.db.Exec(`INSERT INTO fact_index(id, stems, numbers) VALUES (?, ?, 0)`, r.id, stems)
		if err != nil {
			t.Fatal(err)
		}
		rid, _ := res.LastInsertId()
		if _, err := s.db.Exec(`INSERT INTO fact_fts(rowid, stems) VALUES (?, ?)`, rid, stems); err != nil {
			t.Fatal(err)
		}
	}
	stamp, err := indexStamp(s.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO stamps(name, value) VALUES ('index', ?)`, stamp); err != nil {
		t.Fatal(err)
	}
}

// A memcue 0.20.0 process that opened the base before this one upgraded it keeps its connection and its rules: its
// rebuild takes the candidates out of the index and stamps the result as checked. The next open of this memcue must
// not trust that stamp; it finds the candidates missing and puts them back (#514).
func TestOpenRepairsTheIndexAnOlderProcessRebuilt(t *testing.T) {
	const id = "01JOLDPROCESS0000000000001"
	path := schema5Base(t, Fact{ID: id, Version: 1, Content: "Held open yankee candidate.", Status: Active,
		Meta: Meta{Kind: "environment", Review: Candidate}})
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids(t, s, "yankee")[id]; !ok {
		t.Fatal("the candidate is not found after the upgrade")
	}
	// The older process writes through its own connection; the SQL is the same on this one.
	olderRebuild(t, s)
	_ = s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, ok := ids(t, s, "yankee")[id]; !ok {
		t.Error("the candidate is lost from search after an older process rebuilt the index")
	}
	if st, err := s.IndexStats(); err != nil || st.Live != st.Rows {
		t.Errorf("index stats %+v %v", st, err)
	}
}
