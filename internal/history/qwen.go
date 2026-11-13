package history

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// qwenHookContext is the text a UserPromptSubmit hook appends to a user message, in a part of its own; it is not
// what the user wrote.
var qwenHookContext = regexp.MustCompile(`(?s)<qwen:user-prompt-submit-context>.*?</qwen:user-prompt-submit-context>`)

// qwenLine is one line of a Qwen Code transcript. The text of a message lies in message.parts, the Gemini format:
// a part carries text, a thought (the thinking of the agent), a functionCall or a functionResponse.
type qwenLine struct {
	Type       string `json:"type"`
	UUID       string `json:"uuid"`
	SessionID  string `json:"sessionId"`
	Timestamp  string `json:"timestamp"`
	CWD        string `json:"cwd"`
	Provenance string `json:"provenance"`
	Message    struct {
		Parts []qwenPart `json:"parts"`
	} `json:"message"`
}

type qwenPart struct {
	Text    string `json:"text"`
	Thought bool   `json:"thought"`
}

// ReadQwen reads the user's and the agent's text of every Qwen Code transcript under root, the projects directory
// of the Qwen config directory (a transcript lies in <project>/chats/<session>.jsonl), in the order of the file
// paths. Left out: thinking parts, tool calls and results (a tool_result record), system records, hook context and
// system reminders inside a message, user text a program wrote (a provenance other than real_user, or a machine
// opening when the provenance is absent), and entries without a time. Tool output carries secrets and fetched
// pages and is never read. The sidecar files of a chat (.title.json, .runtime.json) are not transcripts.
func ReadQwen(root string) ([]Message, Stats, error) {
	return readTranscripts(root, qwenReadFile)
}

func qwenReadFile(path string, st *Stats) ([]Message, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // deleted since the walk
	}
	if err != nil {
		return nil, err
	}
	st.Files++
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	seen := map[string]bool{}
	return eachLine(data, st, func(raw []byte) (Message, bool) {
		return qwenParse(raw, stem, seen, st)
	}), nil
}

// qwenParse reads one transcript line into a message, or says why not through the stats.
func qwenParse(raw []byte, stem string, seen map[string]bool, st *Stats) (Message, bool) {
	var l qwenLine
	if err := json.Unmarshal(raw, &l); err != nil {
		st.BadLines++
		return Message{}, false
	}
	if l.Type != "user" && l.Type != "assistant" {
		return Message{}, false
	}
	var texts []string
	for _, p := range l.Message.Parts {
		if p.Text != "" && !p.Thought {
			texts = append(texts, p.Text)
		}
	}
	if len(texts) == 0 {
		return Message{}, false
	}
	uid := l.UUID
	if uid == "" {
		uid = stem + ":" + string(raw[:min(len(raw), 64)])
	}
	if seen[uid] {
		st.Duplicates++
		return Message{}, false
	}
	seen[uid] = true
	t, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		st.NoTime++
		return Message{}, false
	}
	text := strings.TrimSpace(qwenHookContext.ReplaceAllString(reminder.ReplaceAllString(strings.Join(texts, "\n"), ""), ""))
	if text == "" {
		return Message{}, false
	}
	if l.Type == "user" {
		// The provenance names the writer of a user message: real_user is the user, anything else a program.
		// A transcript without the field falls back to the machine openings, as the Claude reading does.
		if (l.Provenance != "" && l.Provenance != "real_user") || (l.Provenance == "" && machine(text)) {
			st.Machine++
			return Message{}, false
		}
	}
	session := l.SessionID
	if session == "" {
		session = stem
	}
	st.Kept++
	return Message{UID: uid, Session: session, Role: l.Type, Text: text, Time: t.UTC(), CWD: l.CWD}, true
}
