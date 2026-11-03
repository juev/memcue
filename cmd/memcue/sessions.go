package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juev/memcue/internal/history"
)

// sessionsHalf is half the window a passage of history is cut to: 400 characters a passage, the size the #602
// measurement found an answer in for a third of the empty searches at about 2,000 characters a query.
const sessionsHalf = 200

// sessionsHeader opens the answer: the passages are history, not facts.
const sessionsHeader = "memcue sessions: passages of earlier sessions, newest first. They are history, not facts: " +
	"they may be out of date, so check a find against the current state, and store what holds with memcue add."

// runSessions is memcue sessions (#621): a word search over the text of this machine's Claude Code sessions, with
// no index, for what an empty memcue search may have missed: what the agent forgot to store is often still in a
// transcript. It reads the user's and the agent's text only (see history.ReadClaude), leaves out the current
// session and every message at or after -before, and prints the newest windows.
func runSessions(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	n := fs.Int("n", 5, "the most passages to return, one a message")
	before := fs.String("before", "", "only messages written before this time, RFC 3339 or YYYY-MM-DD (default: now)")
	cwd := fs.String("cwd", "", "only sessions that ran in this directory or under it (default: every session)")
	session := fs.String("session", os.Getenv("CLAUDE_CODE_SESSION_ID"),
		"session ids to leave out, comma-separated (default: the current session, $CLAUDE_CODE_SESSION_ID)")
	asJSON := fs.Bool("json", false, `print one JSON object, {"passages": [...], "terms": [...]}, instead of lines`)
	words, err := parseAnywhere(fs, args)
	if err != nil {
		return err
	}
	if *n < 1 {
		return errors.New("sessions: -n must be >= 1")
	}
	query := strings.Join(words, " ")
	if strings.TrimSpace(query) == "" {
		return errors.New("sessions: give the words to look for")
	}
	limit := time.Now()
	if *before != "" {
		if limit, err = parseBefore(*before); err != nil {
			return err
		}
	}
	dir := ""
	if *cwd != "" {
		if dir, err = filepath.Abs(*cwd); err != nil {
			return err
		}
	}
	msgs, _, err := history.ReadClaude(filepath.Join(claudeDir(), "projects"))
	if err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	if dir != "" {
		msgs = inDir(msgs, dir)
	}
	exclude := map[string]bool{}
	for _, id := range splitKeywords(*session) {
		exclude[id] = true
	}
	res := history.Search(msgs, query, limit, exclude, *n, sessionsHalf)
	if *asJSON {
		return writeSessionsJSON(out, res)
	}
	switch {
	case len(res.Terms) == 0:
		_, err = fmt.Fprintln(out, "memcue sessions: the query has no words to look for (stop words alone)")
	case len(res.Hits) == 0:
		_, err = fmt.Fprintf(out, "memcue sessions: nothing in the history holds %s\n", enough(res))
	default:
		lines := []string{sessionsHeader}
		for _, h := range res.Hits {
			lines = append(lines, fmt.Sprintf("- %s, %s in session %s: %s", h.Message.Time.Local().Format("2006-01-02 15:04"),
				h.Message.Role, shortID(h.Message.Session), h.Text))
		}
		_, err = fmt.Fprintln(out, strings.Join(lines, "\n"))
	}
	return err
}

// enough names the matching rule the search applied, for an empty answer.
func enough(res history.Result) string {
	if len(res.Terms) == 1 {
		return fmt.Sprintf("the word %q", res.Terms[0])
	}
	return fmt.Sprintf("%d of the words %s", res.MinMatch, strings.Join(res.Terms, ", "))
}

// parseBefore reads -before: an RFC 3339 time, or a day, which means its start in local time.
func parseBefore(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("sessions: -before %q is not an RFC 3339 time or a YYYY-MM-DD day", s)
}

// inDir keeps the messages of sessions that ran in dir or under it.
func inDir(msgs []history.Message, dir string) []history.Message {
	var out []history.Message
	for _, m := range msgs {
		if m.CWD == dir || strings.HasPrefix(m.CWD, dir+string(filepath.Separator)) {
			out = append(out, m)
		}
	}
	return out
}

// shortID is the first eight characters of a session id, the first group of a UUID: enough to tell sessions apart
// in a list.
func shortID(id string) string {
	if r := []rune(id); len(r) > 8 {
		return string(r[:8])
	}
	return id
}

// sessionsPassage is one passage in the JSON answer.
type sessionsPassage struct {
	Time    string `json:"time"`
	Session string `json:"session"`
	Role    string `json:"role"`
	Text    string `json:"text"`
	Matched int    `json:"matched"`
}

func writeSessionsJSON(out io.Writer, res history.Result) error {
	passages := make([]sessionsPassage, 0, len(res.Hits))
	for _, h := range res.Hits {
		passages = append(passages, sessionsPassage{Time: h.Message.Time.UTC().Format(time.RFC3339), Session: h.Message.Session,
			Role: h.Message.Role, Text: h.Text, Matched: h.Matched})
	}
	terms := res.Terms
	if terms == nil {
		terms = []string{}
	}
	return json.NewEncoder(out).Encode(struct {
		Passages []sessionsPassage `json:"passages"`
		Terms    []string          `json:"terms"`
		MinMatch int               `json:"min_match"`
		Header   string            `json:"header"`
	}{passages, terms, res.MinMatch, sessionsHeader})
}
