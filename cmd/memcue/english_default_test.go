package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

func factHistory(t *testing.T, id string) []store.Fact {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	h, err := s.History(id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestEnglishDefaultCLIStillAcceptsNonEnglishFacts(t *testing.T) {
	env(t)
	var out bytes.Buffer
	if err := runAdd([]string{"--", "Сервис слушает порт 5432."}, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.Fields(out.String())[0]
	if f := storedFact(t, id); f.Content != "Сервис слушает порт 5432." {
		t.Errorf("add translated content: %s", f.Content)
	}
	if err := runCorrect([]string{id, "Сервис слушает порт 5433."}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	h := factHistory(t, id)
	if len(h) != 2 || h[0].Content != "Сервис слушает порт 5432." || h[1].Content != "Сервис слушает порт 5433." {
		t.Errorf("CLI history: %+v", h)
	}
}

func TestEnglishDefaultCJKHintStartsWithEnglish(t *testing.T) {
	env(t)
	addFactForTest(t, "The synthetic service supports Unicode.")
	var out bytes.Buffer
	if err := runSearch([]string{"-json", "中文"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "search first in English") {
		t.Errorf("hint: %s", out.String())
	}
}
