package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openCodeTranscripts writes a base with the shape OpenCode 2 keeps: two sessions of one repository, one session of
// another, a child session, a tool result and a synthetic entry that hold the words too.
func openCodeTranscripts(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "data", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("data directory: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open base: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`create table if not exists session_v2 (id text primary key, project_id text not null, parent_id text,
			directory text not null, title text, version text not null, time_created integer not null,
			time_updated integer not null)`,
		`create table if not exists session_message (id text primary key, session_id text not null, type text not null,
			seq integer not null, time_created integer not null, time_updated integer not null, data text not null,
			unique (session_id, seq))`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	ms := func(offset time.Duration) int64 { return at.Add(offset).UnixMilli() }
	session := func(id, parent, directory string) {
		t.Helper()
		if _, err := db.Exec(`insert into session_v2 (id, project_id, parent_id, directory, title, version,
			time_created, time_updated) values (?, 'project', ?, ?, 'title', '2.2.7', ?, ?)`,
			id, nullIfEmpty(parent), directory, ms(0), ms(0)); err != nil {
			t.Fatalf("session %s: %v", id, err)
		}
	}
	message := func(id, session, kind string, seq int, offset time.Duration, data string) {
		t.Helper()
		if _, err := db.Exec(`insert into session_message (id, session_id, type, seq, time_created, time_updated, data)
			values (?, ?, ?, ?, ?, ?, ?)`, id, session, kind, seq, ms(offset), ms(offset), data); err != nil {
			t.Fatalf("message %s: %v", id, err)
		}
	}
	session("ses-shop", "", "/w/shop")
	session("ses-lists", "", "/w/lists")
	session("ses-child", "ses-shop", "/w/shop")
	message("msg-1", "ses-shop", "user", 1, 0, `{"text":"Какой порт у прокси в shop?"}`)
	message("msg-2", "ses-shop", "assistant", 2, time.Minute,
		`{"content":[{"type":"reasoning","text":"REASONING-CANARY"},`+
			`{"type":"text","text":"Прокси shop слушает порт 7070 после переезда."},`+
			`{"type":"tool","name":"shell","state":{"content":"PROXY_TOKEN=TOOL-CANARY порт прокси 7070"}}]}`)
	message("msg-3", "ses-shop", "synthetic", 3, 2*time.Minute, `{"text":"SYNTHETIC-CANARY"}`)
	message("msg-4", "ses-lists", "user", 1, 3*time.Minute, `{"text":"В lists прокси нет, только база."}`)
	message("msg-child", "ses-child", "user", 1, 4*time.Minute, `{"text":"CHILD-CANARY"}`)
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// memcue sessions reads the OpenCode base too: its user and agent text is found, with the session id and the
// directory, while tool output, reasoning, synthetic entries and child sessions stay out; -cwd and -json behave as
// they do for Claude Code sessions.
func TestSessionsReadsOpenCodeTranscripts(t *testing.T) {
	dir := env(t)
	openCodeTranscripts(t, dir)

	var out bytes.Buffer
	if err := runSessions([]string{"порт", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "assistant in session ses-shop: Прокси shop слушает порт 7070") {
		t.Errorf("OpenCode passage is missing:\n%s", got)
	}
	if !strings.Contains(got, "user in session ses-shop: Какой порт у прокси в shop?") {
		t.Errorf("OpenCode question is missing:\n%s", got)
	}
	for _, leak := range []string{"TOOL-CANARY", "REASONING-CANARY", "SYNTHETIC-CANARY", "CHILD-CANARY"} {
		if strings.Contains(got, leak) {
			t.Errorf("output holds %q:\n%s", leak, got)
		}
	}

	out.Reset()
	if err := runSessions([]string{"-cwd", "/w/shop", "порт", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "ses-shop") || strings.Contains(got, "ses-lists") {
		t.Errorf("-cwd kept the wrong sessions:\n%s", got)
	}

	out.Reset()
	if err := runSessions([]string{"-json", "-cwd", "/w/lists", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"session":"ses-lists"`) {
		t.Errorf("-json did not carry the OpenCode session:\n%s", got)
	}
}
