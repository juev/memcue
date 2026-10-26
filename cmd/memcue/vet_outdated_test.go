package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// vet retires by rule a live fact past its expiry and installation state of an older memcue, keeps a behaviour fact
// that names an old version, and a dry run only lists them (#457).
func TestVetRetiresOutdatedInstallAndExpired(t *testing.T) {
	env(t)
	old := version
	t.Cleanup(func() { version = old })
	version = "0.16.0"
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	add := func(content, expires string) string {
		t.Helper()
		f, err := s.AddWith(content, nil, "x 2026-10-20", expires, store.Meta{Scope: "global"})
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	state := add("The memcue binaries on the build host and on mac are 0.8.0.", "")
	behaviour := add("С memcue 0.15.0 карта пропускает строки, которые повторяет память.", "")
	expired := add("Окно миграции кластера orchid открыто до 2026-01-02.", "2026-01-02")
	ahead := add("Окно миграции кластера orchid-two открыто весь год.", "2099-01-02")
	_ = s.Close()

	var out bytes.Buffer
	if err := runVet([]string{"-no-model", "-dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{state, expired} {
		if f := factByID(t, id); f.Status != store.Active {
			t.Errorf("a dry run retired %s", id)
		}
	}
	for _, want := range []string{"старше текущего релиза 1", "с истёкшим сроком 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run report lacks %q:\n%s", want, out.String())
		}
	}
	if err := runVet([]string{"-no-model"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{state: store.Retired, expired: store.Retired, behaviour: store.Active,
		ahead: store.Active} {
		if f := factByID(t, id); f.Status != want {
			t.Errorf("%q: %s, want %s", f.Content, f.Status, want)
		}
	}
}
