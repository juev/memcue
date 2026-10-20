package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/vet"
)

// vetBatch is how many candidates one review call reads: their neighbours plus ten more candidates still fit a small
// answer, and a failed call costs a batch, not the queue.
const vetBatch = 12

// reviewFn is the model step, replaced in tests.
var reviewFn = extract.Review

// vetLine is one decision as vet.log records it, and as the review page logs its own: the same fields, so a weekly
// report or an audit reads both without knowing which decided.
type vetLine struct {
	Time     string `json:"time"`
	ID       string `json:"id"`
	Decision string `json:"decision"` // approve, retire, left; failed: … when the base refused the write
	Applied  bool   `json:"applied"`
	Verdict  string `json:"verdict"`
	By       string `json:"by"` // rule or model
	Reason   string `json:"reason"`
	Expires  string `json:"expires,omitempty"`
	Source   string `json:"source,omitempty"`
	Content  string `json:"content,omitempty"`
}

// vetRecord is the summary line of a run.
type vetRecord struct {
	Time       string         `json:"time"`
	Candidates int            `json:"candidates"`
	Approved   int            `json:"approved"`
	Retired    int            `json:"retired"`
	Unsure     int            `json:"unsure"`
	DryRun     bool           `json:"dry_run,omitempty"`
	CostUSD    float64        `json:"cost_usd,omitempty"`
	Tokens     extract.Tokens `json:"tokens,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// vetOptions is how a run was asked for.
type vetOptions struct {
	dry     bool
	noModel bool
	memory  string // directory of the built-in memory notes; empty skips the mirror rule
}

// runVet decides the candidate queue: memcue vet [-dry-run] [-limit N] [-no-model] [-json] [-memory DIR].
func runVet(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("vet", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "print the verdicts and change nothing")
	limit := fs.Int("limit", 0, "decide at most N candidates, newest first (0: the whole queue)")
	noModel := fs.Bool("no-model", false, "run the deterministic rules only")
	asJSON := fs.Bool("json", false, "print the decisions as JSON")
	memory := fs.String("memory", os.Getenv("MEMCUE_MEMORY_DIR"),
		"directory of the built-in memory notes the mirrors come from; empty skips the mirror rule")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: memcue vet [-dry-run] [-limit N] [-no-model] [-json] [-memory DIR]")
	}
	var lines []vetLine
	var rec vetRecord
	err := withStore(func(s *store.Store) error {
		cands, err := s.Candidates()
		if err != nil {
			return err
		}
		if *limit > 0 && len(cands) > *limit {
			cands = cands[:*limit]
		}
		lines, rec = vetQueue(s, cands, vetOptions{dry: *dry, noModel: *noModel, memory: *memory})
		return nil
	})
	if err != nil {
		return err
	}
	if err := logVet(lines, rec); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{"record": rec, "decisions": lines})
	}
	return printVet(out, lines, rec)
}

// vetQueue decides cands and, unless dry, carries the verdicts out. approved facts are what a rule and the judge
// compare a candidate with; a candidate never sees itself there.
func vetQueue(s *store.Store, cands []store.Fact, o vetOptions) ([]vetLine, vetRecord) {
	rec := vetRecord{Time: time.Now().UTC().Format(time.RFC3339), Candidates: len(cands), DryRun: o.dry}
	if len(cands) == 0 {
		return nil, rec
	}
	approved, err := s.Active()
	if err != nil {
		rec.Error = err.Error()
		return nil, rec
	}
	memory, haveMemory := memoryNotes(o.memory)
	decided, rest := vet.Rules(cands, approved, memory, haveMemory)

	if o.noModel {
		for _, f := range rest {
			decided = append(decided, vet.Decision{Fact: f, Verdict: vet.Unsure, By: "rule",
				Reason: "the rules did not decide it and the model was off"})
		}
	} else {
		for batch := range slices.Chunk(rest, vetBatch) {
			got, cost, tokens, err := judge(s, batch)
			rec.CostUSD += cost
			rec.Tokens.Add(tokens)
			for _, f := range batch {
				d, ok := got[f.ID]
				switch {
				case err != nil:
					// A failed call decides nothing: the batch waits for the next run rather than being approved blind.
					decided = append(decided, vet.Decision{Fact: f, Verdict: vet.Unsure, By: "model",
						Reason: "the judge failed: " + err.Error()})
				case !ok:
					decided = append(decided, vet.Decision{Fact: f, Verdict: vet.Unsure, By: "model",
						Reason: "the judge said nothing"})
				default:
					decided = append(decided, vet.Decision{Fact: f, Verdict: verdictOf(d.Verdict), By: "model",
						Reason: strings.TrimSpace(d.Reason), Expires: d.Expires})
				}
			}
		}
	}

	lines := make([]vetLine, 0, len(decided))
	for _, d := range decided {
		line := vetLine{Time: rec.Time, ID: d.Fact.ID, Verdict: string(d.Verdict), By: d.By,
			Reason: d.Reason, Expires: d.Expires, Source: d.Fact.Source, Content: clip(d.Fact.Content, 200)}
		switch d.Verdict {
		case vet.Keep:
			line.Decision, rec.Approved = "approve", rec.Approved+1
		case vet.Drop:
			line.Decision, rec.Retired = "retire", rec.Retired+1
		default:
			line.Decision, rec.Unsure = "left", rec.Unsure+1
		}
		if !o.dry {
			line.Applied, line.Decision = true, applyVet(s, d, line.Decision)
			if strings.HasPrefix(line.Decision, "failed") {
				line.Applied = false
			}
		}
		lines = append(lines, line)
	}
	return lines, rec
}

// applyVet writes the verdict into the base and names what it did, or why it could not.
func applyVet(s *store.Store, d vet.Decision, decision string) string {
	var err error
	switch d.Verdict {
	case vet.Keep:
		_, err = s.ApproveExpiring(d.Fact.ID, d.Expires)
	case vet.Drop:
		_, err = s.Retire(d.Fact.ID)
	default:
		return decision
	}
	if err != nil {
		return "failed: " + err.Error()
	}
	return decision
}

// judge asks the model about one batch and returns its verdicts by id. A verdict about an id the batch did not hold
// is dropped: the answer cannot move a fact the model was not shown.
func judge(s *store.Store, batch []store.Fact) (map[string]extract.ReviewDecision, float64, extract.Tokens, error) {
	input, err := reviewInput(s, batch)
	if err != nil {
		return nil, 0, extract.Tokens{}, err
	}
	ctx, cancel := contextTimeout()
	defer cancel()
	r, err := reviewFn(ctx, input)
	if err != nil {
		return nil, 0, extract.Tokens{}, err
	}
	want := map[string]bool{}
	for _, f := range batch {
		want[f.ID] = true
	}
	out := make(map[string]extract.ReviewDecision, len(r.Decisions))
	for _, d := range r.Decisions {
		if want[d.ID] {
			out[d.ID] = d
		}
	}
	return out, r.CostUSD, r.Tokens, nil
}

// judgedCandidate is one candidate as the judge sees it.
type judgedCandidate struct {
	ID         string            `json:"id"`
	Text       string            `json:"text"`
	Kind       string            `json:"kind,omitempty"`
	Scope      string            `json:"scope,omitempty"`
	Source     string            `json:"source,omitempty"`
	Origin     string            `json:"origin,omitempty"`
	Created    string            `json:"created,omitempty"`
	Neighbours []judgedNeighbour `json:"neighbours,omitempty"`
}

type judgedNeighbour struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// reviewInput renders a batch with the approved facts nearest to each candidate, the way a dream groups facts: the
// candidate itself is the prompt, so the shared words are what makes a duplicate or a contradiction visible.
func reviewInput(s *store.Store, batch []store.Fact) (string, error) {
	list := make([]judgedCandidate, 0, len(batch))
	for _, f := range batch {
		rc := judgedCandidate{ID: f.ID, Text: f.Content, Kind: f.Kind, Scope: f.Scope, Source: f.Source,
			Origin: f.Origin, Created: f.Created.UTC().Format(time.DateOnly)}
		hits, err := judgedNeighbours(s, f)
		if err != nil {
			return "", err
		}
		rc.Neighbours = hits
		list = append(list, rc)
	}
	b, err := json.Marshal(map[string]any{"candidates": list})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// judgedNeighbours finds the approved facts closest to f: the dream's own search, since a fact that covers or
// contradicts another is exactly what a dream would group with it.
func judgedNeighbours(s *store.Store, f store.Fact) ([]judgedNeighbour, error) {
	p := dreamNeighbours
	p.Seen = map[string]bool{f.ID: true}
	hits, err := recall.Recall(s, f.Content+" "+strings.Join(f.Keywords, " "), "", p)
	if err != nil {
		return nil, err
	}
	out := make([]judgedNeighbour, 0, len(hits))
	for _, h := range hits {
		out = append(out, judgedNeighbour{ID: h.ID, Text: clip(h.Content, 300)})
	}
	return out, nil
}

// verdictOf reads the model's word as a verdict; anything it does not name is the answer "a human has to decide".
func verdictOf(word string) vet.Verdict {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case extract.KeepVerdict:
		return vet.Keep
	case extract.DropVerdict:
		return vet.Drop
	default:
		return vet.Unsure
	}
}

func printVet(out io.Writer, lines []vetLine, rec vetRecord) error {
	for _, l := range lines {
		expires := ""
		if l.Expires != "" {
			expires = " expires=" + l.Expires
		}
		if _, err := fmt.Fprintf(out, "%-6s %s by %s%s | %s — %s\n", l.Verdict, l.ID, l.By, expires,
			clip(l.Content, 90), l.Reason); err != nil {
			return err
		}
	}
	if rec.Candidates == 0 {
		_, err := fmt.Fprintln(out, "Карантин memcue пуст.")
		return err
	}
	line := fmt.Sprintf("Кандидатов %d: принято %d, отклонено %d, осталось решать %d", rec.Candidates, rec.Approved,
		rec.Retired, rec.Unsure)
	if rec.DryRun {
		line = fmt.Sprintf("Пробный прогон, ничего не изменено. Кандидатов %d: к принятию %d, к отклонению %d, "+
			"осталось решать %d", rec.Candidates, rec.Approved, rec.Retired, rec.Unsure)
	}
	if rec.Error != "" {
		line += "; ошибка: " + rec.Error
	}
	if rec.CostUSD > 0 {
		line += fmt.Sprintf("; $%.4f", rec.CostUSD)
	}
	_, err := fmt.Fprintln(out, line)
	return err
}

// logVet appends the decisions and the summary to vet.log. The log is the record a human checks the automatic
// decisions by, so a failure to write it is reported rather than swallowed.
func logVet(lines []vetLine, rec vetRecord) error {
	if len(lines) == 0 && rec.Candidates == 0 {
		return nil
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	for _, l := range lines {
		if err := appendJSON(filepath.Join(dir, "vet.log"), l); err != nil {
			return err
		}
	}
	return appendJSON(filepath.Join(dir, "vet.log"), rec)
}

// maybeVet reviews the candidates a session just stored when MEMCUE_VET=1. It runs in the extraction process, which
// the hook already detached, so it holds no turn, and before the dream, which only sees approved facts. A run that
// finds the lock taken is skipped, not waited for: the next session reviews the same queue. Its failures go to
// vet.log and never fail the extraction.
func maybeVet(ids []string) {
	if os.Getenv("MEMCUE_VET") != "1" || len(ids) == 0 || !extract.ReviewOn() {
		return
	}
	dir, err := stateDir()
	if err != nil {
		return
	}
	lock, err := os.OpenFile(filepath.Join(dir, "vet.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return // another run is deciding this queue
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	s, err := store.Open(factsPath())
	if err != nil {
		return
	}
	defer func() { _ = s.Close() }()
	var cands []store.Fact
	for _, id := range ids {
		f, err := s.Get(id)
		if err != nil || f.Review != store.Candidate || f.Status != store.Active {
			continue
		}
		cands = append(cands, f)
	}
	lines, rec := vetQueue(s, cands, vetOptions{memory: os.Getenv("MEMCUE_MEMORY_DIR")})
	if err := logVet(lines, rec); err != nil {
		fmt.Fprintln(os.Stderr, "memcue:", err)
	}
}

// memoryNotes reads the built-in memory notes of dir — every markdown file, folded into one text. An unset or
// unreadable directory is reported as not found: the mirror rule is skipped rather than retiring every mirror.
func memoryNotes(dir string) (string, bool) {
	if strings.TrimSpace(dir) == "" {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String(), true
}
