package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/juev/memcue/internal/store"
)

// auditMinPrecision is the share of a heuristic's labelled proposals that must be genuine junk before its batch is
// applied. Below it the owner looks at more proposals or fixes the heuristic, as the issue asks.
const auditMinPrecision = 0.95

// auditProposal is one active fact a heuristic proposes to retire. The owner marks Decision retire or keep.
type auditProposal struct {
	ID           string `json:"id"`
	Version      int    `json:"version"`
	Content      string `json:"content"`
	Origin       string `json:"origin,omitempty"`
	Created      string `json:"created,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Heuristic    string `json:"heuristic"`
	Reason       string `json:"reason"`
	LastRecalled string `json:"last_recalled,omitempty"`
	Delivered    string `json:"delivered,omitempty"`
	Decision     string `json:"decision"` // retire or keep; empty means not reviewed yet
}

// auditHeuristic is the per-heuristic count and, after application, its measured precision.
type auditHeuristic struct {
	Name      string   `json:"name"`
	Proposed  int      `json:"proposed"`
	Labelled  int      `json:"labelled,omitempty"`
	Retire    int      `json:"retire,omitempty"`
	Keep      int      `json:"keep,omitempty"`
	Precision *float64 `json:"precision,omitempty"`
	Applied   int      `json:"applied,omitempty"`
	Refused   int      `json:"refused,omitempty"`
}

// auditPlan is the file `memcue audit -dry-run -plan` writes and `-apply` reads.
type auditPlan struct {
	Generated    string           `json:"generated"`
	MinPrecision float64          `json:"min_precision"`
	Heuristics   []auditHeuristic `json:"heuristics"`
	Proposals    []auditProposal  `json:"proposals"`
}

// auditRecord is one line of audit.log: what was applied, what precision it rested on, and what was refused.
type auditRecord struct {
	Time         string           `json:"time"`
	Plan         string           `json:"plan"`
	MinPrecision float64          `json:"min_precision"`
	Heuristics   []auditHeuristic `json:"heuristics"`
	Applied      int              `json:"applied"`
	Refused      int              `json:"refused"`
	Errors       []string         `json:"errors,omitempty"`
}

var (
	// A date plus an action verb is the shape of a journal entry: "20 октября обновили", "2026-10-20 упал".
	reAuditDate = regexp.MustCompile(`(?i)(?:20\d{2}-\d{2}-\d{2}|\d{1,2}[./]\d{1,2}[./]20\d{2}|` +
		`\d{1,2}\s+(?:января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря))`)
	reAuditEvent = regexp.MustCompile(`(?i)(?:обнов|выпущ|выпуст|запущ|запуст|перезапуст|упал|сломал|починил|исправ|` +
		`deployed|released|updated|restarted|failed|healthy|merged)`)
	// A retelling of CI or a Makefile, not a fact about the system.
	reAuditCI = regexp.MustCompile(`(?i)(?:\bci\b|github actions|gitlab ci|\bworkflow\b|\bpipeline\b|\bjob\b|` +
		`makefile|make test|make build)`)
)

// curated reports a kind the owner or the extraction policy already selected. The audit is for the old migrated
// base, whose facts mostly have no kind; it must not propose retiring a rule, decision, pitfall or environment.
func curated(kind string) bool {
	switch strings.ToLower(kind) {
	case "rule", "decision", "pitfall", "environment":
		return true
	}
	return false
}

// auditEventDate finds a date within a short distance of an action verb.
func auditEventDate(content string) (string, bool) {
	lower := strings.ToLower(content)
	dates := reAuditDate.FindAllStringIndex(lower, -1)
	events := reAuditEvent.FindAllStringIndex(lower, -1)
	for _, d := range dates {
		for _, e := range events {
			if abs(d[0]-e[0]) > 160 { // a few dozen characters: "20 октября обновили ..."
				continue
			}
			return fmt.Sprintf("date %q next to the action %q reads as a journal entry, not a durable fact",
				lower[d[0]:d[1]], lower[e[0]:e[1]]), true
		}
	}
	return "", false
}

// auditCI finds a retelling of CI or a Makefile.
func auditCI(content string) (string, bool) {
	lower := strings.ToLower(content)
	if m := reAuditCI.FindString(lower); m != "" {
		return fmt.Sprintf("mentions %q: a retelling of CI or a Makefile, not a durable fact", m), true
	}
	return "", false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// auditFact returns the first heuristic that proposes the fact, or "".
func auditFact(f store.Fact) (string, string) {
	if curated(f.Kind) {
		return "", ""
	}
	if reason, ok := auditEventDate(f.Content); ok {
		return "event_date", reason
	}
	if reason, ok := auditCI(f.Content); ok {
		return "ci_makefile", reason
	}
	return "", ""
}

// runAudit writes a plan or applies one: memcue audit -dry-run -plan FILE | memcue audit -apply FILE.
func runAudit(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "write proposals without changing the base")
	planPath := fs.String("plan", "", "file to write the proposals to")
	applyPath := fs.String("apply", "", "reviewed plan to apply")
	minPrecision := fs.Float64("min-precision", auditMinPrecision, "labelled precision a heuristic needs before its retirements apply")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: memcue audit -dry-run -plan FILE | memcue audit -apply FILE [-min-precision P]")
	}
	if *minPrecision < 0 || *minPrecision > 1 {
		return fmt.Errorf("audit: -min-precision %v is not a probability", *minPrecision)
	}
	if *applyPath != "" {
		if *dry || *planPath != "" {
			return errors.New("audit: -apply cannot be combined with -dry-run or -plan")
		}
		return applyAudit(*applyPath, *minPrecision, out)
	}
	if !*dry || *planPath == "" {
		return errors.New("audit: use -dry-run -plan FILE to write proposals, or -apply FILE to carry them out")
	}
	return writeAuditPlan(*planPath, *minPrecision, out)
}

// writeAuditPlan scans the active base, writes proposals with their reasons and usage, and changes nothing.
func writeAuditPlan(path string, minPrecision float64, out io.Writer) error {
	s, err := store.Open(factsPath())
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	facts, err := s.Active()
	if err != nil {
		return err
	}
	recalled, err := readRecallUsage()
	if err != nil {
		return err
	}
	delivered, err := readCardUsage()
	if err != nil {
		return err
	}
	plan := auditPlan{Generated: time.Now().UTC().Format(time.RFC3339), MinPrecision: minPrecision}
	counts := map[string]int{}
	for _, f := range facts {
		name, reason := auditFact(f)
		if name == "" {
			continue
		}
		counts[name]++
		plan.Proposals = append(plan.Proposals, auditProposal{ID: f.ID, Version: f.Version, Content: f.Content,
			Origin: f.Origin, Created: f.Created.Format(time.RFC3339), Kind: f.Kind, Scope: f.Scope, Heuristic: name,
			Reason: reason, LastRecalled: recalled[f.ID], Delivered: delivered[f.ID]})
	}
	sort.SliceStable(plan.Proposals, func(i, j int) bool {
		a, b := plan.Proposals[i], plan.Proposals[j]
		if ra, rb := auditRank(a.Heuristic), auditRank(b.Heuristic); ra != rb {
			return ra < rb
		}
		// Usage orders the review: a fact recall never served comes first, then the oldest service.
		if a.LastRecalled != b.LastRecalled {
			return a.LastRecalled < b.LastRecalled
		}
		if a.Delivered != b.Delivered {
			return a.Delivered < b.Delivered
		}
		return a.ID < b.ID
	})
	plan.Heuristics = auditSummaries(counts)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "audit: %d proposals (%s); plan %s\n", len(plan.Proposals), auditCounts(plan.Heuristics), path)
	return err
}

func auditRank(name string) int {
	switch name {
	case "event_date":
		return 0
	case "ci_makefile":
		return 1
	}
	return 2
}

func auditSummaries(counts map[string]int) []auditHeuristic {
	return []auditHeuristic{{Name: "event_date", Proposed: counts["event_date"]},
		{Name: "ci_makefile", Proposed: counts["ci_makefile"]}}
}

func auditCounts(hs []auditHeuristic) string {
	var parts []string
	for _, h := range hs {
		if h.Proposed > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", h.Name, h.Proposed))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// readRecallUsage reads the last time recall served each fact. A missing log is not an error: it only means no usage
// data yet, and the plan keeps the fact in its normal order.
func readRecallUsage() (map[string]string, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	return readUsage(filepath.Join(dir, "recall.log"), func(rec json.RawMessage) map[string]string {
		var r struct {
			Time  string `json:"time"`
			Facts []struct {
				ID string `json:"id"`
			} `json:"facts"`
		}
		if json.Unmarshal(rec, &r) != nil {
			return nil
		}
		out := map[string]string{}
		for _, f := range r.Facts {
			if f.ID != "" {
				out[f.ID] = r.Time
			}
		}
		return out
	})
}

// readCardUsage reads the last time the SessionStart card carried each fact.
func readCardUsage() (map[string]string, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	return readUsage(filepath.Join(dir, "card.log"), func(rec json.RawMessage) map[string]string {
		var r struct {
			Time  string   `json:"time"`
			Facts []string `json:"facts"`
		}
		if json.Unmarshal(rec, &r) != nil {
			return nil
		}
		out := map[string]string{}
		for _, id := range r.Facts {
			out[id] = r.Time
		}
		return out
	})
}

// readUsage merges one JSON line per log record into fact id -> latest time. Malformed lines are skipped: an audit
// must not fail because a log was cut off.
func readUsage(path string, parse func(json.RawMessage) map[string]string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		for id, t := range parse(sc.Bytes()) {
			if t > out[id] {
				out[id] = t
			}
		}
	}
	return out, sc.Err()
}

// applyAudit carries out the retire decisions of a reviewed plan. Only a heuristic whose labelled precision reaches
// minPrecision is applied; a fact changed since the plan is refused rather than retired on stale text.
func applyAudit(path string, minPrecision float64, out io.Writer) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var plan auditPlan
	if err := json.Unmarshal(b, &plan); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	summaries, order := auditSummaryIndex(plan.Heuristics)
	decisions := 0
	for _, p := range plan.Proposals {
		switch p.Decision {
		case "":
			continue
		case "retire", "keep":
			decisions++
		default:
			return fmt.Errorf("audit: %s has decision %q, want retire or keep", p.ID, p.Decision)
		}
		h := summaries[p.Heuristic]
		if h == nil {
			return fmt.Errorf("audit: %s has unknown heuristic %q", p.ID, p.Heuristic)
		}
		h.Labelled++
		if p.Decision == "retire" {
			h.Retire++
		} else {
			h.Keep++
		}
	}
	if decisions == 0 {
		return errors.New("audit: the plan has no retire or keep decisions; review it first")
	}
	for _, h := range summaries {
		if h.Labelled > 0 {
			p := float64(h.Retire) / float64(h.Labelled)
			h.Precision = &p
		}
	}
	s, err := store.Open(factsPath())
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	var applied, refused int
	var failures []string
	for _, p := range plan.Proposals {
		if p.Decision != "retire" {
			continue
		}
		h := summaries[p.Heuristic]
		if h.Precision == nil || *h.Precision < minPrecision {
			h.Refused++
			refused++
			continue
		}
		cur, err := s.Get(p.ID)
		if err != nil {
			failures = append(failures, p.ID+": "+err.Error())
			continue
		}
		if cur.Version != p.Version {
			failures = append(failures, fmt.Sprintf("%s: changed since the plan (v%d, plan v%d)", p.ID, cur.Version,
				p.Version))
			continue
		}
		if cur.Status != store.Active {
			failures = append(failures, p.ID+": not active")
			continue
		}
		if _, err := s.Retire(p.ID); err != nil {
			failures = append(failures, p.ID+": "+err.Error())
			continue
		}
		h.Applied++
		applied++
	}
	rec := auditRecord{Time: time.Now().UTC().Format(time.RFC3339), Plan: path, MinPrecision: minPrecision,
		Heuristics: auditOrdered(summaries, order), Applied: applied, Refused: refused, Errors: failures}
	if err := appendJSON(filepath.Join(dir, "audit.log"), rec); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "audit: applied %d, refused %d, errors %d; audit.log %s\n", applied, refused,
		len(failures), filepath.Join(dir, "audit.log")); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("audit: %d facts were not applied; see audit.log", len(failures))
	}
	return nil
}

func auditSummaryIndex(hs []auditHeuristic) (map[string]*auditHeuristic, []string) {
	index := map[string]*auditHeuristic{}
	var order []string
	for i := range hs {
		h := hs[i]
		index[h.Name] = &h
		order = append(order, h.Name)
	}
	return index, order
}

func auditOrdered(index map[string]*auditHeuristic, order []string) []auditHeuristic {
	var out []auditHeuristic
	seen := map[string]bool{}
	for _, name := range order {
		if h := index[name]; h != nil {
			out = append(out, *h)
			seen[name] = true
		}
	}
	// A plan may omit a heuristic that has proposals; keep its summary rather than losing the precision record.
	for name, h := range index {
		if !seen[name] {
			out = append(out, *h)
		}
	}
	return out
}
