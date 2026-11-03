// Package history searches the text of earlier sessions of this machine for the words of a query, with no index
// (#621): what the agent forgot to store is often still in a transcript. It is the parser of the #602 measurement
// (docs/research/scripts/history602/sessions.py) as a command: the same filters, matching rule and windows, so the
// measured numbers describe what ships.
package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Message is one text of a session: what the user or the agent wrote.
type Message struct {
	UID     string
	Session string
	Role    string // user or assistant
	Text    string
	Time    time.Time
	CWD     string
}

// Stats counts what a reading of the transcripts kept and left out.
type Stats struct {
	Files      int
	Lines      int
	BadLines   int
	Oversized  int // lines over MaxLine bytes, skipped whole
	NoTime     int
	Machine    int // text a program wrote: command echoes, summaries, skill bodies, prompts of a delegating agent
	Duplicates int
	Kept       int
}

// MaxLine is the longest transcript line read; a longer one is skipped and counted. A line holds a whole tool
// result, and an 8 MiB one is a pasted file, not a message.
const MaxLine = 8 << 20

// reminder is the system text Claude Code embeds in a user message; it is not what the user wrote.
var reminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// machineStarts open a user message a program wrote, not the user: a command echo, a task notification, the
// continuation summary of a compacted session, the body of a skill.
var machineStarts = []string{"<command-name>", "<command-message>", "<command-args>", "<local-command-stdout>",
	"<local-command-stderr>", "<local-command-caveat>", "<task-notification>", "<bash-input>", "<bash-stdout>",
	"<bash-stderr>", "<user-prompt-submit-hook>", "Caveat: The messages below",
	"This session is being continued from a previous conversation", "[Request interrupted",
	"Base directory for this skill:"}

// line is what a transcript entry carries that the reading needs.
type line struct {
	Type       string `json:"type"`
	UUID       string `json:"uuid"`
	SessionID  string `json:"sessionId"`
	Timestamp  string `json:"timestamp"`
	CWD        string `json:"cwd"`
	Entrypoint string `json:"entrypoint"`
	Sidechain  bool   `json:"isSidechain"`
	Meta       bool   `json:"isMeta"`
	Compact    bool   `json:"isCompactSummary"`
	Transcript bool   `json:"isVisibleInTranscriptOnly"`
	Message    struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// block is one part of a message's content.
type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ReadClaude reads the user's and the agent's text of every Claude Code transcript under root, the projects
// directory of the Claude config directory, in the order of the file paths. Left out: thinking, tool input and
// output, skill bodies, command echoes, task notifications, compaction summaries, embedded system reminders, the
// briefs of subagents (sidechains) and the prompts of claude -p sessions, which a delegating agent wrote. Tool output
// carries secrets and fetched pages and is never read. An entry without a time cannot be ordered and is dropped.
// A reading of the author's 2.5 GB of transcripts takes about a second and a half on a laptop; a prefilter of the raw
// lines by the words of the query did not make it faster, since lowercasing the lines costs as much as parsing them.
func ReadClaude(root string) ([]Message, Stats, error) {
	var st Stats
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return nil // no transcripts yet: nothing in the history
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, st, err
	}
	sort.Strings(paths)
	// The files are read at once, as many as there are processors: a reading is bound by the parsing of a few
	// gigabytes of lines, and the files are independent. The messages keep the order of the paths, and an entry
	// repeated across files counts once, the first in that order.
	type read struct {
		msgs []Message
		st   Stats
		err  error
	}
	reads := make([]read, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(1, min(runtime.NumCPU(), 8)))
	for i, path := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			reads[i].msgs, reads[i].err = readFile(path, &reads[i].st)
		}()
	}
	wg.Wait()
	var out []Message
	seen := map[string]bool{}
	for _, r := range reads {
		if r.err != nil {
			return nil, st, r.err
		}
		st.add(r.st)
		for _, m := range r.msgs {
			if seen[m.UID] {
				st.Duplicates++
				st.Kept--
				continue
			}
			seen[m.UID] = true
			out = append(out, m)
		}
	}
	return out, st, nil
}

func (s *Stats) add(o Stats) {
	s.Files += o.Files
	s.Lines += o.Lines
	s.BadLines += o.BadLines
	s.Oversized += o.Oversized
	s.NoTime += o.NoTime
	s.Machine += o.Machine
	s.Duplicates += o.Duplicates
	s.Kept += o.Kept
}

// readFile reads one transcript whole and walks its lines in place: a line is a slice of the file, so a transcript
// of tool results costs one read and no copy a line.
func readFile(path string, st *Stats) ([]Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	st.Files++
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	var out []Message
	seen := map[string]bool{}
	entry := ""
	for len(data) > 0 {
		raw := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			raw, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		st.Lines++
		if len(raw) > MaxLine {
			st.Oversized++
		} else if m, ok := parse(raw, stem, &entry, seen, st); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// parse reads one transcript line into a message, or says why not through the stats. entry keeps the entrypoint of
// the file, which a transcript names once at the start.
func parse(raw []byte, stem string, entry *string, seen map[string]bool, st *Stats) (Message, bool) {
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		st.BadLines++
		return Message{}, false
	}
	if l.Entrypoint != "" {
		*entry = l.Entrypoint
	}
	if l.Type != "user" && l.Type != "assistant" {
		return Message{}, false
	}
	texts := textBlocks(l.Message.Content)
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
	if l.Compact || l.Meta || l.Transcript {
		st.Machine++
		return Message{}, false
	}
	text := strings.TrimSpace(reminder.ReplaceAllString(strings.Join(texts, "\n"), ""))
	if text == "" {
		return Message{}, false
	}
	if l.Type == "user" && (l.Sidechain || *entry == "sdk-cli" || machine(text)) {
		st.Machine++
		return Message{}, false
	}
	session := l.SessionID
	if session == "" {
		session = stem
	}
	st.Kept++
	return Message{UID: uid, Session: session, Role: l.Type, Text: text, Time: t.UTC(), CWD: l.CWD}, true
}

// machine says whether a user message opens with the text of a program.
func machine(text string) bool {
	for _, p := range machineStarts {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// textBlocks is the text of a message's content: the string itself, or its text blocks. A tool_result, a tool_use
// and a thinking block carry no text of the user or the agent.
func textBlocks(content json.RawMessage) []string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return []string{s}
	}
	var blocks []block
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			out = append(out, b.Text)
		}
	}
	return out
}
