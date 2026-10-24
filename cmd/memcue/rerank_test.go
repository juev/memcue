package main

import (
	"testing"

	"github.com/juev/memcue/internal/rerank"
)

// MEMCUE_RERANK names the reader. Without it Jev runs when it is configured and the words decide otherwise, whether
// the main model is configured or not: the listwise reader is opt-in (#321); `off` keeps the words. `local`, the
// removed tree reader (#324), is an unknown value like any other and gets the default.
func TestRerankReaderPrecedence(t *testing.T) {
	for _, k := range []string{"MEMCUE_JUDGE_URL", "MEMCUE_JUDGE_MODEL", "MEMCUE_JUDGE_KEY", "MEMCUE_API_URL",
		"MEMCUE_MODEL", "MEMCUE_API_KEY", "MEMCUE_RERANK"} {
		t.Setenv(k, "")
	}
	if r := ranker(); r != nil {
		t.Fatalf("no endpoint: ranker = %#v, want nil", r)
	}

	t.Setenv("MEMCUE_API_URL", "https://api.deepseek.com")
	t.Setenv("MEMCUE_MODEL", "deepseek-flash")
	t.Setenv("MEMCUE_API_KEY", "k")
	if r := ranker(); r != nil {
		t.Fatalf("main model only: ranker = %#v, want nil", r)
	}
	for _, name := range []string{"local", "unknown"} {
		t.Setenv("MEMCUE_RERANK", name)
		if r := ranker(); r != nil {
			t.Fatalf("%s without Jev: ranker = %#v, want nil", name, r)
		}
	}
	t.Setenv("MEMCUE_RERANK", "")

	t.Setenv("MEMCUE_JUDGE_URL", "http://127.0.0.1:1/decisions")
	t.Setenv("MEMCUE_JUDGE_MODEL", "typesafe/jev-1.14")
	t.Setenv("MEMCUE_JUDGE_KEY", "k")
	if _, ok := ranker().(*rerank.Ranker); !ok {
		t.Fatalf("both configured: ranker = %#v, want the Jev pass", ranker())
	}

	for _, name := range []string{"listwise", "main"} {
		t.Setenv("MEMCUE_RERANK", name)
		if _, ok := ranker().(*rerank.Listwise); !ok {
			t.Fatalf("%s: ranker = %#v, want the listwise reader", name, ranker())
		}
	}

	for _, name := range []string{"local", "unknown"} {
		t.Setenv("MEMCUE_RERANK", name)
		if _, ok := ranker().(*rerank.Ranker); !ok {
			t.Fatalf("%s with Jev configured: ranker = %#v, want the Jev pass", name, ranker())
		}
	}

	t.Setenv("MEMCUE_RERANK", "off")
	if r := ranker(); r != nil {
		t.Fatalf("off: ranker = %#v, want nil", r)
	}

	// A reader named but not configured falls back to the words, not to the other reader.
	t.Setenv("MEMCUE_RERANK", "listwise")
	t.Setenv("MEMCUE_API_URL", "")
	if r := ranker(); r != nil {
		t.Fatalf("listwise without an endpoint: ranker = %#v, want nil", r)
	}
}
