package extract

import (
	"slices"
	"testing"
)

func TestTranslationQuery(t *testing.T) {
	q := []string{"обновить headscale", "= How to update headscale on the host", "vpn"}
	if tr, ok := Translation(q); !ok || tr != "How to update headscale on the host" {
		t.Errorf("Translation = %q, %v", tr, ok)
	}
	if got := SearchQueries(q); !slices.Equal(got, []string{"обновить headscale", "vpn"}) {
		t.Errorf("SearchQueries = %q", got)
	}
	if _, ok := Translation(SearchQueries(q)); ok {
		t.Error("SearchQueries kept the translation")
	}
	// A new translation replaces the old one, and its commas go the way CleanQueries sends them.
	got := WithTranslation(q, "  Обновлять headscale, затем  перезапускать ")
	want := []string{"обновить headscale", "vpn", "= Обновлять headscale затем перезапускать"}
	if !slices.Equal(got, want) {
		t.Errorf("WithTranslation = %q, want %q", got, want)
	}
	if got := WithTranslation(q, " "); !slices.Equal(got, []string{"обновить headscale", "vpn"}) {
		t.Errorf("WithTranslation(empty) = %q, want the search queries alone", got)
	}
	// CleanQueries keeps the mark: the store round trip does not lose it.
	if got := CleanQueries(want); !slices.Equal(got, want) {
		t.Errorf("CleanQueries = %q", got)
	}
}
