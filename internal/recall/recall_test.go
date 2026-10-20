package recall

import (
	"path/filepath"
	"testing"

	"github.com/juev/memcue/internal/store"
)

var facts = []struct{ content, keywords, origin string }{
	{"Gradle вне make требует JAVA_HOME=/opt/homebrew/opt/openjdk@21, иначе сборка не находит JDK.", "gradle, jdk, java", "lists 2026-10-09"},
	{"Issue #20 в hledger-lsp: Neovim format-on-type не двигает курсор после TextEdit.", "neovim, format", "hledger-lsp 2026-09-01"},
	{"Публичный Caddy монтирует каталог configs/caddy; перезагрузка конфига только с путём /etc/caddy/configs/Caddyfile.", "caddy reload", "server-migration 2026-09-24"},
	{"Эксперимент byte-vs-BPE на vast.ai: арендовать RTX 4090, запускать обучение на сутки.", "vast, gpu", "llm 2026-08-01"},
	{"Прокси mihomo на 127.0.0.1:1080 для тестового стенда, NO_PROXY содержит staging.example.com.", "прокси, proxy", "loadtest 2026-09-10"},
	{"Прокси mihomo на 127.0.0.1:1080 смешанный: HTTP и SOCKS на одном порту.", "прокси, proxy", "_global 2026-08-01"},
}

func setup(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, f := range facts {
		if _, err := s.Add(f.content, splitKW(f.keywords), f.origin); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func splitKW(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func recall(t *testing.T, s *store.Store, prompt, tier string) []Hit {
	t.Helper()
	hs, err := Recall(s, prompt, tier, Default)
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestConfirmationsRecallNothing(t *testing.T) {
	s := setup(t)
	for _, p := range []string{"да, запускай эксперимент", "давай закрывать", "ок", "продолжаем"} {
		if got := recall(t, s, p, "server-migration"); len(got) != 0 {
			t.Errorf("%q recalled %v", p, got)
		}
	}
}

func TestFactFromAnotherRepositoryIsRecalled(t *testing.T) {
	s := setup(t)
	got := recall(t, s, "Хочу запустить gradle руками без make, какой JAVA_HOME выставить для JDK?", "perigee")
	if len(got) == 0 || got[0].Origin != "lists 2026-10-09" {
		t.Errorf("got %+v", got)
	}
}

func TestOwnRepositoryWinsTies(t *testing.T) {
	s := setup(t)
	p := Default
	p.MinMatched = 2
	for tier, want := range map[string]string{"loadtest": "loadtest 2026-09-10", "": "_global 2026-08-01"} {
		got, err := Recall(s, "прокси mihomo", tier, p)
		if err != nil || len(got) != 2 || got[0].Coverage != got[1].Coverage {
			t.Fatalf("want a tie of two, got %+v %v", got, err)
		}
		if got[0].Origin != want {
			t.Errorf("tier %q: first is %q, want %q", tier, got[0].Origin, want)
		}
	}
}

func TestAtMostMaxFacts(t *testing.T) {
	s := setup(t)
	p := Default
	p.MaxFacts, p.MinMatched, p.MinCoverage = 1, 1, 0
	hs, err := Recall(s, "прокси mihomo caddy gradle", "", p)
	if err != nil || len(hs) != 1 {
		t.Errorf("got %d %v", len(hs), err)
	}
}

// A two-word prompt asks about two terms: the dev-set minimum of three matched terms silenced every fact about them.
func TestShortPromptWithKnownTermsRecalls(t *testing.T) {
	s := setup(t)
	hs := recall(t, s, "прокси mihomo", "loadtest")
	if len(hs) == 0 {
		t.Fatalf(`"прокси mihomo" recalled nothing`)
	}
}

// A ticket key is an exact anchor, not a project word: PROJ-1234 recalls for PROJ-1234 and never for PROJ-1235.
func TestTicketKeyRecallsExactOnly(t *testing.T) {
	s := setup(t)
	a, err := s.AddWith("Тикет PROJ-1234: берём в работу.", []string{"PROJ"}, "x 2026-10-20", "",
		store.Meta{Kind: "decision", Scope: "repo:x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddWith("Тикет PROJ-1235: отложен.", []string{"PROJ"}, "x 2026-10-20", "",
		store.Meta{Kind: "decision", Scope: "repo:x"})
	if err != nil {
		t.Fatal(err)
	}
	for prompt, want := range map[string]string{"давай брать PROJ-1234": a.ID, "PROJ-1235": b.ID} {
		got := recall(t, s, prompt, "x")
		if len(got) == 0 || got[0].ID != want {
			t.Errorf("%q recalled %+v, want %s", prompt, got, want)
		}
	}
	if got := recall(t, s, "давай брать PROJ-1236", "x"); len(got) != 0 {
		t.Errorf("PROJ-1236 recalled %+v", got)
	}
	// A prompt naming the wrong ticket must not reach the other one through the shared keyword "PROJ".
	if got := recall(t, s, "PROJ-1235 postgres port", "x"); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("wrong ticket plus postgres recalled %+v", got)
	}
}

func TestExpiredFactIsNotRecalled(t *testing.T) {
	s := setup(t)
	f, err := s.AddExpiring("Стенд нагрузки поднимается на порту 8443 в staging.", []string{"staging"},
		"loadtest 2026-10-01", "2026-10-10")
	if err != nil {
		t.Fatal(err)
	}
	p := Default
	p.Today = "2026-10-19"
	hs, err := Recall(s, "на каком порту поднимается стенд нагрузки в staging", "loadtest", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hs {
		if h.ID == f.ID {
			t.Error("expired fact recalled")
		}
	}
	p.Today = "2026-10-05" // still inside its last day
	hs, err = Recall(s, "на каком порту поднимается стенд нагрузки в staging", "loadtest", p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hs {
		if h.ID == f.ID {
			found = true
		}
	}
	if !found {
		t.Error("fact not recalled before its expiry")
	}
}
