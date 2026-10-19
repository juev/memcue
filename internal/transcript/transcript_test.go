package transcript

import (
	"encoding/json"
	"strings"
	"testing"
)

const sample = `{"type":"user","cwd":"/src/app","timestamp":"2026-10-18T06:41:00Z","message":{"content":"проверь сборку<system-reminder>секрет</system-reminder>"}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"думаю"},{"type":"tool_use","name":"Bash","input":{"command":"make build"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}
{"type":"user","isMeta":true,"message":{"content":[{"type":"text","text":"Base directory for this skill"}]}}
{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"subagent"}]}}
{"type":"attachment","message":{"content":"x"}}
not json
{"type":"assistant","message":{"content":[{"type":"text","text":"Сборка зелёная."}]}}
`

func TestReadRendersConversation(t *testing.T) {
	s, err := Read(strings.NewReader(sample), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "USER: проверь сборку\n\nTOOL Bash: {\"command\":\"make build\"}\n\nRESULT: ok\n\nASSISTANT: Сборка зелёная.\n\n"
	if s.Text != want {
		t.Errorf("text:\n%q\nwant\n%q", s.Text, want)
	}
	if s.CWD != "/src/app" || s.Start != "2026-10-18" || s.Lines != 8 {
		t.Errorf("got cwd %q start %q lines %d", s.CWD, s.Start, s.Lines)
	}
}

func TestReadFromOffsetKeepsCWD(t *testing.T) {
	s, err := Read(strings.NewReader(sample), 3)
	if err != nil {
		t.Fatal(err)
	}
	if s.Text != "ASSISTANT: Сборка зелёная.\n\n" || s.CWD != "/src/app" {
		t.Errorf("got %q cwd %q", s.Text, s.CWD)
	}
}

func TestCutKeepsHeadAndTail(t *testing.T) {
	got := cut(strings.Repeat("a", 100)+strings.Repeat("z", 100), 30)
	if !strings.HasPrefix(got, strings.Repeat("a", 20)) || !strings.HasSuffix(got, strings.Repeat("z", 10)) ||
		!strings.Contains(got, "170 chars cut") {
		t.Errorf("got %q", got)
	}
}

func TestEditShowsOnlyTheFile(t *testing.T) {
	in := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/src/app/main.go","old_string":"a","new_string":"b"}}]}}`
	s, err := Read(strings.NewReader(in), 0)
	if err != nil || s.Text != "TOOL Edit: /src/app/main.go\n\n" {
		t.Errorf("got %q %v", s.Text, err)
	}
}

func TestRenderMessages(t *testing.T) {
	var msgs []Message
	in := `[{"role":"system","content":"you are"},
		{"role":"user","content":"проверь порт"},
		{"role":"assistant","content":null,"tool_calls":[{"function":{"name":"terminal","arguments":"{\"command\":\"ss -lnt\"}"}}]},
		{"role":"tool","content":[{"type":"text","text":"LISTEN 0 128 *:55432"}]},
		{"role":"assistant","content":"Порт 55432 открыт."}]`
	if err := json.Unmarshal([]byte(in), &msgs); err != nil {
		t.Fatal(err)
	}
	want := "USER: проверь порт\n\nTOOL terminal: {\"command\":\"ss -lnt\"}\n\nRESULT: LISTEN 0 128 *:55432\n\nASSISTANT: Порт 55432 открыт.\n\n"
	if got := RenderMessages(msgs); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}
