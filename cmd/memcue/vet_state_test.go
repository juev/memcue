package main

import (
	"bytes"
	"strings"
	"testing"
)

// The agent's add is live at once and skips vet.Rules, so add refuses installation state from the agent; the user's
// add and the agent's rule that quotes a version are stored (#475).
func TestAddRefusesAgentState(t *testing.T) {
	env(t)
	// What the agent saw in a tool's output is checked like what it concluded (#494), and so is a fact that names no
	// source: it is the agent's (#574).
	for _, source := range [][]string{{"-source", "agent"}, {"-source", "tool"}, nil} {
		err := runAdd(append(source, "-kind", "environment", "--",
			"The gateway was restarted 2026-10-19 23:30:02 (PID 46005)."), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "installation state (restart)") {
			t.Fatalf("%v state: err = %v, want a refusal naming the restart", source, err)
		}
	}
	for _, args := range [][]string{
		{"-source", "agent", "-kind", "rule", "--", "We stay on tool 1.2.3 until the schema migration lands."},
		{"-source", "user", "-kind", "environment", "--", "The gateway was restarted 2026-10-19 23:30:02 (PID 46005)."},
	} {
		if err := runAdd(args, &bytes.Buffer{}); err != nil {
			t.Errorf("add %v: %v", args, err)
		}
	}
}

// A shelf fact says what an outside source states about another system, so the agent's finding of a config default
// goes on the shelf, while the same text in an ordinary place is still installation state (#565). Hermes and DSH pass
// their shelf parameter as -shelf.
func TestAddKeepsShelfFindingOfAConfigDefault(t *testing.T) {
	env(t)
	const finding = "The vendor API's poller.interval defaults to 30 s, with a 10 s floor (API reference, 2026-10)."
	err := runAdd([]string{"-source", "agent", "--", finding}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "installation state (config default)") {
		t.Fatalf("ordinary place: err = %v, want a refusal naming the config default", err)
	}
	for _, place := range [][]string{{"-shelf", "research"}, {"-scope", "shelf:research"}} {
		for _, source := range []string{"agent", "tool"} {
			args := append([]string{"-source", source}, place...)
			var out bytes.Buffer
			if err := runAdd(append(args, "--", finding), &out); err != nil {
				t.Errorf("add %v: %v", args, err)
			} else if strings.TrimSpace(out.String()) == "" {
				t.Errorf("add %v printed no id", args)
			}
		}
	}
}

// A refusal names the ways out (#577): an agent took the bare refusal of this finding for "not worth storing" and
// dropped what the server does. The same text goes on the shelf, and without the version range and the repository's
// part it is stored in an ordinary place.
func TestAddRefusalNamesTheWaysOut(t *testing.T) {
	env(t)
	const finding = "bookOrbit POST books/query принимает filter {type:group, join:AND, rules:[{type:rule, field:format, " +
		"operator:includesAny, value:[...]}]}: сервер сравнивает со всеми файлами книги (book_files.format, строчные), " +
		"до 20 значений, неизвестные форматы просто не совпадают; total считается уже с фильтром. Поддерживается во " +
		"всех тегах v2.0.1–v3.6.0. Perigee шлёт его со списком Library.FORMATS на экране сервера (PR #64, issue #55)."
	err := runAdd([]string{"-source", "tool", "-kind", "environment", "--", finding}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("the finding with a version range was stored as an ordinary fact")
	}
	for _, want := range []string{"installation state (version)", "-shelf research", "shelf parameter of memcue_add",
		"drop it from the text"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
	if err := runAdd([]string{"-source", "tool", "-shelf", "research", "--", finding}, &bytes.Buffer{}); err != nil {
		t.Errorf("on the shelf: %v", err)
	}
	const rest = "bookOrbit POST books/query принимает filter по полю format с оператором includesAny: сервер сравнивает " +
		"со всеми файлами книги, до 20 значений; total считается уже с фильтром."
	if err := runAdd([]string{"-source", "tool", "-kind", "environment", "--", rest}, &bytes.Buffer{}); err != nil {
		t.Errorf("without the version: %v", err)
	}
}

// DSH hands the model the first 500 characters of memcue's error (plugins/deepseek-harness/src/index.mjs), so the
// refusal with its longest part must fit, or the ways out are cut off (#577).
func TestAddRefusalFitsTheDSHCut(t *testing.T) {
	env(t)
	err := runAdd([]string{"-source", "agent", "--",
		"The vendor API's poller.interval defaults to 30 s, with a 10 s floor (API reference, 2026-10)."}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "(config default)") {
		t.Fatalf("err = %v, want the config default refusal", err)
	}
	if n := len([]rune("memcue: " + err.Error())); n > 500 {
		t.Errorf("the refusal is %d characters, DSH keeps 500", n)
	}
}
