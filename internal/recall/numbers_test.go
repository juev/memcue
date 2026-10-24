package recall

import (
	"path/filepath"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// A prompt that is nothing but a version must reach the fact that names it: the numeric term is exact, so it
// says enough on its own the way a ticket key does. With the flag off the same prompt recalls nothing, which is
// why the number has to be indexed at all.
func TestVersionOnlyPromptRecallsTheFact(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Added with the flag off, so the index is word-only until the base is rebuilt below.
	if _, err := s.Add("Стенд собран под Go 1.27, другой версии не поддерживает.", nil, "notes 2026-10-19"); err != nil {
		t.Fatal(err)
	}

	if got, err := Recall(s, "1.27", "", Default); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Fatalf("version recalled %v with the flag off, want nothing", got)
	}

	t.Setenv("MEMCUE_NUMBER_TERMS", "1")
	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	got, err := Recall(s, "1.27", "", Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("a bare version recalled nothing, want the fact that names it")
	}
}

// A version in the prompt is an anchor, not a filter: facts that answer the same question without repeating the
// number are still candidates.
func TestVersionDoesNotFilterThePool(t *testing.T) {
	t.Setenv("MEMCUE_NUMBER_TERMS", "1")
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Add("Сборка стенда идёт под Go 1.27, другой версии стенд не поддерживает.", nil, "notes 2026-10-19"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("Сборка стенда идёт через make stand и требует пин версии.", nil, "notes 2026-10-20"); err != nil {
		t.Fatal(err)
	}
	p := Default
	p.MaxFacts = 3
	got, err := Recall(s, "сборка стенда 1.27", "", p)
	if err != nil {
		t.Fatal(err)
	}
	var numbered, worded bool
	for _, h := range got {
		if h.Content == "Сборка стенда идёт под Go 1.27, другой версии стенд не поддерживает." {
			numbered = true
		}
		if h.Content == "Сборка стенда идёт через make stand и требует пин версии." {
			worded = true
		}
	}
	if !numbered {
		t.Errorf("the fact naming the version was not recalled: %+v", got)
	}
	if !worded {
		t.Errorf("the version filtered out a fact answering the same question: %+v", got)
	}
}
