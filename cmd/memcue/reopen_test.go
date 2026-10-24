package main

import (
	"bytes"
	"strings"
	"testing"
)

// retire then reopen leaves the fact active with the same content, scope and source, and recall returns it.
func TestReopenBringsARetiredFactBackToRecall(t *testing.T) {
	env(t)
	var out bytes.Buffer
	if err := runAdd([]string{"-keywords", "gradle, jdk", "-origin", "lists 2026-10-09", "-scope", "repo:lists",
		"-source", "user", "--", "Gradle вне make требует JAVA_HOME=/opt/homebrew/opt/openjdk@21."}, &out); err != nil {
		t.Fatal(err)
	}
	id := strings.Fields(out.String())[0]
	prompt := "какой JAVA_HOME нужен gradle без make для jdk"
	recalled := func() bool {
		t.Helper()
		var r bytes.Buffer
		if err := runRecall([]string{prompt}, &r); err != nil {
			t.Fatal(err)
		}
		return strings.Contains(r.String(), "openjdk@21")
	}
	if !recalled() {
		t.Fatal("the fact is not recalled before the retire")
	}
	if err := runRetire([]string{id}, &out); err != nil {
		t.Fatal(err)
	}
	if recalled() {
		t.Fatal("a retired fact is recalled")
	}
	out.Reset()
	if err := runReopen([]string{id}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != id+" v3 active" {
		t.Errorf("reopen printed %q", got)
	}
	f := vetStatus(t, id)
	if f.Scope != "repo:lists" || f.Source != "user" || !strings.Contains(f.Content, "openjdk@21") {
		t.Errorf("reopened %+v", f)
	}
	if !recalled() {
		t.Error("a reopened fact is not recalled")
	}
	if err := runReopen([]string{id}, &out); err == nil {
		t.Error("reopening an active fact gave no error")
	}
}
