package store

import "testing"

// tiedTails are what two machines held of one fact on 2026-10-29: both had v1 without a source; the Mac's dream wrote
// queries and 0.25.0 relabelled that, while loffe relabelled v1 itself. Every version after v1 keeps the creation time
// and the author of v1, so creation time, author and content tie (#582).
func tiedTails() (common Fact, mac, loffe []Fact) {
	common = Fact{ID: "01JMERGEORDER0000000000001", Version: 1, Content: "Headscale не раздаёт Control D",
		Created: t0, Status: Active, Author: "denis", Meta: Meta{Kind: "environment"}}
	queried := common
	queried.Version, queried.Queries = 2, []string{"днс", "= Headscale does not push Control D"}
	macLabel := queried
	macLabel.Version, macLabel.Source = 3, "agent"
	loffeLabel := common
	loffeLabel.Version, loffeLabel.Source = 2, "agent"
	return common, []Fact{common, queried, macLabel}, []Fact{common, loffeLabel}
}

// Versions that tie on creation time, author and content merge in one order on every side, the fullest last: the
// current version keeps both the queries and the source.
func TestTiedVersionsMergeInOneOrder(t *testing.T) {
	_, mac, loffe := tiedTails()
	onMac, onLoffe := MergeHistories(mac, loffe), MergeHistories(loffe, mac)
	if !sameHistory(onMac.Versions, onLoffe.Versions) {
		t.Fatalf("the sides disagree:\n%+v\n%+v", onMac.Versions, onLoffe.Versions)
	}
	cur := onMac.Versions[len(onMac.Versions)-1]
	if cur.Source != "agent" || len(cur.Queries) != 2 || len(onMac.Versions) != 4 {
		t.Errorf("current %+v of %d versions, want the relabelled version with queries of 4", cur, len(onMac.Versions))
	}
	if h := MergeHistories(onMac.Versions, onLoffe.Versions); h.Changed() {
		t.Errorf("merging the result again changes it: %+v", h)
	}
}

// Two sides that merged the same versions in different orders, as 0.25.0 did, end with one history: each side takes
// the order of the rule, and a side already in it stays as it is, so a pull no longer rewrites the file every time.
func TestDifferentlyOrderedSidesConverge(t *testing.T) {
	_, mac, loffe := tiedTails()
	right := MergeHistories(mac, loffe).Versions
	wrong := append([]Fact{right[0]}, right[3], right[1], right[2]) // the order loffe's 0.25.0 left
	for i := range wrong {
		wrong[i].Version = i + 1
	}
	h := MergeHistories(wrong, right)
	if !h.Reordered || h.Added != 0 || !sameHistory(h.Versions, right) {
		t.Errorf("the misordered side: %+v", h)
	}
	if h := MergeHistories(right, wrong); h.Changed() {
		t.Errorf("the side in the order of the rule changed: %+v", h)
	}
}
