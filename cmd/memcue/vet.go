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
	"strconv"
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

// errVetBusy means another vet run holds stateDir/vet.lock.
var errVetBusy = errors.New("another vet run is in progress")

// reviewFn is the model step, replaced in tests.
var reviewFn = extract.Review

// vetLine is one decision as vet.log records it.
type vetLine struct {
	Time        string   `json:"time"`
	ID          string   `json:"id"`
	Decision    string   `json:"decision"` // approve, retire, provisional, split, left; failed: … when the base refused the write
	Applied     bool     `json:"applied"`
	Provisional bool     `json:"provisional,omitempty"`
	Verdict     string   `json:"verdict"`
	By          string   `json:"by"` // rule, judge or model
	Reason      string   `json:"reason"`
	Expires     string   `json:"expires,omitempty"`
	Source      string   `json:"source,omitempty"`
	Content     string   `json:"content,omitempty"`
	Parts       []string `json:"parts,omitempty"` // what a split candidate was split into
	Into        []string `json:"into,omitempty"`  // ids of the parts, once they are written
}

// vetRecord is the summary line of a run.
type vetRecord struct {
	Time        string `json:"time"`
	Candidates  int    `json:"candidates"`
	Approved    int    `json:"approved"`
	Retired     int    `json:"retired"`
	Unsure      int    `json:"unsure"`
	Provisional int    `json:"provisional,omitempty"`
	Split       int    `json:"split,omitempty"`
	Failed      int    `json:"failed,omitempty"`
	// Suspects counts the approved facts the binary found suspect and asked the model about (#289); SuspectsRetired
	// those the model said describe a past state as current.
	Suspects        int            `json:"suspects,omitempty"`
	SuspectsRetired int            `json:"suspects_retired,omitempty"`
	DryRun          bool           `json:"dry_run,omitempty"`
	CostUSD         float64        `json:"cost_usd,omitempty"`
	Tokens          extract.Tokens `json:"tokens,omitempty"`
	Error           string         `json:"error,omitempty"`
}

// vetOptions is how a run was asked for.
type vetOptions struct {
	dry     bool
	noModel bool
	auto    bool
	ttlDays int
	memory  string // directory of the built-in memory notes; empty skips the mirror rule
	parts   bool   // the candidates are the parts of a split in this run: they are not split again
}

// vetTTLDays is how long a provisional fact is recalled; the default is two weeks.
func vetTTLDays() int {
	if v := strings.TrimSpace(os.Getenv("MEMCUE_VET_TTL_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 14
}

// vetAutoPolicy says whether unsure becomes provisional instead of waiting for review. Provisional is the default;
// quarantine (or strict, or any unknown value) is the explicit opt-out.
func vetAutoPolicy() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEMCUE_VET_POLICY"))) {
	case "", "auto", "provisional":
		return true
	default:
		return false
	}
}

// runVet decides the candidate queue: memcue vet [-dry-run] [-limit N] [-no-model] [-auto|-quarantine] [-ttl-days N]
// [-json] [-memory DIR].
func runVet(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("vet", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "print the verdicts and change nothing")
	limit := fs.Int("limit", 0, "decide at most N candidates, newest first (0: the whole queue)")
	noModel := fs.Bool("no-model", false, "run the deterministic rules only; undecided candidates stay for review")
	auto := fs.Bool("auto", false, "turn a model's unsure answer into a provisional fact (the default; ignored with -no-model)")
	quarantine := fs.Bool("quarantine", false, "leave unsure candidates for review even if the default is provisional")
	ttlDays := fs.Int("ttl-days", vetTTLDays(), "how long a provisional fact is recalled")
	asJSON := fs.Bool("json", false, "print the decisions as JSON")
	memory := fs.String("memory", os.Getenv("MEMCUE_MEMORY_DIR"),
		"directory of the built-in memory notes the mirrors come from; empty skips the mirror rule")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: memcue vet [-dry-run] [-limit N] [-no-model] [-auto|-quarantine] [-ttl-days N] [-json] [-memory DIR]")
	}
	lock, err := acquireVetLock()
	if err != nil {
		return err
	}
	defer releaseVetLock(lock)
	var lines []vetLine
	var rec vetRecord
	err = withStore(func(s *store.Store) error {
		cands, err := s.Candidates()
		if err != nil {
			return err
		}
		if *limit > 0 && len(cands) > *limit {
			cands = cands[:*limit]
		}
		policyAuto := (*auto || vetAutoPolicy()) && !*quarantine
		if *noModel {
			// Rules alone cannot answer what the judge would: an undecided candidate waits for a real review
			// instead of entering recall as provisional.
			policyAuto = false
		}
		ttl := *ttlDays
		if ttl <= 0 {
			ttl = vetTTLDays()
		}
		o := vetOptions{dry: *dry, noModel: *noModel, auto: policyAuto, ttlDays: ttl, memory: *memory}
		lines, rec = vetQueue(s, cands, o)
		more, sus := vetSuspects(s, o, time.Now())
		lines = append(lines, more...)
		rec.add(sus)
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
		if err := enc.Encode(map[string]any{"record": rec, "decisions": lines}); err != nil {
			return err
		}
		return vetFailure(rec)
	}
	if err := printVet(out, lines, rec); err != nil {
		return err
	}
	return vetFailure(rec)
}

// vetFailure is the command's exit status: a run that could not read the base or write a decision is not a success.
func vetFailure(rec vetRecord) error {
	if rec.Error != "" {
		return errors.New(rec.Error)
	}
	if rec.Failed > 0 {
		return fmt.Errorf("%d vet decision(s) failed to apply", rec.Failed)
	}
	return nil
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
					decided = append(decided, vet.Decision{Fact: f, Verdict: vet.Unsure, By: "model", Failed: true,
						Reason: "the judge failed: " + err.Error()})
				case !ok:
					decided = append(decided, vet.Decision{Fact: f, Verdict: vet.Unsure, By: "model", Failed: true,
						Reason: "the judge said nothing"})
				default:
					decided = append(decided, modelDecision(f, d, o.parts))
				}
			}
		}
	}

	// A malformed expiry is a model answer we cannot apply. Turn it into a judgement for a human instead of
	// turning a formatting mistake into a failed write.
	for i := range decided {
		d := &decided[i]
		if d.Verdict != vet.Keep || d.Expires == "" {
			continue
		}
		if err := store.ValidExpiry(d.Expires); err != nil {
			reason := "invalid expiry: " + err.Error()
			if d.Reason != "" {
				reason += "; " + d.Reason
			}
			d.Verdict, d.Reason, d.Expires, d.Failed = vet.Unsure, reason, "", true
		}
	}

	if o.auto {
		days := o.ttlDays
		if days <= 0 {
			days = 14
		}
		until := time.Now().AddDate(0, 0, days).Format(time.DateOnly)
		for i := range decided {
			d := &decided[i]
			// Only a model that read the candidate and said "unsure" may become provisional. A failed or empty
			// answer, an undecided rule, or a malformed expiry is not that judgement.
			if d.Verdict != vet.Unsure || d.Failed || d.By != "model" {
				continue
			}
			expires := until
			if d.Expires != "" && d.Expires < expires {
				expires = d.Expires
			}
			reason := "provisional: the model was unsure"
			if d.Reason != "" {
				reason += " — " + d.Reason
			}
			d.Verdict, d.Provisional, d.By, d.Expires, d.Reason = vet.Keep, true, "policy", expires, reason
		}
	}

	lines := make([]vetLine, 0, len(decided))
	var parts []store.Fact
	for _, d := range decided {
		line := vetLine{Time: rec.Time, ID: d.Fact.ID, Verdict: string(d.Verdict), By: d.By,
			Reason: d.Reason, Expires: d.Expires, Provisional: d.Provisional, Source: d.Fact.Source,
			Content: clip(d.Fact.Content, 200)}
		switch {
		case d.Verdict == vet.Split:
			line.Decision, line.Parts = "split", d.Parts
		case d.Provisional:
			line.Decision = "provisional"
		case d.Verdict == vet.Keep:
			line.Decision = "approve"
		case d.Verdict == vet.Drop:
			line.Decision = "retire"
		default:
			line.Decision = "left"
		}
		if o.dry {
			switch {
			case d.Verdict == vet.Split:
				rec.Split++
			case d.Provisional:
				rec.Provisional++
			case d.Verdict == vet.Keep:
				rec.Approved++
			case d.Verdict == vet.Drop:
				rec.Retired++
			default:
				rec.Unsure++
			}
			lines = append(lines, line)
			continue
		}
		switch d.Verdict {
		case vet.Split:
			written, err := s.SplitCandidate(d.Fact.ID, d.Parts)
			if err != nil {
				line.Decision = "failed: " + err.Error()
				rec.Failed++
				break
			}
			line.Applied = true
			rec.Split++
			for _, p := range written {
				line.Into = append(line.Into, p.ID)
			}
			parts = append(parts, written...)
		case vet.Keep, vet.Drop:
			line.Decision = applyVet(s, d, line.Decision)
			if strings.HasPrefix(line.Decision, "failed") {
				rec.Failed++
			} else {
				line.Applied = true
				switch {
				case d.Provisional:
					rec.Provisional++
				case d.Verdict == vet.Keep:
					rec.Approved++
				default:
					rec.Retired++
				}
			}
		default:
			line.Decision = "left"
			rec.Unsure++
		}
		lines = append(lines, line)
	}
	// The parts are reviewed in the same run: a session's vet sees only the ids the session stored, so parts left for
	// later would wait for a manual run. A part that the model wants to split again stays a candidate instead.
	if len(parts) > 0 {
		sub := o
		sub.parts = true
		more, subRec := vetQueue(s, parts, sub)
		lines = append(lines, more...)
		rec.add(subRec)
	}
	return lines, rec
}

// add counts a run over the parts of a split into the run that split them.
func (r *vetRecord) add(o vetRecord) {
	r.Candidates += o.Candidates
	r.Approved += o.Approved
	r.Retired += o.Retired
	r.Unsure += o.Unsure
	r.Provisional += o.Provisional
	r.Split += o.Split
	r.Failed += o.Failed
	r.Suspects += o.Suspects
	r.SuspectsRetired += o.SuspectsRetired
	r.CostUSD += o.CostUSD
	r.Tokens.Add(o.Tokens)
	if r.Error == "" {
		r.Error = o.Error
	}
}

// modelDecision turns the model's answer about one candidate into a decision. A split needs two parts at least, and a
// part of a split is not split again in the same run; either case is a failed answer, so the candidate waits.
func modelDecision(f store.Fact, d extract.ReviewDecision, isPart bool) vet.Decision {
	dec := vet.Decision{Fact: f, Verdict: verdictOf(d.Verdict), By: "model", Reason: strings.TrimSpace(d.Reason),
		Expires: d.Expires}
	if dec.Verdict != vet.Split {
		return dec
	}
	switch {
	case isPart:
		dec.Verdict, dec.Failed = vet.Unsure, true
		dec.Reason = "a part of a split was split again; " + dec.Reason
	case len(d.Parts) < 2:
		dec.Verdict, dec.Failed = vet.Unsure, true
		dec.Reason = fmt.Sprintf("split into %d part(s); %s", len(d.Parts), dec.Reason)
	default:
		dec.Parts, dec.Expires = d.Parts, ""
	}
	return dec
}

// applyVet writes the verdict into the base and names what it did, or why it could not.
func applyVet(s *store.Store, d vet.Decision, decision string) string {
	var err error
	switch d.Verdict {
	case vet.Keep:
		if d.Provisional {
			_, err = s.ApproveProvisional(d.Fact.ID, d.Expires)
		} else {
			_, err = s.ApproveExpiring(d.Fact.ID, d.Expires)
		}
	case vet.Drop:
		_, err = s.RetireCandidate(d.Fact.ID)
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
	hits, err := recall.Recall(s, f.Content+" "+strings.Join(f.Keywords, " "), nil, p)
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
	case extract.SplitVerdict:
		return vet.Split
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
		reason := l.Reason
		if l.Provisional {
			reason = "provisional until " + l.Expires + " — " + reason
		}
		if strings.HasPrefix(l.Decision, "failed:") {
			reason += " [" + l.Decision + "]"
		}
		if _, err := fmt.Fprintf(out, "%-6s %s by %s%s | %s — %s\n", l.Verdict, l.ID, l.By, expires,
			clip(l.Content, 90), reason); err != nil {
			return err
		}
		for _, p := range l.Parts {
			if _, err := fmt.Fprintf(out, "       + %s\n", clip(p, 110)); err != nil {
				return err
			}
		}
	}
	if rec.Suspects > 0 {
		if _, err := fmt.Fprintf(out, "Подозрительных одобренных фактов %d: снято %d\n", rec.Suspects,
			rec.SuspectsRetired); err != nil {
			return err
		}
	}
	if rec.Candidates == 0 {
		_, err := fmt.Fprintln(out, "Карантин memcue пуст.")
		return err
	}
	line := fmt.Sprintf("Кандидатов %d: принято %d, отклонено %d, временно принято %d, осталось решать %d",
		rec.Candidates, rec.Approved, rec.Retired, rec.Provisional, rec.Unsure)
	if rec.DryRun {
		line = fmt.Sprintf("Пробный прогон, ничего не изменено. Кандидатов %d: к принятию %d, к отклонению %d, "+
			"временно принять %d, осталось решать %d", rec.Candidates, rec.Approved, rec.Retired, rec.Provisional,
			rec.Unsure)
	}
	if rec.Split > 0 {
		line += fmt.Sprintf("; разделено на части %d", rec.Split)
	}
	if rec.Failed > 0 {
		line += fmt.Sprintf("; ошибок записи %d", rec.Failed)
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

// acquireVetLock takes the stateDir/vet.lock without waiting. Both the manual command and maybeVet use it, so a
// stale decision from one process cannot overlap a decision from another.
func acquireVetLock() (*os.File, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "vet.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errVetBusy
		}
		return nil, fmt.Errorf("vet lock: %w", err)
	}
	return lock, nil
}

func releaseVetLock(lock *os.File) {
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

// logVet appends the decisions and the summary to vet.log. The log is the record a human checks the automatic
// decisions by, so a failure to write it is reported rather than swallowed.
func logVet(lines []vetLine, rec vetRecord) error {
	if len(lines) == 0 && rec.Candidates == 0 && rec.Error == "" {
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

// maybeVet reviews the candidates a session just stored, unless MEMCUE_VET=0 or no review model can run (no
// endpoint and no `claude` on PATH). It runs in the extraction process, which the hook already detached, so it holds
// no turn, and before the dream, which only sees approved facts. A run that finds the lock taken is skipped, not
// waited for: the next session reviews the same queue. Its failures go to vet.log and never fail the extraction.
func maybeVet(ids []string) {
	if os.Getenv("MEMCUE_VET") == "0" || len(ids) == 0 || !extract.ReviewOn() {
		return
	}
	lock, err := acquireVetLock()
	if err != nil {
		return // another run is deciding this queue
	}
	defer releaseVetLock(lock)

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
	o := vetOptions{auto: vetAutoPolicy(), ttlDays: vetTTLDays(), memory: os.Getenv("MEMCUE_MEMORY_DIR")}
	lines, rec := vetQueue(s, cands, o)
	more, sus := vetSuspects(s, o, time.Now())
	lines = append(lines, more...)
	rec.add(sus)
	if err := logVet(lines, rec); err != nil {
		fmt.Fprintln(os.Stderr, "memcue:", err)
	}
}

// memoryNotes reads the built-in memory notes of dir — every markdown file, folded into one text. An unset or
// unreadable directory is reported as not found: the mirror rule is skipped rather than retiring every mirror. A file
// that cannot be read reports the same, because the notes we could read are not the whole note and a mirror they do
// not hold is not a deleted one.
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
			return "", false
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String(), true
}
