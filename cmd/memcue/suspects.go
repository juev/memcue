package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/vet"
)

// knownEnv is every MEMCUE_* variable memcue and its plugins read; a fact naming another one was likely written about a
// release that had it (#289). TestKnownEnvCoversTheSource checks it against the os.Getenv calls of the source.
var knownEnv = []string{
	"MEMCUE_API_KEY", "MEMCUE_API_URL", "MEMCUE_DREAM_EVERY", "MEMCUE_EVENT_TTL_DAYS", "MEMCUE_EXTRACT", "MEMCUE_FACTS",
	"MEMCUE_HOST",
	"MEMCUE_MEMORY_DIR", "MEMCUE_MODEL",
	"MEMCUE_NUMBER_TERMS",
	"MEMCUE_SEARCH_LOG", "MEMCUE_STATE_DIR",
	"MEMCUE_USAGE_LOG", "MEMCUE_VET", "MEMCUE_VET_POLICY", "MEMCUE_VET_TTL_DAYS",
	// read by the plugins only
	"MEMCUE_AUTO_INSTALL", "MEMCUE_BIN", "MEMCUE_RELEASE_URL",
}

// suspectBatch is how many suspects one model call reads.
const suspectBatch = 20

// suspectFn is the model step of the suspects, replaced in tests.
var suspectFn = extract.ReviewSuspects

var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// knownSelf is what this binary knows of itself: its variables, the verbs its help lists, and its release version (empty
// for a dev build).
func knownSelf() vet.Known {
	k := vet.Known{Env: map[string]bool{}, Commands: map[string]bool{}}
	for _, e := range knownEnv {
		k.Env[e] = true
	}
	inCommands := false
	for line := range strings.SplitSeq(usage, "\n") {
		switch {
		case line == "commands:":
			inCommands = true
		case inCommands && !strings.HasPrefix(line, "  "):
			inCommands = false
		case inCommands:
			if f := strings.Fields(line); len(f) > 0 {
				k.Commands[f[0]] = true
			}
		}
	}
	if releaseVersion.MatchString(version) {
		k.Version = version
	}
	return k
}

// suspectInput is one suspect as the model reads it.
type suspectInput struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Origin   string   `json:"origin,omitempty"`
	Created  string   `json:"created"`
	Evidence []string `json:"evidence"`
}

// vetSuspects asks the main model about the approved facts this binary finds suspect, and retires those it says
// describe a past state as current (#289). A fact asked about is remembered with its version and the binary's version
// in vet.checked, so a kept fact is not asked about again until its text or the binary changes. Without the model it
// only lists the suspects; a dry run asks but changes and remembers nothing; a failed call remembers nothing either.
func vetSuspects(s *store.Store, o vetOptions, now time.Time) ([]vetLine, vetRecord) {
	rec := vetRecord{Time: now.UTC().Format(time.RFC3339), DryRun: o.dry}
	approved, err := s.Active()
	if err != nil {
		rec.Error = err.Error()
		return nil, rec
	}
	dir, err := stateDir()
	if err != nil {
		rec.Error = err.Error()
		return nil, rec
	}
	checkedPath := filepath.Join(dir, "vet.checked")
	checked, err := readChecked(checkedPath)
	if err != nil {
		rec.Error = err.Error()
		return nil, rec
	}
	var lines []vetLine
	// A fact stored before the write gave every fact a scope is in no place a search asks for (#442): it gets the
	// scope its origin names, the one it would get written today. A dry run only lists them.
	for i, f := range approved {
		if strings.TrimSpace(f.Scope) != "" {
			continue
		}
		scope := defaultScope("", f.Origin)
		if !o.dry {
			g, err := s.Relabel(f.ID, f.Version, f.Kind, scope, f.Expires)
			if err != nil {
				rec.Error = err.Error()
				continue
			}
			approved[i] = g
		}
		rec.Scoped++
		lines = append(lines, vetLine{Time: rec.Time, ID: f.ID, Decision: "scoped", Verdict: string(vet.Keep),
			By: "rule", Reason: "no scope: " + scope + " from the origin", Source: f.Source, Content: clip(f.Content, 200)})
	}
	// Three kinds of fact no longer hold, and code can tell them without a model, so they are retired by rule:
	// - a mirror of the built-in memory whose note is gone: it is approved when it is written (#440), so nothing else
	//   would retire it, and the note is the source of truth;
	// - a fact past its expiry: search and the card already leave it out, so it is only dead weight (#457);
	// - installation state of a memcue older than this one, such as "the binary on the host is 0.8.0": a command
	//   reads the current state, and the copy is wrong since the release it names (#457). A rule or a decision that
	//   names an old version as a change of behaviour is not state and stays.
	k := knownSelf()
	today := store.Today()
	live := approved[:0:0]
	memory, haveMemory := memoryNotes(o.memory)
	stale := 0
	for _, f := range approved {
		var reason string
		switch {
		case f.Source == "memory" && haveMemory && !vet.HoldsNote(memory, f.Content):
			reason = "stale mirror: the built-in memory no longer holds it"
			stale++
		case f.Expires != "" && f.Expires < today:
			reason = "expired on " + f.Expires
			rec.Expired++
		default:
			if old := vet.OutdatedInstall(f, k.Version); old != "" {
				reason = fmt.Sprintf("installation state of memcue %s, older than %s: a command reads it", old, k.Version)
				rec.Outdated++
			}
		}
		if reason == "" {
			live = append(live, f)
			continue
		}
		if !o.dry {
			if _, err := s.Retire(f.ID); err != nil {
				rec.Error = err.Error()
				continue
			}
		}
		lines = append(lines, vetLine{Time: rec.Time, ID: f.ID, Decision: "retired", Verdict: string(vet.Drop),
			By: "rule", Reason: reason, Source: f.Source, Content: clip(f.Content, 200)})
	}
	rec.SuspectsRetired += stale
	approved = live
	var todo []vet.Suspect
	for _, sp := range vet.Suspects(approved, k, now) {
		if !checked[checkKey(sp.Fact, version)] {
			todo = append(todo, sp)
		}
	}
	rec.Suspects = len(todo)
	line := func(sp vet.Suspect, decision, verdict, by, reason string) vetLine {
		return vetLine{Time: rec.Time, ID: sp.Fact.ID, Decision: decision, Verdict: verdict, By: by,
			Reason: "suspect: " + strings.Join(sp.Evidence, "; ") + reason, Source: sp.Fact.Source,
			Content: clip(sp.Fact.Content, 200)}
	}
	if o.noModel {
		for _, sp := range todo {
			lines = append(lines, line(sp, "left", string(vet.Unsure), "rule", "; the model was off"))
		}
		return lines, rec
	}
	var done []string
	for batch := range slices.Chunk(todo, suspectBatch) {
		got, err := askSuspects(batch, k, now)
		if err != nil {
			rec.Error = "suspects: " + err.Error()
			for _, sp := range batch {
				lines = append(lines, line(sp, "left", string(vet.Unsure), "model", "; the model failed: "+err.Error()))
			}
			continue
		}
		rec.CostUSD += got.CostUSD
		rec.Tokens.Add(got.Tokens)
		byID := map[string]extract.SuspectDecision{}
		for _, d := range got.Decisions {
			byID[d.ID] = d
		}
		// A retire is the failure this step must not make, and the model samples even at temperature zero (#296), so
		// every fact it wants to retire is asked about a second time and retired only when both answers agree.
		confirmed := map[string]bool{}
		var retiring []vet.Suspect
		for _, sp := range batch {
			if d, ok := byID[sp.Fact.ID]; ok && d.Verdict == extract.RetireSuspect {
				retiring = append(retiring, sp)
			}
		}
		if len(retiring) > 0 {
			again, err := askSuspects(retiring, k, now)
			switch {
			case err != nil:
				if rec.Error == "" {
					rec.Error = "suspects: second answer: " + err.Error()
				}
			default:
				rec.CostUSD += again.CostUSD
				rec.Tokens.Add(again.Tokens)
				for _, d := range again.Decisions {
					if d.Verdict == extract.RetireSuspect {
						confirmed[d.ID] = true
					}
				}
			}
		}
		for _, sp := range batch {
			d, ok := byID[sp.Fact.ID]
			if !ok {
				lines = append(lines, line(sp, "left", string(vet.Unsure), "model", "; the model said nothing"))
				continue
			}
			reason := "; " + strings.TrimSpace(d.Reason)
			if d.Verdict != extract.RetireSuspect {
				lines = append(lines, line(sp, "kept", string(vet.Keep), "model", reason))
				done = append(done, checkKey(sp.Fact, version))
				continue
			}
			if !confirmed[sp.Fact.ID] {
				lines = append(lines, line(sp, "kept", string(vet.Keep), "model",
					reason+"; the second answer did not agree"))
				done = append(done, checkKey(sp.Fact, version))
				continue
			}
			l := line(sp, "retire", string(vet.Drop), "model", reason)
			if o.dry {
				rec.SuspectsRetired++
				lines = append(lines, l)
				continue
			}
			if err := retireSuspect(s, sp.Fact); err != nil {
				l.Decision = "failed: " + err.Error()
				rec.Failed++
			} else {
				l.Applied = true
				rec.SuspectsRetired++
				done = append(done, checkKey(sp.Fact, version))
			}
			lines = append(lines, l)
		}
	}
	if !o.dry && len(done) > 0 {
		if err := appendChecked(checkedPath, done); err != nil && rec.Error == "" {
			rec.Error = err.Error()
		}
	}
	return lines, rec
}

func askSuspects(batch []vet.Suspect, k vet.Known, now time.Time) (extract.SuspectResult, error) {
	facts := make([]suspectInput, len(batch))
	for i, sp := range batch {
		facts[i] = suspectInput{ID: sp.Fact.ID, Text: sp.Fact.Content, Origin: sp.Fact.Origin,
			Created: sp.Fact.Created.Format(time.DateOnly), Evidence: sp.Evidence}
	}
	b, err := json.Marshal(map[string]any{"version": k.Version, "today": now.Format(time.DateOnly), "facts": facts})
	if err != nil {
		return extract.SuspectResult{}, err
	}
	ctx, cancel := contextTimeout()
	defer cancel()
	return suspectFn(ctx, string(b))
}

// retireSuspect retires the fact the model read, and refuses one whose text changed since.
func retireSuspect(s *store.Store, f store.Fact) error {
	cur, err := s.Get(f.ID)
	if err != nil {
		return err
	}
	if cur.Version != f.Version || cur.Status != store.Active {
		return fmt.Errorf("%w: version %d, read %d", store.ErrChanged, cur.Version, f.Version)
	}
	_, err = s.Retire(f.ID)
	return err
}

// checkKey names a fact at its version as one binary checked it: a new text or a new binary asks again.
func checkKey(f store.Fact, binary string) string {
	return f.ID + " " + strconv.Itoa(f.Version) + " " + binary
}

func readChecked(path string) (map[string]bool, error) {
	out := map[string]bool{}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out[l] = true
		}
	}
	return out, sc.Err()
}

func appendChecked(path string, keys []string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(strings.Join(keys, "\n") + "\n")
	return errors.Join(err, file.Close())
}
