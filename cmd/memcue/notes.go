package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/juev/memcue/internal/store"
)

// noteEntry is one entry of a built-in memory note file and the live fact that already holds it, if any.
type noteEntry struct {
	File  string `json:"file"`
	Index int    `json:"index"`
	Chars int    `json:"chars"`
	Fact  string `json:"fact"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	Text  string `json:"text"`
}

// notesReport is what `memcue notes -json` prints.
type notesReport struct {
	Entries      []noteEntry `json:"entries"`
	Already      int         `json:"already"`
	Total        int         `json:"total"`
	CharsAlready int         `json:"chars_already"`
	CharsTotal   int         `json:"chars_total"`
}

// noteSeparator splits a note file into entries the way the Hermes plugin reads MEMORY.md and USER.md: a blank line or
// a §.
var noteSeparator = regexp.MustCompile(`\n\s*\n|§`)

// noteWord is a word of the text comparison: a run of Latin or Cyrillic letters and digits.
var noteWord = regexp.MustCompile(`[0-9A-Za-zА-Яа-яЁё]+`)

// runNotes reports which entries of the built-in memory note files are already facts of the base (#489): the mirror
// copies every note, so an entry the base holds is a second copy in the block the harness injects at every turn. It
// never writes the base and calls no model.
func runNotes(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("notes", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory of the note files (default MEMCUE_MEMORY_DIR, else the memory directory of the Claude config)")
	asJSON := fs.Bool("json", false, "print one JSON object instead of lines")
	minShare := fs.Float64("min-share", 0, "share of the words two texts must hold in common, of both and of the shorter (default 0: 0.30 and 0.40)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		*dir = os.Getenv("MEMCUE_MEMORY_DIR")
	}
	if *dir == "" {
		*dir = filepath.Join(claudeDir(), "memory")
	}
	entries, err := readNoteEntries(*dir)
	if err != nil {
		return err
	}
	facts, err := liveFacts()
	if err != nil {
		return err
	}
	factWords := make([]map[string]bool, len(facts))
	for i, f := range facts {
		factWords[i] = noteTokens(f.Content)
	}
	r := notesReport{Entries: entries, Total: len(entries)}
	for i := range r.Entries {
		e := &r.Entries[i]
		r.CharsTotal += e.Chars
		ew := noteTokens(e.Text)
		best := -1
		bestShared := 0
		for j, fw := range factWords {
			if !sameText(ew, fw, *minShare) {
				continue
			}
			if n := sharedWords(ew, fw); n > bestShared {
				best, bestShared = j, n
			}
		}
		if best < 0 {
			continue
		}
		e.Fact, e.Kind, e.Scope = facts[best].ID, facts[best].Kind, facts[best].Scope
		r.Already++
		r.CharsAlready += e.Chars
	}
	if *asJSON {
		return json.NewEncoder(out).Encode(r)
	}
	return printNotes(out, r)
}

func printNotes(out io.Writer, r notesReport) error {
	unset := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	for _, e := range r.Entries {
		head := truncateRunes(strings.Join(strings.Fields(e.Text), " "), 60)
		var err error
		if e.Fact != "" {
			_, err = fmt.Fprintf(out, "ok %s %s/%s %s\n", e.Fact, unset(e.Kind), unset(e.Scope), head)
		} else {
			_, err = fmt.Fprintf(out, "new %dch %s\n", e.Chars, head)
		}
		if err != nil {
			return err
		}
	}
	missing := r.Total - r.Already
	if _, err := fmt.Fprintf(out, "%d entries: %d already facts (%d of %d characters), %d not in the base\n",
		r.Total, r.Already, r.CharsAlready, r.CharsTotal, missing); err != nil {
		return err
	}
	if missing > 0 {
		_, err := fmt.Fprintln(out, `move them with: memcue add -kind <kind> -scope <scope> -source <user for the user's words, else tool or agent> -- "<text>"`)
		return err
	}
	return nil
}

// readNoteEntries reads every *.md file directly inside dir, MEMORY.md included (findNotes leaves it out for migrate),
// and splits each into its entries. A missing directory, and one without a single entry, are errors: the report
// would say nothing.
func readNoteEntries(dir string) ([]noteEntry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("notes directory: %w", err)
	}
	var entries []noteEntry
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, err
		}
		index := 0
		for _, part := range noteSeparator.Split(string(data), -1) {
			text := strings.TrimSpace(part)
			if text == "" {
				continue
			}
			index++
			entries = append(entries, noteEntry{File: f.Name(), Index: index, Chars: utf8.RuneCountInString(text), Text: text})
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no note entries in the *.md files of %s", dir)
	}
	return entries, nil
}

// liveFacts are the facts recall uses: the newest approved version of each, expired ones left out. A base that does
// not exist yet has none; it is not opened, so the report does not create it.
func liveFacts() ([]store.Fact, error) {
	if !factsBase().exists() {
		return nil, nil
	}
	s, err := openBaseReadOnly()
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	all, err := s.Active()
	if err != nil {
		return nil, err
	}
	today := store.Today()
	live := all[:0]
	for _, f := range all {
		if !store.Expired(f.Expires, today) {
			live = append(live, f)
		}
	}
	return live, nil
}

// noteTokens are the words two texts are compared by: lower-cased, words of more than three letters, each cut to five,
// which folds the endings of a Russian word together.
func noteTokens(text string) map[string]bool {
	words := map[string]bool{}
	for _, w := range noteWord.FindAllString(strings.ToLower(text), -1) {
		if utf8.RuneCountInString(w) > 3 {
			words[truncateRunes(w, 5)] = true
		}
	}
	return words
}

func sharedWords(a, b map[string]bool) int {
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return n
}

// sameText says whether two sets of words say the same thing: at least five shared words, and the shared ones a share
// of both of at least 0.30 or of the shorter one of at least 0.40, the thresholds the Hermes plugin's card filter
// measured until #456 removed it. A minShare above zero takes the place of both shares.
func sameText(a, b map[string]bool, minShare float64) bool {
	shared := sharedWords(a, b)
	if shared < 5 {
		return false
	}
	union, shorter := 0.30, 0.40
	if minShare > 0 {
		union, shorter = minShare, minShare
	}
	return float64(shared)/float64(len(a)+len(b)-shared) >= union ||
		float64(shared)/float64(min(len(a), len(b))) >= shorter
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
