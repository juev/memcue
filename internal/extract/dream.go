package extract

import (
	_ "embed"
	"strings"
)

// Action is one change a dream asks for: retire a fact, or correct it with new content.
type Action struct {
	Op      string `json:"op"` // "retire" or "correct"
	ID      string `json:"id"`
	Content string `json:"content,omitempty"`
	By      string `json:"by,omitempty"`   // retire: optional evidence for resolved, required for duplicate and outdated
	Kind    string `json:"kind,omitempty"` // retire: "duplicate", "outdated", "resolved" or "noise"
	Reason  string `json:"reason"`
}

//go:embed dream_packet.md
var packetPrompt string

//go:embed dream_prompt.md
var dreamPrompt string

//go:embed scope_prompt.md
var scopePrompt string

// Rescope moves a global fact that holds in one project to that project's scope (#607).
type Rescope struct {
	ID     string `json:"id"`
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}

// answerShape is the one answer a packet takes, with the packet id the model must echo.
const answerShape = "## Answer format\n\nReturn one JSON object. `packet` must be exactly `%s`.\n" +
	`{"packet": "%s","splits":[{"id":"…","parts":["…","…"],"reason":"…"}],` +
	`"actions":[{"op":"retire|correct","id":"…","content":"…","by":"…","kind":"duplicate|outdated|resolved|noise","reason":"…"}],` +
	`"scopes":[{"id":"…","scope":"…","reason":"…"}]}` + "\nOnly answer the supplied split, group and scope tasks. Empty lists are valid.\n"

// DreamInstructions is the text a dream packet starts with: what to do, the three tasks with their rules, and the
// form of the answer to packet id.
func DreamInstructions(id string) string {
	var b strings.Builder
	b.WriteString(packetPrompt)
	b.WriteString("\n## Task 1. Split compound facts\n\n" + splitPrompt)
	b.WriteString("\n## Task 2. Similar groups and noise candidates\n\n" + dreamPrompt)
	b.WriteString("\n## Task 3. Where a fact applies\n\n" + scopePrompt)
	b.WriteString("\n" + strings.ReplaceAll(answerShape, "%s", id))
	return b.String()
}
