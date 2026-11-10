package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The text history keeps every version of every fact as JSON lines, one file per fact, so that git stores and sends
// only the facts that changed (#530). A file is the history of one fact as a machine sees it: one line per version,
// in version order. The files live under a directory named by the last character of the fact's ID: the first
// characters of a ULID encode its time and are the same for every fact, the last one is random.

// textLine is one version of a fact as a line of its file. The field order is the order of the line, so every
// machine writes the same bytes for the same version.
type textLine struct {
	ID            string `json:"id"`
	Version       int    `json:"version"`
	Created       string `json:"created"`
	Status        string `json:"status"`
	Author        string `json:"author,omitempty"`
	Content       string `json:"content"`
	Origin        string `json:"origin,omitempty"`
	Expires       string `json:"expires,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Scope         string `json:"scope,omitempty"`
	Source        string `json:"source,omitempty"`
	Evidence      string `json:"evidence,omitempty"`
	Review        string `json:"review,omitempty"`
	PromptVersion int    `json:"prompt_version,omitempty"`
	Channel       string `json:"channel,omitempty"`
	WriteID       string `json:"write_id,omitempty"`
}

// FactPath is the file of the fact id in the text history at dir.
func FactPath(dir, id string) string {
	return filepath.Join(dir, id[len(id)-1:], id+".jsonl")
}

// encodeLine writes the current content-only representation, including preserved metadata, as one JSON line.
func encodeLine(f Fact) ([]byte, error) {
	l := textLine{ID: f.ID, Version: f.Version, Created: f.Created.UTC().Format(time.RFC3339Nano), Status: f.Status,
		Author: f.Author, Content: f.Content, Origin: f.Origin, Expires: f.Expires,
		Kind: f.Kind, Scope: f.Scope, Source: f.Source, Evidence: f.Evidence, Review: f.Review,
		PromptVersion: f.PromptVersion, Channel: f.Channel, WriteID: f.WriteID}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(l); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// decodedVersion separates a supplied operational identity from the canonical
// legacy class. Temporary fingerprints must not participate in version equality.
type decodedVersion struct {
	fact   Fact
	base   string
	legacy bool
}

func decodeLine(b []byte) (decodedVersion, error) {
	var l struct {
		textLine
		Keywords json.RawMessage `json:"keywords"`
		Queries  json.RawMessage `json:"queries"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return decodedVersion{}, err
	}
	if l.ID == "" || l.Version < 1 || (l.Status != Active && l.Status != Retired) {
		return decodedVersion{}, fmt.Errorf("not a fact version: %.80s", b)
	}
	created, err := time.Parse(time.RFC3339Nano, l.Created)
	if err != nil {
		return decodedVersion{}, fmt.Errorf("created %q: %w", l.Created, err)
	}
	f := Fact{ID: l.ID, Version: l.Version, Content: l.Content, Origin: l.Origin,
		Created: created.UTC(), Status: l.Status, Author: l.Author, Expires: l.Expires,
		Meta: Meta{Kind: l.Kind, Scope: l.Scope, Source: l.Source, Evidence: l.Evidence, Review: l.Review,
			PromptVersion: l.PromptVersion, Channel: l.Channel}, WriteID: l.WriteID}
	auxValue := func(raw json.RawMessage) (string, error) {
		if len(raw) == 0 {
			return "", nil
		}
		var list []string
		if err := json.Unmarshal(raw, &list); err == nil {
			return strings.Join(list, ", "), nil
		}
		var scalar string
		err := json.Unmarshal(raw, &scalar)
		return scalar, err
	}
	kw, err := auxValue(l.Keywords)
	if err != nil {
		return decodedVersion{}, err
	}
	qu, err := auxValue(l.Queries)
	if err != nil {
		return decodedVersion{}, err
	}
	plain := f
	plain.WriteID = ""
	base := projectedWriteID(plain, kw, qu)
	if f.WriteID != "" {
		storedBase, _, _ := strings.Cut(f.WriteID, ".")
		// Empty obsolete fields are redundant on a current row. Nonempty
		// fields must agree with its supplied identity, never replace it.
		if (kw != "" || qu != "") && base != storedBase {
			return decodedVersion{}, fmt.Errorf("fact %s has legacy fields inconsistent with write_id", f.ID)
		}
		base = storedBase
	}
	return decodedVersion{fact: f, base: base, legacy: l.Keywords != nil || l.Queries != nil}, nil
}

func equivalentVersion(a, b decodedVersion) bool {
	af, bf := a.fact, b.fact
	if af.WriteID != "" && bf.WriteID != "" && af.WriteID != bf.WriteID {
		return false
	}
	af.WriteID, bf.WriteID = "", ""
	return a.base == b.base && same(af, bf)
}

// ParseFacts reads one complete fact history. Canonically equivalent retries at
// the same version count once; different records at that version require a merge.
// Deduplication precedes occurrence counting, so retries cannot invent writes.
func ParseFacts(data []byte) ([]Fact, error) {
	var out []decodedVersion
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		row, err := decodeLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if len(out) > 0 && row.fact.ID != out[0].fact.ID {
			return nil, fmt.Errorf("line %d: fact %s in the file of %s", n, row.fact.ID, out[0].fact.ID)
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].fact.Version < out[j].fact.Version })
	kept := out[:0]
	for _, row := range out {
		if n := len(kept); n > 0 && kept[n-1].fact.Version == row.fact.Version {
			previous := &kept[n-1]
			if !equivalentVersion(*previous, row) {
				return nil, fmt.Errorf("fact %s has two different versions %d", row.fact.ID, row.fact.Version)
			}
			if previous.fact.WriteID == "" {
				previous.fact.WriteID = row.fact.WriteID
			}
			previous.legacy = previous.legacy || row.legacy
			continue
		}
		kept = append(kept, row)
	}
	// The same canonical classes govern both retry equality and full-history
	// ranks. Stored identities contribute occurrences but are never renumbered.
	keys := make([]string, len(kept))
	legacyClasses := map[string]bool{}
	for i, row := range kept {
		plain := row.fact
		plain.WriteID = ""
		empty := projectedWriteID(plain, "", "")
		plain.WriteID = row.base
		keys[i] = legacyOccurrenceKey(plain)
		legacyClasses[keys[i]] = legacyClasses[keys[i]] || row.legacy || row.fact.WriteID != "" && row.base == empty
	}
	counts := map[string]int{}
	var facts []Fact
	for i, row := range kept {
		counts[keys[i]]++
		f := row.fact
		if f.WriteID == "" && legacyClasses[keys[i]] {
			f.WriteID = legacyOccurrenceID(row.base, counts[keys[i]])
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// ReadFactFile reads the history of one fact; a missing file is a fact with no versions.
func ReadFactFile(path string) ([]Fact, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	facts, err := ParseFacts(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return facts, nil
}

// EncodeFacts is the text of a fact file holding versions, in the order given.
func EncodeFacts(versions []Fact) ([]byte, error) {
	var b bytes.Buffer
	for _, f := range versions {
		line, err := encodeLine(f)
		if err != nil {
			return nil, err
		}
		b.Write(line)
	}
	return b.Bytes(), nil
}

// WriteFactFile replaces the file at path with versions. It writes a file next to it and renames it into place, so a
// reader sees either the old history or the new one.
func WriteFactFile(path string, versions []Fact) error {
	data, err := EncodeFacts(versions)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.jsonl")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// appendFacts adds versions at the end of the file at path in one write, creating it when the fact is new. Appending
// to a file does not change its directory's modification time, which is what an open checks for changes (see
// textstore.go), so another cache of the same history would never read the new version (#709). appendFacts therefore
// sets that time itself, after the write: an open that sees the new time reads the directory and finds the version.
func appendFacts(path string, versions []Fact) error {
	data, err := EncodeFacts(versions)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(filepath.Dir(path), time.Time{}, time.Now())
}
