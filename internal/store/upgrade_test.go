package store

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

// schema5Base writes a base as memcue 0.20.0 left it: schema 5, the rows given, and an index without the facts whose
// latest version is a candidate, stamped as checked. A memcue that trusted the stamp would never find those facts.
func schema5Base(t *testing.T, rows ...Fact) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "facts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	legacyRows(t, s, rows...)
	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	const candidates = `SELECT f.id FROM facts f WHERE f.version = (SELECT max(version) FROM facts WHERE id = f.id)
		AND f.status = 'active' AND f.review = 'candidate'`
	for _, q := range []string{
		`INSERT INTO fact_fts(fact_fts, rowid, stems) SELECT 'delete', rid, stems FROM fact_index WHERE id IN (` +
			candidates + `)`,
		`DELETE FROM fact_index WHERE id IN (` + candidates + `)`,
		`PRAGMA user_version = 5`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	stamp, err := indexStamp(s.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveStamp(s.db, "index", stamp); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	return path
}

// Opening a schema-5 base makes every live fact searchable, candidates included, and writes no fact row: a candidate
// keeps its row and labels, a retired one stays retired, an expired one stays out of what honours expiry, and every
// version stays in the history (#514).
func TestUpgradeFromSchema5(t *testing.T) {
	today := TodayAt(time.Now())
	future := TodayAt(time.Now().AddDate(0, 1, 0))
	past := TodayAt(time.Now().AddDate(0, -1, 0))
	cand := Meta{Kind: "environment", Scope: "host:db1", Source: "tool", Evidence: "ss -ltn", Review: Candidate}
	rule := Meta{Kind: "rule", Scope: "global", Source: "user", Review: Candidate}
	row := func(id string, v int, content, status, expires string, m Meta) Fact {
		return Fact{ID: id, Version: v, Content: content, Keywords: []string{"zebra"}, Origin: "hermes 2026-10-20",
			Author: "old", Status: status, Expires: expires, Meta: m}
	}
	const (
		approvedID    = "01JUPGRADE0000000000000001"
		pendingID     = "01JUPGRADE0000000000000002"
		correctedID   = "01JUPGRADE0000000000000003"
		rejectedID    = "01JUPGRADE0000000000000004"
		expiredID     = "01JUPGRADE0000000000000005"
		retiredID     = "01JUPGRADE0000000000000006"
		wasApprovedID = "01JUPGRADE0000000000000007"
		provisionalID = "01JUPGRADE0000000000000008"
	)
	rows := []Fact{
		row(approvedID, 1, "Approved zebra fact alpha.", Active, "", Meta{Kind: "environment", Source: "tool"}),
		row(pendingID, 1, "Pending zebra candidate bravo.", Active, future, cand),
		row(correctedID, 1, "Corrected zebra candidate charlie obsolete.", Active, "", cand),
		row(correctedID, 2, "Corrected zebra candidate charlie renewed.", Active, "", cand),
		row(rejectedID, 1, "Rejected zebra candidate delta.", Active, "", cand),
		row(rejectedID, 2, "Rejected zebra candidate delta.", Retired, "", cand),
		row(expiredID, 1, "Expired zebra candidate echo rule.", Active, past, rule),
		row(retiredID, 1, "Retired zebra fact foxtrot.", Active, "", Meta{}),
		row(retiredID, 2, "Retired zebra fact foxtrot.", Retired, "", Meta{}),
		row(wasApprovedID, 1, "Approved zebra candidate golf.", Active, "", cand),
		row(wasApprovedID, 2, "Approved zebra candidate golf.", Active, "", Meta{Kind: "environment", Scope: "host:db1",
			Source: "tool", Evidence: "ss -ltn"}),
		row(provisionalID, 1, "Provisional zebra fact hotel.", Active, future, Meta{Source: "tool", Review: Provisional}),
	}
	path := schema5Base(t, rows...)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema %d, want %d (%v)", version, schemaVersion, err)
	}
	after, err := s.Rows()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(rows) {
		t.Fatalf("the upgrade wrote fact rows: %d, want %d", len(after), len(rows))
	}
	for i, f := range after {
		want := rows[i]
		want.Created = time.Date(2026, 10, 20, 12, 0, want.Version, 0, time.UTC)
		if !f.Created.Equal(want.Created) {
			t.Errorf("row %s v%d created %v", f.ID, f.Version, f.Created)
		}
		f.Created = want.Created
		if !reflect.DeepEqual(f, want) {
			t.Errorf("row changed:\n got %+v\nwant %+v", f, want)
		}
	}

	found := ids(t, s, "zebra")
	for _, id := range []string{approvedID, pendingID, correctedID, expiredID, wasApprovedID, provisionalID} {
		if _, ok := found[id]; !ok {
			t.Errorf("%s is not found", id)
		}
	}
	for _, id := range []string{rejectedID, retiredID} {
		if _, ok := found[id]; ok {
			t.Errorf("retired %s is found", id)
		}
	}
	if h := found[correctedID]; h.Version != 2 {
		t.Errorf("the corrected candidate is found at %+v, want version 2", h)
	}
	if _, ok := ids(t, s, "obsolete")[correctedID]; ok {
		t.Error("the corrected candidate is found by the words of its first version")
	}
	if h := found[pendingID]; h.Meta != cand || h.Expires != future || h.Origin != "hermes 2026-10-20" {
		t.Errorf("pending candidate %+v", h)
	}

	current, err := s.SearchFiltered([]string{"zebra"}, 50, Filter{Today: today})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(current, func(h Hit) bool { return h.ID == expiredID }) {
		t.Error("an expired candidate is found by a search that honours expiry")
	}
	card, _, err := s.CardFacts(nil, today)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(card, func(f Fact) bool { return f.ID == expiredID }) {
		t.Error("an expired candidate rule is on the card")
	}

	active, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	var activeIDs []string
	for _, f := range active {
		activeIDs = append(activeIDs, f.ID)
	}
	if want := []string{approvedID, pendingID, correctedID, expiredID, wasApprovedID, provisionalID}; !slices.Equal(activeIDs, want) {
		t.Errorf("active %v, want %v", activeIDs, want)
	}
	for _, id := range []string{rejectedID, retiredID} {
		if f, err := s.Get(id); err != nil || f.Status != Retired || f.Version != 2 {
			t.Errorf("%s: %+v %v", id, f, err)
		}
	}
	if h, err := s.History(correctedID); err != nil || len(h) != 2 || h[0].Content != rows[2].Content {
		t.Errorf("history %+v %v", h, err)
	}
	st, err := s.IndexStats()
	if err != nil || st.Live != 6 || st.Rows != 6 {
		t.Errorf("index stats %+v %v", st, err)
	}

	// A second open has nothing left to do.
	_ = s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Rows()
	if err != nil || !reflect.DeepEqual(again, after) {
		t.Errorf("a second open changed the rows: %v", err)
	}
}

// A new base starts at the current schema, and what is added there is found at once.
func TestCleanInstallFindsAFactAtOnce(t *testing.T) {
	s, _ := open(t)
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema %d (%v)", version, err)
	}
	f, err := s.AddWith("Порт webforge на builder 3000.", []string{"webforge"}, "x 2026-10-27", "", Meta{Kind: "environment",
		Source: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids(t, s, "порт webforge")[f.ID]; !ok {
		t.Error("a new fact is not found at once")
	}
}
