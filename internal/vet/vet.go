// Package vet tells installation state from knowledge: the check add runs on a fact the agent writes itself (#475,
// #494). A version, the path of a binary, a config default, the size of a base or the moment of a restart is read by a
// command and stale after the next release or restart, so it is not stored; a rule, a decision or a pitfall that only
// names such a value is.
package vet

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/juev/memcue/internal/store"
)

// The signals of installation state (#426). Each one fires only together with a word near it, so a fact that merely
// names a version or a path is left alone. The text is lower-cased before matching.
var (
	// semVer is a release number such as 1.2.3 or v0.40.1; an address such as 10.0.0.1 is filtered out by the caller.
	semVer = regexp.MustCompile(`v?\d+\.\d+\.\d+`)
	// installWord says the number is the version of something installed, not a value of some other kind.
	installWord = regexp.MustCompile(`верси|version|установлен|installed|бинар|binar|обёртк|обертк|wrapper|релиз|release|тег|tag|сборк|build`)
	// installPath is a place binaries, wrappers and plugins are installed to: the part of a base that carries the
	// tool itself. A service's directory or a data path is knowledge a command does not give, so it is not here.
	installPath = regexp.MustCompile(`/(?:bin|plugins)/`)
	// placeWord says the path is where something lies, not an argument of a command.
	placeWord = regexp.MustCompile(`лежит|лежат|находится|хранится|пут[ьи]|path|stored`)
	// defaultWord marks a config default.
	defaultWord = regexp.MustCompile(`по умолчанию|by default|default`)
	// configKey is a dotted key such as memory.memcue.search_facts; a file name such as config.yaml is filtered out by
	// the caller.
	configKey = regexp.MustCompile(`[a-z_][a-z0-9_]*(?:\.[a-z_][a-z0-9_]*)+`)
	// fileExt is the last part of a dotted name that makes it a file, not a config key.
	fileExt = regexp.MustCompile(`^(?:ya?ml|json|toml|ini|conf|env|log|md|txt|go|py|sh|js|ts|db|sqlite)$`)
	// hostExt is the last part of a dotted name that makes it a host name (a private forge, a site), not a key.
	hostExt = regexp.MustCompile(`^(?:com|org|net|io|ru|de|me|co|ai|app|cloud|dev|local|internal|online|site|tech|xyz|info)$`)
	// baseSize is the size of a base or the count of its facts.
	baseSize = regexp.MustCompile(`живых фактов|фактов в базе|live facts|facts in the base|размер базы|занимает \d+(?:[.,]\d+)? ?(?:кб|мб|гб|kb|mb|gb)`)
	// clock and pid are the time and the process of a start; restartWord says a start happened, so a schedule ("runs
	// at 03:00") is not one (#475).
	clock       = regexp.MustCompile(`\b\d{1,2}:\d{2}(?::\d{2})?\b`)
	pid         = regexp.MustCompile(`\bpid\s*\d+`)
	restartWord = regexp.MustCompile(`перезапущен|restarted`)
	// lineCount is the size of a file at one moment, "(419 lines)"; a limit such as "reads 2000 lines" is not in
	// brackets.
	lineCount = regexp.MustCompile(`\(\d+ (?:lines?|строк[аи]?)\)`)
	// mtime is when a file last changed.
	mtime = regexp.MustCompile(`mtime \d|не менял\p{L}* с \d|(?:has not|hasn't) changed since|not modified since|unchanged since`)
)

// nearRunes is how far, in characters either side, a signal looks for the word that confirms it.
const nearRunes = 60

// State names the installation state a fact only records (installationState), or returns "" when the fact is of a
// knowledge kind or records none.
func State(f store.Fact) string {
	if !stateKind(f) {
		return ""
	}
	part := installationState(f)
	if strings.EqualFold(strings.TrimSpace(f.Kind), "pitfall") {
		// A pitfall names a wrapper's path or a config key as its cause, so only a version and a moment are state in
		// one: on a live base the path and size signals took two real pitfalls of six they fired on.
		switch part {
		case "path", "config default", "size":
			return ""
		}
	}
	return part
}

// stateKind reports whether a fact can be mere installation state: knowledge kinds never are. A pitfall's label is
// trusted when it rests on the user's words or a tool's output: one with no source predates the policy, and one the
// agent concluded has nothing behind its label (#475).
func stateKind(f store.Fact) bool {
	switch strings.ToLower(strings.TrimSpace(f.Kind)) {
	case "", "environment", "other":
		return true
	case "pitfall":
		return f.Source == "" || f.Source == "agent"
	}
	return false
}

// installationState reports whether a fact only records the state of an installation: the version of something
// installed, the path of a binary or a wrapper, the variables a wrapper exports, a config default, the size and
// the fact count of a base, or the moment of a process or a file — a restart time or PID, a line count, an mtime
// (#475). A command reads each of these (#426), and the stored copy is stale after the next release or restart; a
// rule, a decision or a pitfall that merely names a version is not this.
//
// It names the part found — version, path, config default, size, restart, line count or mtime — or returns "" when
// there is none. When in doubt it returns "": a wrong refusal loses knowledge.
func installationState(c store.Fact) string {
	s := strings.ToLower(c.Content)
	switch {
	case near(s, semVer, installWord, isVersion):
		return "version"
	case near(s, installPath, placeWord, nil) || near(s, installPath, installWord, nil):
		return "path"
	case near(s, configKey, defaultWord, isConfigKey):
		return "config default"
	case baseSize.MatchString(s):
		return "size"
	case near(s, clock, restartWord, nil) || near(s, pid, restartWord, nil):
		return "restart"
	case lineCount.MatchString(s):
		return "line count"
	case mtime.MatchString(s):
		return "mtime"
	}
	return ""
}

// near reports whether a match of sig, accepted by ok when ok is set, has a match of word within nearRunes
// characters either side of it.
func near(s string, sig, word *regexp.Regexp, ok func(s string, at []int) bool) bool {
	for _, at := range sig.FindAllStringIndex(s, -1) {
		if ok != nil && !ok(s, at) {
			continue
		}
		if word.MatchString(s[back(s, at[0], nearRunes):ahead(s, at[1], nearRunes)]) {
			return true
		}
	}
	return false
}

// isVersion rejects a number that is part of a longer dotted one or of a word: 10.0.0.1 is an address, not 10.0.0.
func isVersion(s string, at []int) bool {
	if at[0] > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:at[0]]); r == '.' || isWord(r) {
			return false
		}
	}
	if at[1] < len(s) {
		r, _ := utf8.DecodeRuneInString(s[at[1]:])
		if isWord(r) || r == '.' && at[1]+1 < len(s) && s[at[1]+1] >= '0' && s[at[1]+1] <= '9' {
			return false
		}
	}
	return true
}

// isConfigKey rejects a dotted name that is a file (config.yaml) or a part of a path or a longer word.
func isConfigKey(s string, at []int) bool {
	if at[0] > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:at[0]]); r == '/' || r == '.' || r == '-' || isWord(r) {
			return false
		}
	}
	key := s[at[0]:at[1]]
	ext := key[strings.LastIndexByte(key, '.')+1:]
	return !fileExt.MatchString(ext) && !hostExt.MatchString(ext)
}

// back returns the byte offset n characters before i in s, or 0.
func back(s string, i, n int) int {
	for ; n > 0 && i > 0; n-- {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return i
}

// ahead returns the byte offset n characters after i in s, or len(s).
func ahead(s string, i, n int) int {
	for ; n > 0 && i < len(s); n-- {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return i
}

// isWord says what keeps a word together: letters of any script, digits and the underscore. Everything else is a
// separator, so punctuation and dashes fold away the same way they do in the index.
func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
