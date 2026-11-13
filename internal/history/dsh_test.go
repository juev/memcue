package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// dshSession is the header line of a journal: version, session id, working directory and delegation depth. An
// empty id leaves the field out, as a header that never named one.
func dshSession(version int, id string, depth int) string {
	rec := map[string]any{"type": "session", "version": version, "createdAt": t0.UnixMilli(),
		"cwd": "/w", "isSeeded": false, "delegationDepth": depth}
	if id != "" {
		rec["id"] = id
	}
	if version >= 4 {
		rec["agentPreset"] = "default"
	}
	if depth > 0 {
		rec["parentSession"] = "session-parent"
		rec["origin"] = "delegation"
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

// dshEntry is one record line of a journal: a record of kind with data at t0 plus offset seconds (epoch ms). A
// negative offset leaves the time out; extra overrides top-level fields (an empty string removes the field).
func dshEntry(kind string, seq int, data map[string]any, offset int, extra map[string]any) string {
	rec := map[string]any{"seq": seq, "type": kind, "data": data}
	if offset >= 0 {
		rec["time"] = t0.Add(time.Duration(offset) * time.Second).UnixMilli()
	}
	for k, v := range extra {
		if v == "" {
			delete(rec, k)
		} else {
			rec[k] = v
		}
	}
	b, _ := json.Marshal(rec)
	return string(b)
}

// dshUserData is the data of a user/message record: the text part and the source kind that names the writer. An
// empty id or kind leaves the field out, as a record without one.
func dshUserData(id, text, kind string) map[string]any {
	data := map[string]any{"role": "user",
		"content": []map[string]any{{"type": "text", "text": text}}}
	if id != "" {
		data["id"] = id
	}
	if kind != "" {
		data["source"] = map[string]any{"kind": kind}
	}
	return data
}

// dshAssistantData is the data of an assistant/message record: the message with its content parts, and a stream
// of deltas that repeats the text and must never be read.
func dshAssistantData(id string, parts []map[string]any) map[string]any {
	return map[string]any{
		"message": map[string]any{"id": id, "role": "assistant", "content": parts},
		"step":    1, "turn": 1,
		"stream": []map[string]any{{"type": "text-delta", "text": "STREAM-CANARY widget delta"}},
	}
}

// dshWrite puts a journal where dsh keeps it: <encoded working directory>/<session id>/session.v<N>.jsonl.zstd,
// zstd-compressed, with the empty session.lock beside it.
func dshWrite(t *testing.T, root, dir, session string, version int, lines ...string) {
	t.Helper()
	path := filepath.Join(root, dir, session, fmt.Sprintf("session.v%d.jsonl.zstd", version))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "session.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// dshWritePlain puts a journal where dsh keeps it when its JSONL encoding is plaintext rather than zstd.
func dshWritePlain(t *testing.T, root, dir, session string, version int, lines ...string) {
	t.Helper()
	path := filepath.Join(root, dir, session, fmt.Sprintf("session.v%d.jsonl", version))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The reading keeps the user's and the agent's text of v3 and v4 journals and leaves out what a program wrote:
// reasoning and tool-call parts, tool output, the machine-written user messages of every source kind, machine
// openings when the source is absent, delegated sessions and unknown journal versions; an entry without a time,
// a repeated entry and an oversized line are counted and left out.
func TestReadDSHKeepsTextAndLeavesOutMachineText(t *testing.T) {
	root := t.TempDir()
	var lines []string
	seq := 0
	add := func(kind string, data map[string]any, offset int, extra map[string]any) {
		seq++
		lines = append(lines, dshEntry(kind, seq, data, offset, extra))
	}
	lines = append(lines, dshSession(4, "session-a", 0))
	add("user/message", dshUserData("u1", "Как настроить виджет?", "user"), 1, nil)
	add("assistant/message", dshAssistantData("a1", []map[string]any{
		{"type": "reasoning", "text": "secret widget plan"},
		{"type": "text", "text": "Виджет настраивается через gizmo."},
		{"type": "tool-call", "name": "shell", "input": map[string]any{"command": "echo widget"}}}), 2, nil)
	add("tool/result", map[string]any{"output": "TOKEN=widget-secret"}, 3, nil)
	add("system/message", map[string]any{"text": "system widget note"}, 4, nil)
	add("user/message", dshUserData("u2", "виджет prompt", "user"), 5, nil)
	for _, kind := range []string{"time-context", "todo-nudge", "skill-catalog", "runtime-context", "tool-jobs",
		"agent-instructions", "plugin", "agent-message", "goal", "subagent-settled", "memcue", "working-directory",
		"user-approval", "tool-goal", "compact-checkpoint", "repeat-tool-reminder"} {
		add("user/message", dshUserData(fmt.Sprintf("m%d", seq), "widget notice of "+kind, kind), seq, nil)
	}
	add("user/message", dshUserData("ns1", "<command-name>/widget</command-name>", ""), 22, nil)
	add("user/message", dshUserData("ns2", "plain widget text without source", ""), 23, nil)
	add("user/message", dshUserData("nt1", "виджет без времени", "user"), -1, nil)
	add("user/message", dshUserData("nt2", "widget zero time", "user"), 25, map[string]any{"time": 0})
	add("user/message", dshUserData("u1", "Как настроить виджет?", "user"), 1, nil)
	add("assistant/message", dshAssistantData("a2", []map[string]any{
		{"type": "reasoning", "text": "only a thought"},
		{"type": "tool-call", "name": "shell"}}), 27, nil)
	add("user/message", dshUserData("u3", "", "user"), 28, nil)
	add("user/message", map[string]any{"id": "u4", "role": "user", "source": map[string]any{"kind": "user"},
		"content": []map[string]any{{"type": "text", "text": "first widget part"},
			{"type": "text", "text": "second widget part"}}}, 29, nil)
	lines = append(lines, "not json at all")
	add("user/message", dshUserData("big", strings.Repeat("widget ", MaxLine/7+1), "user"), 30, nil)
	add("user/message", dshUserData("small", "small widget line after the big one", "user"), 31, nil)
	dshWrite(t, root, "-w-shop", "session-a", 4, lines...)
	dshWrite(t, root, "-w-old", "session-b", 3,
		dshSession(3, "session-b", 0),
		dshEntry("user/message", 1, dshUserData("b1", "widget of the older journal", "user"), 40, nil))
	dshWrite(t, root, "-w-sub", "session-sub", 4,
		dshSession(4, "session-sub", 1),
		dshEntry("user/message", 1, dshUserData("s1", "SUBAGENT-CANARY widget brief", "user"), 50, nil),
		dshEntry("assistant/message", 2, dshAssistantData("s2",
			[]map[string]any{{"type": "text", "text": "SUBAGENT-CANARY widget report"}}), 51, nil))
	dshWrite(t, root, "-w-unknown", "session-u", 5,
		dshSession(5, "session-u", 0),
		dshEntry("user/message", 1, dshUserData("x1", "widget of an unknown journal version", "user"), 60, nil))
	dshWrite(t, root, "-w-zz", "session-e", 4,
		dshSession(4, "session-e", 0),
		dshEntry("user/message", 1, dshUserData("u1", "widget repeated across files", "user"), 70, nil))
	dshWrite(t, root, "-w-fallback", "session-fallback-dir", 4,
		dshSession(4, "", 0),
		dshEntry("user/message", 7, dshUserData("", "widget fallback session", "user"), 80, nil))

	msgs, st, err := ReadDSH(root)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range msgs {
		texts = append(texts, m.Role+": "+m.Text)
	}
	want := []string{"user: widget fallback session", "user: widget of the older journal",
		"user: Как настроить виджет?", "assistant: Виджет настраивается через gizmo.", "user: виджет prompt",
		"user: plain widget text without source", "user: first widget part\nsecond widget part",
		"user: small widget line after the big one"}
	if !slices.Equal(texts, want) {
		t.Errorf("kept %q\nwant %q", texts, want)
	}
	if st.Files != 6 || st.Lines != 44 || st.Oversized != 1 || st.BadLines != 1 || st.NoTime != 2 ||
		st.Duplicates != 2 || st.Machine != 17 || st.Kept != 8 {
		t.Errorf("stats %+v", st)
	}
	if msgs[0].Session != "session-fallback-dir" || msgs[0].UID != "session-fallback-dir:7" || msgs[0].CWD != "/w" {
		t.Errorf("fallback fields %+v", msgs[0])
	}
	if msgs[2].Session != "session-a" || msgs[2].UID != "u1" || !msgs[2].Time.Equal(t0.Add(time.Second)) || msgs[2].CWD != "/w" {
		t.Errorf("fields %+v", msgs[2])
	}
	if msgs[3].Role != "assistant" || !msgs[3].Time.Equal(t0.Add(2*time.Second)) {
		t.Errorf("assistant fields %+v", msgs[3])
	}
	for _, m := range msgs {
		for _, canary := range []string{"secret", "TOKEN", "STREAM-CANARY", "SUBAGENT-CANARY", "widget notice",
			"unknown journal version", "system widget note", "repeated across files"} {
			if strings.Contains(m.Text, canary) {
				t.Errorf("machine text kept: %q", m.Text)
			}
		}
	}
}

// No journals yet is an empty history, not an error.
func TestReadDSHMissingRootIsEmpty(t *testing.T) {
	msgs, st, err := ReadDSH(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(msgs) != 0 || st.Files != 0 {
		t.Errorf("msgs %d, stats %+v, err %v", len(msgs), st, err)
	}
}

// dsh can keep the journal plaintext instead of zstd-compressed: the same records are read, with the same filters.
func TestReadDSHReadsAPlaintextJournal(t *testing.T) {
	root := t.TempDir()
	dshWritePlain(t, root, "-w-plain", "session-plain", 3,
		dshSession(3, "session-plain", 0),
		dshEntry("user/message", 1, dshUserData("p1", "the widget port is 8080", "user"), 1, nil),
		dshEntry("assistant/message", 2, dshAssistantData("p2",
			[]map[string]any{{"type": "text", "text": "widget port moved to 9090"}}), 2, nil),
		dshEntry("user/message", 3, dshUserData("p3", "widget notice of the plugin", "memcue"), 3, nil))
	msgs, st, err := ReadDSH(root)
	if err != nil {
		t.Fatal(err)
	}
	var uids []string
	for _, m := range msgs {
		uids = append(uids, m.UID)
	}
	if want := []string{"p1", "p2"}; !slices.Equal(uids, want) {
		t.Errorf("kept %v, want %v", uids, want)
	}
	if st.Files != 1 || st.Machine != 1 || st.Kept != 2 {
		t.Errorf("stats %+v", st)
	}
}

// A journal that is not valid zstd (a truncated frame of a session still being written) is counted and left out;
// the reading goes on with the rest instead of failing.
func TestReadDSHSkipsAJournalItCannotDecompress(t *testing.T) {
	root := t.TempDir()
	dshWrite(t, root, "-w-good", "session-good", 4,
		dshSession(4, "session-good", 0),
		dshEntry("user/message", 1, dshUserData("g1", "a widget message", "user"), 1, nil))
	bad := filepath.Join(root, "-w-bad", "session-bad", "session.v4.jsonl.zstd")
	if err := os.MkdirAll(filepath.Dir(bad), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("not a zstd frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, st, err := ReadDSH(root)
	if err != nil || len(msgs) != 1 || msgs[0].Text != "a widget message" {
		t.Fatalf("msgs %+v, err %v", msgs, err)
	}
	if st.Files != 1 || st.BadLines != 1 {
		t.Errorf("stats %+v", st)
	}
}

// dsh writes a frame per record, so the journal of a session still running can end mid-frame: the records that
// decoded stay in the history and only the damaged tail is counted.
func TestReadDSHKeepsTheRecordsBeforeADamagedTail(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-w-live", "session-live")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		dshSession(4, "session-live", 0),
		dshEntry("user/message", 1, dshUserData("l1", "the widget port is 8080", "user"), 1, nil),
		dshEntry("assistant/message", 2, dshAssistantData("l2",
			[]map[string]any{{"type": "text", "text": "widget port moved to 9090"}}), 2, nil),
		dshEntry("user/message", 3, dshUserData("l3", strings.Repeat("widget tail ", 40), "user"), 3, nil),
	}
	var whole []byte
	for _, line := range lines[:3] {
		whole = enc.EncodeAll([]byte(line+"\n"), whole)
	}
	tail := enc.EncodeAll([]byte(lines[3]+"\n"), nil)
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.v4.jsonl.zstd")
	if err := os.WriteFile(path, append(whole, tail[:len(tail)/2]...), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, st, err := ReadDSH(root)
	if err != nil {
		t.Fatal(err)
	}
	var uids []string
	for _, m := range msgs {
		uids = append(uids, m.UID)
	}
	if want := []string{"l1", "l2"}; !slices.Equal(uids, want) {
		t.Errorf("kept %v, want %v", uids, want)
	}
	if st.Files != 1 || st.BadLines < 1 {
		t.Errorf("stats %+v", st)
	}
}

// Search orders the kept messages newest first and leaves out the current session by the Session ids the reading
// returned, although the reading itself keeps the order of the file paths.
func TestReadDSHSearchNewestFirstOutsideTheCurrentSession(t *testing.T) {
	root := t.TempDir()
	dshWrite(t, root, "-w-one", "session-old", 4,
		dshSession(4, "session-old", 0),
		dshEntry("user/message", 1, dshUserData("o1", "the widget port is 8080", "user"), 1, nil))
	dshWrite(t, root, "-w-two", "session-mid", 3,
		dshSession(3, "session-mid", 0),
		dshEntry("assistant/message", 1, dshAssistantData("m1",
			[]map[string]any{{"type": "text", "text": "widget port moved to 9090"}}), 3, nil))
	dshWrite(t, root, "-w-three", "session-current", 4,
		dshSession(4, "session-current", 0),
		dshEntry("user/message", 1, dshUserData("c1", "widget port of the current session", "user"), 4, nil))
	dshWrite(t, root, "-w-four", "session-future", 4,
		dshSession(4, "session-future", 0),
		dshEntry("user/message", 1, dshUserData("f1", "widget port written after the query", "user"), 10, nil))
	msgs, _, err := ReadDSH(root)
	if err != nil {
		t.Fatal(err)
	}
	res := Search(msgs, "widget port", t0.Add(10*time.Second), map[string]bool{"session-current": true}, 5, 200)
	var uids []string
	for _, h := range res.Hits {
		uids = append(uids, h.Message.UID)
	}
	if want := []string{"m1", "o1"}; !slices.Equal(uids, want) {
		t.Errorf("hits %v, want %v", uids, want)
	}
	if res.Candidates != 2 || res.Future != 1 || res.Excluded != 1 {
		t.Errorf("result %+v", res)
	}
}
