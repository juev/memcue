package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// dshJournals writes the journals dsh keeps in a sessions directory: two sessions of one repository, one of
// another, a subagent session, and the machine-written records that hold the words too.
func dshJournals(t *testing.T, sessions string) {
	t.Helper()
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	ms := func(offset time.Duration) int64 { return at.Add(offset).UnixMilli() }
	header := func(id, cwd string, depth int) string {
		return fmt.Sprintf(`{"type":"session","version":4,"id":%q,"createdAt":%d,"cwd":%q,"isSeeded":false,`+
			`"delegationDepth":%d}`, id, ms(0), cwd, depth)
	}
	record := func(seq int, offset time.Duration, kind, data string) string {
		return fmt.Sprintf(`{"seq":%d,"time":%d,"type":%q,"data":%s}`, seq, ms(offset), kind, data)
	}
	user := func(id, text, kind string) string {
		return fmt.Sprintf(`{"id":%q,"role":"user","content":[{"type":"text","text":%q}],"source":{"kind":%q}}`,
			id, text, kind)
	}
	assistant := func(id, text string) string {
		return fmt.Sprintf(`{"message":{"id":%q,"role":"assistant","content":[`+
			`{"type":"reasoning","text":"REASONING-CANARY порт прокси"},{"type":"text","text":%q},`+
			`{"type":"tool-call","name":"shell"}]},`+
			`"stream":[{"type":"text-delta","text":"STREAM-CANARY порт прокси"}]}`, id, text)
	}
	write := func(dir, session string, lines ...string) {
		t.Helper()
		path := filepath.Join(sessions, dir, session, "session.v4.jsonl.zstd")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("session directory: %v", err)
		}
		enc, err := zstd.NewWriter(nil)
		if err != nil {
			t.Fatal(err)
		}
		data := enc.EncodeAll([]byte(strings.Join(lines, "\n")+"\n"), nil)
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("journal: %v", err)
		}
	}
	write("-w-shop", "dsh-shop", header("dsh-shop", "/w/shop", 0),
		record(1, 0, "user/message", user("u1", "Какой порт у прокси в shop?", "user")),
		record(2, time.Minute, "assistant/message", assistant("a1", "Прокси shop слушает порт 7070 после переезда.")),
		record(3, 2*time.Minute, "tool/result",
			`{"message":{"content":[{"type":"text","text":"PROXY_TOKEN=TOOL-CANARY порт прокси 7070"}]}}`),
		record(4, 3*time.Minute, "user/message", user("m1", "MEMCUE-CANARY порт прокси", "memcue")))
	write("-w-lists", "dsh-lists", header("dsh-lists", "/w/lists", 0),
		record(1, 4*time.Minute, "user/message", user("u2", "В lists прокси нет, только база.", "user")))
	write("-w-sub", "dsh-sub", header("dsh-sub", "/w/shop", 1),
		record(1, 5*time.Minute, "user/message", user("s1", "SUBAGENT-CANARY порт прокси", "user")))
}

// memcue sessions reads the dsh journals too: their user and agent text is found, with the session id and the
// directory, while tool output, reasoning, the streaming deltas, the plugin's own notices and subagent sessions
// stay out; -cwd, -session and -json behave as they do for Claude Code sessions.
func TestSessionsReadsDSHJournals(t *testing.T) {
	dir := env(t)
	dshJournals(t, filepath.Join(dir, ".dsh", "sessions"))

	var out bytes.Buffer
	if err := runSessions([]string{"порт", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "assistant in session dsh-shop: Прокси shop слушает порт 7070") {
		t.Errorf("dsh passage is missing:\n%s", got)
	}
	if !strings.Contains(got, "user in session dsh-shop: Какой порт у прокси в shop?") {
		t.Errorf("dsh question is missing:\n%s", got)
	}
	for _, leak := range []string{"TOOL-CANARY", "REASONING-CANARY", "STREAM-CANARY", "MEMCUE-CANARY", "SUBAGENT-CANARY"} {
		if strings.Contains(got, leak) {
			t.Errorf("output holds %q:\n%s", leak, got)
		}
	}

	out.Reset()
	if err := runSessions([]string{"-cwd", "/w/shop", "порт", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "dsh-shop") || strings.Contains(got, "dsh-list") {
		t.Errorf("-cwd kept the wrong sessions:\n%s", got)
	}

	out.Reset()
	if err := runSessions([]string{"-session", "dsh-shop", "порт", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, "dsh-shop") {
		t.Errorf("-session kept the current session:\n%s", got)
	}

	out.Reset()
	if err := runSessions([]string{"-json", "-cwd", "/w/lists", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"session":"dsh-lists"`) {
		t.Errorf("-json did not carry the dsh session:\n%s", got)
	}
}

// DSH_HOME names another home, as the harness resolves it: the journals there are read instead of ~/.dsh.
func TestSessionsReadsTheDSHHomeOfTheEnvironment(t *testing.T) {
	dir := env(t)
	home := filepath.Join(dir, "elsewhere")
	dshJournals(t, filepath.Join(home, "sessions"))
	t.Setenv("DSH_HOME", home)

	var out bytes.Buffer
	if err := runSessions([]string{"порт", "прокси"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "dsh-shop") {
		t.Errorf("the journals of DSH_HOME are missing:\n%s", got)
	}
}
