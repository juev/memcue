package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

const secretToken = "sk-abcdefghijklmnopqrstuvwxyz012345"

// add and correct refuse a credential, and -force is the explicit way past the guard.
func TestAddAndCorrectRefuseSecrets(t *testing.T) {
	env(t)
	for _, text := range []string{
		"Ключ API: " + secretToken,
		"Пароль от демо-базы: hunter2",
	} {
		if err := runAdd([]string{text}, &bytes.Buffer{}); err == nil {
			t.Errorf("add stored a secret: %q", text)
		}
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	if act, _ := s.Active(); len(act) != 0 {
		t.Errorf("a refused add wrote facts: %+v", act)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runAdd([]string{"-force", "Ключ API: " + secretToken}, &out); err != nil {
		t.Fatalf("add -force: %v", err)
	}
	id := strings.TrimSpace(out.String())
	if err := runCorrect([]string{id, "Пароль от демо-базы: hunter2"}, &bytes.Buffer{}); err == nil {
		t.Error("correct stored a secret")
	}
	s, err = store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.Content, "hunter2") {
		t.Errorf("a refused correction changed the fact: %+v", f)
	}
	if err := runCorrect([]string{"-force", id, "Пароль от демо-базы: hunter2"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("correct -force: %v", err)
	}
}

// import skips a fact that looks like a secret, stores the rest and reports the count without echoing the value.
func TestImportSkipsSecrets(t *testing.T) {
	dir := env(t)
	path := filepath.Join(dir, "facts.json")
	body := `{"note":"n","origin":"demo 2026-10-20","facts":[` +
		`{"content":"Ключ API: ` + secretToken + `","keywords":[]},` +
		`{"content":"Обычный факт про порт 3000.","keywords":["port"]}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runImport([]string{path}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), secretToken) {
		t.Error("import echoed the secret")
	}
	if !strings.Contains(out.String(), "skipped 1") {
		t.Errorf("import did not report the skip: %q", out.String())
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	act, _ := s.Active()
	if len(act) != 1 || !strings.Contains(act[0].Content, "порт 3000") {
		t.Errorf("active %+v", act)
	}
}

// migrate drops a secret fact before storing and before writing the dry-run record, and says why without the text.
func TestMigrateSkipsSecrets(t *testing.T) {
	env(t)
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte("Заметка про демо-базу."), 0o600); err != nil {
		t.Fatal(err)
	}
	old := noteFn
	noteFn = func(context.Context, string, string) (extract.Result, error) {
		return extract.Result{Facts: []extract.Fact{
			{Content: "Обычный факт про порт 3000.", Keywords: []string{"port"}},
			{Content: "Ключ API: " + secretToken, Keywords: []string{"key"}},
		}}, nil
	}
	t.Cleanup(func() { noteFn = old })
	var out bytes.Buffer
	if err := runMigrate([]string{path}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), secretToken) {
		t.Error("migrate echoed the secret")
	}
	var rec struct {
		Facts    []extract.Fact `json:"facts"`
		Rejected []string       `json:"rejected"`
	}
	if err := json.Unmarshal(out.Bytes(), &rec); err != nil {
		t.Fatalf("record %q: %v", out.String(), err)
	}
	if len(rec.Facts) != 1 || len(rec.Rejected) != 1 {
		t.Errorf("record facts %+v rejected %+v", rec.Facts, rec.Rejected)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	act, _ := s.Active()
	if len(act) != 1 || !strings.Contains(act[0].Content, "порт 3000") {
		t.Errorf("active %+v", act)
	}
}
