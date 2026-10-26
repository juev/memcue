package vet

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/juev/memcue/internal/store"
)

// Known is what the running binary knows of itself. A fact about memcue that names a variable or a verb the binary no
// longer has, or a version older than its own, was likely written about a previous release; code can find such a fact
// but cannot tell a stale one from a record of history, so it only names the suspect.
type Known struct {
	Env      map[string]bool // MEMCUE_* variables the binary or its plugins read
	Commands map[string]bool // memcue verbs
	Version  string          // the binary's release version, X.Y.Z; empty (a dev build) skips the version signal
}

// Suspect is an approved fact code finds suspect, with each piece of evidence it found.
type Suspect struct {
	Fact     store.Fact
	Evidence []string
}

var (
	envName  = regexp.MustCompile(`MEMCUE_[A-Z0-9_]*[A-Z0-9]`)
	verbName = regexp.MustCompile("`memcue ([a-z][a-z-]*)")
	// A version right after the word, and a version a few words later when a word that carries it stands in between
	// ("memcue установлен на двух машинах: бинарь 0.1.2"). The second pattern needs that keyword, so a version of
	// another tool in the same sentence is not read as memcue's (#296). No \b before the keyword: Go's \b is
	// ASCII-only and never holds next to a Cyrillic letter.
	versionName  = regexp.MustCompile(`(?i)\bmemcue\s+v?(\d+)\.(\d+)\.(\d+)`)
	versionLabel = regexp.MustCompile(`(?i)\bmemcue\b[^;\n]{0,60}[\s:—–-](?:бинарь|бинар[а-я]*|верси[а-я]+|version|сборк[а-я]+|релиз[а-я]*|тег|tag)\s*:?\s*v?(\d+)\.(\d+)\.(\d+)`)
	// A reminder fact starts with the word; one that only mentions a scheduler or a branch named reminder is not one.
	reminder = regexp.MustCompile(`(?i)^\s*(напоминание|напомнить|reminder|remind)`)
	isoDate  = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	dotDate  = regexp.MustCompile(`\b(\d{1,2})\.(\d{1,2})\.(\d{4})\b`)
	// No word boundary after the month: Go's \b is ASCII-only and never holds after a Cyrillic letter.
	ruDate   = regexp.MustCompile(`\b(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)`)
	ruMonths = []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября",
		"ноября", "декабря"}
)

// Suspects finds the approved facts that name a MEMCUE_* variable or a `memcue X` verb the binary does not know, a
// memcue version older than its own, or a reminder whose dates have all passed by today.
func Suspects(facts []store.Fact, k Known, today time.Time) []Suspect {
	this := "this memcue"
	if k.Version != "" {
		this = "memcue " + k.Version
	}
	var out []Suspect
	for _, f := range facts {
		var ev []string
		for _, name := range unknownEnv(f.Content, k.Env) {
			ev = append(ev, fmt.Sprintf("names %s, which %s does not read", name, this))
		}
		for _, verb := range unknownVerbs(f.Content, k.Commands) {
			ev = append(ev, fmt.Sprintf("names `memcue %s`, not a command of %s", verb, this))
		}
		if old := olderVersion(f.Content, k.Version); old != "" {
			ev = append(ev, fmt.Sprintf("names memcue %s, older than %s", old, k.Version))
		}
		if last, ok := pastReminder(f, today); ok {
			ev = append(ev, fmt.Sprintf("a reminder whose last date, %s, has passed", last))
		}
		if len(ev) > 0 {
			out = append(out, Suspect{Fact: f, Evidence: ev})
		}
	}
	return out
}

// unknownEnv lists the MEMCUE_* names of text the binary does not know. A name followed by "_*" is a family, such as
// MEMCUE_VET_*, and is known when any known variable belongs to it.
func unknownEnv(text string, known map[string]bool) []string {
	var out []string
	for _, loc := range envName.FindAllStringIndex(text, -1) {
		name := text[loc[0]:loc[1]]
		if strings.HasPrefix(text[loc[1]:], "_*") {
			family := false
			for k := range known {
				family = family || strings.HasPrefix(k, name+"_")
			}
			if family {
				continue
			}
			name += "_*"
		} else if known[name] {
			continue
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func unknownVerbs(text string, known map[string]bool) []string {
	var out []string
	for _, m := range verbName.FindAllStringSubmatch(text, -1) {
		if !known[m[1]] && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// OutdatedInstall returns the oldest version an installation-state fact about memcue names when every version it names
// is older than current, or "": the fact records which version is installed, where a binary lies or what a config
// holds (installationState), and the releases it names are gone (#457). A fact of a knowledge kind, one that names an
// old version without describing an installation, and one that also names a version not older than current (another
// tool's, as likely as memcue's) return "". An empty current (a dev build) returns "".
func OutdatedInstall(f store.Fact, current string) string {
	cur, ok := parseVersion(current)
	if !ok || !stateKind(f.Kind) || installationState(f) == "" {
		return ""
	}
	s := strings.ToLower(f.Content)
	if !strings.Contains(s, "memcue") {
		return ""
	}
	oldest, found := [3]int{}, ""
	for _, at := range semVer.FindAllStringIndex(s, -1) {
		if !isVersion(s, at) {
			continue
		}
		v, ok := parseVersion(strings.TrimPrefix(s[at[0]:at[1]], "v"))
		if !ok || !less(v, cur) {
			return ""
		}
		if found == "" || less(v, oldest) {
			oldest, found = v, fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
		}
	}
	return found
}

// olderVersion returns the oldest memcue version text names that is older than current, or "". Both signals count: the
// version right after the word, and the one a version word carries a few words later.
func olderVersion(text, current string) string {
	cur, ok := parseVersion(current)
	if !ok {
		return ""
	}
	oldest, found := [3]int{}, ""
	for _, re := range []*regexp.Regexp{versionName, versionLabel} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			v, _ := parseVersion(m[1] + "." + m[2] + "." + m[3])
			if less(v, cur) && (found == "" || less(v, oldest)) {
				oldest, found = v, fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
			}
		}
	}
	return found
}

func parseVersion(s string) ([3]int, bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func less(a, b [3]int) bool { return slices.Compare(a[:], b[:]) < 0 }

// pastReminder reports a reminder whose dates are all before today, with the latest of them. A date without a year
// ("21 октября") takes the year the fact was written. A reminder with no date, or with one still ahead, is no suspect.
func pastReminder(f store.Fact, today time.Time) (string, bool) {
	if !reminder.MatchString(f.Content) {
		return "", false
	}
	var dates []time.Time
	add := func(y, m, d int) {
		t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.Local)
		if t.Year() == y && int(t.Month()) == m && t.Day() == d { // drop 31.02 and the like
			dates = append(dates, t)
		}
	}
	for _, m := range isoDate.FindAllStringSubmatch(f.Content, -1) {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		add(y, mo, d)
	}
	for _, m := range dotDate.FindAllStringSubmatch(f.Content, -1) {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		add(y, mo, d)
	}
	for _, m := range ruDate.FindAllStringSubmatch(f.Content, -1) {
		d, _ := strconv.Atoi(m[1])
		add(f.Created.Year(), slices.Index(ruMonths, m[2])+1, d)
	}
	if len(dates) == 0 {
		return "", false
	}
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	last := slices.MaxFunc(dates, func(a, b time.Time) int { return a.Compare(b) })
	if !last.Before(day) {
		return "", false
	}
	return last.Format(time.DateOnly), true
}
