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
	ID            string   `json:"id"`
	Version       int      `json:"version"`
	Created       string   `json:"created"`
	Status        string   `json:"status"`
	Author        string   `json:"author,omitempty"`
	Content       string   `json:"content"`
	Keywords      []string `json:"keywords,omitempty"`
	Origin        string   `json:"origin,omitempty"`
	Expires       string   `json:"expires,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	Source        string   `json:"source,omitempty"`
	Evidence      string   `json:"evidence,omitempty"`
	Review        string   `json:"review,omitempty"`
	PromptVersion int      `json:"prompt_version,omitempty"`
	Queries       []string `json:"queries,omitempty"`
}

// FactPath is the file of the fact id in the text history at dir.
func FactPath(dir, id string) string {
	return filepath.Join(dir, id[len(id)-1:], id+".jsonl")
}

// encodeLine writes f as one line, the newline included. Keywords and queries go through the same join and split as
// a row of the base, so a version reads back from the text exactly as from the base.
func encodeLine(f Fact) ([]byte, error) {
	l := textLine{ID: f.ID, Version: f.Version, Created: f.Created.UTC().Format(time.RFC3339Nano), Status: f.Status,
		Author: f.Author, Content: f.Content, Keywords: asStored(f.Keywords), Origin: f.Origin, Expires: f.Expires,
		Kind: f.Kind, Scope: f.Scope, Source: f.Source, Evidence: f.Evidence, Review: f.Review,
		PromptVersion: f.PromptVersion, Queries: asStored(f.Queries)}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(l); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// asStored is a list as a row of the base reads it back.
func asStored(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	joined := strings.Join(list, ", ")
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ", ")
}

func decodeLine(b []byte) (Fact, error) {
	var l textLine
	if err := json.Unmarshal(b, &l); err != nil {
		return Fact{}, err
	}
	if l.ID == "" || l.Version < 1 || (l.Status != Active && l.Status != Retired) {
		return Fact{}, fmt.Errorf("not a fact version: %.80s", b)
	}
	created, err := time.Parse(time.RFC3339Nano, l.Created)
	if err != nil {
		return Fact{}, fmt.Errorf("created %q: %w", l.Created, err)
	}
	return Fact{ID: l.ID, Version: l.Version, Content: l.Content, Keywords: l.Keywords, Origin: l.Origin,
		Created: created.UTC(), Status: l.Status, Author: l.Author, Expires: l.Expires, Queries: l.Queries,
		Meta: Meta{Kind: l.Kind, Scope: l.Scope, Source: l.Source, Evidence: l.Evidence, Review: l.Review,
			PromptVersion: l.PromptVersion}}, nil
}

// ParseFacts reads the versions of one fact file, in version order. A line repeated as it is counts once: two
// processes may append the same version, and a hand-made union of two sides repeats the common ones. Two different
// lines with one version number, or lines of another fact, are an error: the file needs a merge.
func ParseFacts(data []byte) ([]Fact, error) {
	var out []Fact
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		f, err := decodeLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if len(out) > 0 && f.ID != out[0].ID {
			return nil, fmt.Errorf("line %d: fact %s in the file of %s", n, f.ID, out[0].ID)
		}
		out = append(out, f)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	kept := out[:0]
	for _, f := range out {
		if n := len(kept); n > 0 && kept[n-1].Version == f.Version {
			if !same(kept[n-1], f) {
				return nil, fmt.Errorf("fact %s has two different versions %d", f.ID, f.Version)
			}
			continue
		}
		kept = append(kept, f)
	}
	return kept, nil
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

// appendFacts adds versions at the end of the file at path in one write, creating it when the fact is new.
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
	return f.Close()
}
