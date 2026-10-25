// Package transcript turns a Claude Code session transcript (JSON lines) into plain text a model can read: what
// the user wrote, what the agent answered, which tools it ran and, cut short, what they returned. Thinking,
// subagent lines, injected meta messages and system reminders are left out.
package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Limits on what one entry contributes; a session's knowledge is in what was said and found, not in full outputs.
const (
	maxToolInput  = 400
	maxToolResult = 1500
	maxLine       = 64 << 20
)

// Session is a rendered transcript.
type Session struct {
	Text  string
	CWD   string // the working directory of the first entry that names one
	Start string // date of the first entry, YYYY-MM-DD
	Lines int    // lines read, the offset for the next run
}

var reReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

type entry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	IsSummary   bool   `json:"isCompactSummary"`
	CWD         string `json:"cwd"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	ToolUseID string          `json:"tool_use_id"`
}

// Read renders the transcript from line `from` (0-based) on.
func Read(r io.Reader, from int) (Session, error) {
	var s Session
	var b strings.Builder
	var open calls
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), maxLine)
	for sc.Scan() {
		s.Lines++
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if s.CWD == "" && e.CWD != "" {
			s.CWD = e.CWD
		}
		if s.Lines <= from || e.IsSidechain || e.IsMeta || e.IsSummary || (e.Type != "user" && e.Type != "assistant") {
			continue
		}
		if s.Start == "" && len(e.Timestamp) >= 10 {
			s.Start = e.Timestamp[:10]
		}
		render(&b, e, &open)
	}
	s.Text = b.String()
	return s, sc.Err()
}

func render(b *strings.Builder, e entry, open *calls) {
	var str string
	if json.Unmarshal(e.Message.Content, &str) == nil {
		if e.Type == "user" {
			say(b, "USER", str)
		} else {
			say(b, "ASSISTANT", str)
		}
		return
	}
	var blocks []block
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return
	}
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			if e.Type == "user" {
				say(b, "USER", bl.Text)
			} else {
				say(b, "ASSISTANT", bl.Text)
			}
		case "tool_use":
			open.add(bl.ID, bl.Name)
			say(b, "TOOL "+bl.Name, toolInput(bl.Name, bl.Input))
		case "tool_result":
			say(b, resultLabel(open.take(bl.ToolUseID)), cut(resultText(bl.Content), maxToolResult))
		}
	}
}

// calls are the tool calls of the turn whose results have not come yet, in the order they were made.
type calls []call

type call struct{ id, name string }

func (c *calls) add(id, name string) { *c = append(*c, call{id, name}) }

// take returns the name of the call a result answers: the one with its id, or the oldest open one when the result
// names none or an unknown one. A result with no open call has no known tool.
func (c *calls) take(id string) string {
	i := slices.IndexFunc(*c, func(x call) bool { return id != "" && x.id == id })
	if i < 0 {
		if len(*c) == 0 {
			return ""
		}
		i = 0
	}
	name := (*c)[i].name
	*c = slices.Delete(*c, i, i+1)
	return name
}

// resultLabel is the block label of a tool result: RESULT and the name of the tool that produced it, so the record
// says where a quote came from (#433); the bare RESULT when the tool is not known. Signs that would end the label
// early or break its line are left out of the name.
func resultLabel(tool string) string {
	tool = strings.Map(func(r rune) rune {
		if r == ':' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, tool)
	if tool == "" {
		return "RESULT"
	}
	return "RESULT " + tool
}

// toolInput is what a tool call shows: the file alone for an edit, whose content is in the repository and teaches
// nothing a fact should keep; the input cut short for the rest.
func toolInput(name string, raw json.RawMessage) string {
	switch name {
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		var in struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		}
		if json.Unmarshal(raw, &in) == nil {
			return in.FilePath + in.NotebookPath
		}
	}
	return cut(string(raw), maxToolInput)
}

// resultText is a tool result's text: a plain string or the text blocks of a list.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []block
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func say(b *strings.Builder, who, text string) {
	text = strings.TrimSpace(reReminder.ReplaceAllString(text, ""))
	if text == "" {
		return
	}
	fmt.Fprintf(b, "%s: %s\n\n", who, reMarker.ReplaceAllString(text, " $1"))
}

// reMarker finds a line of a message or tool output that starts the way a block of the record does. say indents it,
// so text a tool returned (a web page, a file) cannot open a block of its own, such as a USER block that would pass
// for the user's words.
var reMarker = regexp.MustCompile(`(?m)^((?:USER|ASSISTANT|RESULT(?: [^:\n]*)?|TOOL [^:\n]*): )`)

// cut keeps the head and the tail of a long text: errors and conclusions tend to sit at either end.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	h := n * 2 / 3
	return string(r[:h]) + fmt.Sprintf("\n[… %d chars cut …]\n", len(r)-n) + string(r[len(r)-(n-h):])
}

// Message is one chat message in the OpenAI shape, as agents such as Hermes keep their sessions. A tool message may
// name its tool and the call it answers; without them it answers the assistant's calls in order.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// RenderMessages renders chat messages the way Read renders a Claude Code transcript; system messages are left out.
func RenderMessages(msgs []Message) string {
	var b strings.Builder
	var open calls
	for _, m := range msgs {
		text := resultText(m.Content)
		switch m.Role {
		case "user":
			say(&b, "USER", text)
		case "assistant":
			say(&b, "ASSISTANT", text)
			open = open[:0]
			for _, c := range m.ToolCalls {
				open.add(c.ID, c.Function.Name)
				say(&b, "TOOL "+c.Function.Name, toolInput(c.Function.Name, json.RawMessage(c.Function.Arguments)))
			}
		case "tool":
			name := open.take(m.ToolCallID)
			if m.Name != "" {
				name = m.Name
			}
			say(&b, resultLabel(name), cut(text, maxToolResult))
		}
	}
	return b.String()
}
