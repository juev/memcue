package store

import (
	"errors"
	"fmt"
	"time"

	"github.com/juev/memcue/internal/hlc"
)

// The registers of a synced fact (#714, docs/specs/sync-server.md R5). Each is a last-writer-wins value with its own
// HLC stamp; for each pair of fact and register the greater stamp wins.
const (
	RegContent = "content"
	RegKind    = "kind"
	RegScope   = "scope"
	RegExpires = "expires"
	RegStatus  = "status"
	RegOrigin  = "origin"
)

// Registers lists every register in a fixed order.
var Registers = []string{RegContent, RegKind, RegScope, RegExpires, RegStatus, RegOrigin}

// Stamps holds the HLC stamp (hlc.Stamp.String) of each register; a missing one is the zero stamp.
type Stamps map[string]string

// Row is a fact of a synced base: its one current version, the stamps of its registers, the change number the
// server gave its last change (0 before the server has seen it) and the time of that change.
type Row struct {
	Fact
	Stamps  Stamps
	Seq     int64
	Updated time.Time
}

// Change is one write of a synced base: new values of some registers of one fact, all stamped with Stamp.
type Change struct {
	OpID string // identifies the write for a retry
	ID   string // the fact
	// Create makes the fact: Values hold every register, Created and Source describe it.
	Create  bool
	Created time.Time
	Source  string
	Values  map[string]string
	Stamp   string
	// Base is the content stamp the new content was made from; it tells a correction made after the stored content
	// from one made beside it (R6).
	Base string
}

// Merged is what ApplyChange did to a row.
type Merged struct {
	// Changed: a register took the change's value, so the row has a new version.
	Changed bool
	// Loser is the content a concurrent correction pushed out, when HasLoser; the server keeps it as a new fact.
	Loser    string
	HasLoser bool
}

// ErrIDTaken: a create names an id that another fact holds.
var ErrIDTaken = errors.New("fact id is held by another fact")

// ErrChange: a change that no base can apply.
var ErrChange = errors.New("malformed fact change")

// ApplyChange merges ch into row, which is the stored fact when exists and empty otherwise. Applying the same change
// twice, or a set of changes in any order, leaves the registers and stamps the same (R5). Only Version counts how
// often a register changed and so depends on the order.
func ApplyChange(row *Row, exists bool, ch Change) (Merged, error) {
	if err := checkChange(ch); err != nil {
		return Merged{}, err
	}
	switch {
	case !exists && !ch.Create:
		return Merged{}, fmt.Errorf("%w: %s", ErrNotFound, ch.ID)
	case !exists:
		*row = Row{Fact: Fact{ID: ch.ID, Created: ch.Created.UTC(), Meta: Meta{Source: ch.Source}}, Stamps: Stamps{}}
	case row.ID != ch.ID:
		return Merged{}, fmt.Errorf("%w: change of %s applied to %s", ErrChange, ch.ID, row.ID)
	case ch.Create && !row.Created.Equal(ch.Created):
		return Merged{}, fmt.Errorf("%w: %s", ErrIDTaken, ch.ID)
	}
	if row.Stamps == nil {
		row.Stamps = Stamps{}
	}
	var m Merged
	if v, ok := ch.Values[RegContent]; ok && exists && !ch.Create &&
		ch.Base != row.Stamps[RegContent] && v != row.Content {
		m.HasLoser = true
		if ch.Stamp > row.Stamps[RegContent] {
			m.Loser = row.Content
		} else {
			m.Loser = v
		}
	}
	for _, reg := range Registers {
		v, ok := ch.Values[reg]
		if !ok || ch.Stamp <= row.Stamps[reg] {
			continue
		}
		setRegister(&row.Fact, reg, v)
		row.Stamps[reg] = ch.Stamp
		m.Changed = true
	}
	if m.Changed {
		row.Version++
	}
	return m, nil
}

func checkChange(ch Change) error {
	if ch.ID == "" || len(ch.Values) == 0 {
		return fmt.Errorf("%w: no fact or no values", ErrChange)
	}
	if s, err := hlc.Parse(ch.Stamp); err != nil || s.IsZero() {
		return fmt.Errorf("%w: stamp %q", ErrChange, ch.Stamp)
	}
	if _, err := hlc.Parse(ch.Base); err != nil {
		return fmt.Errorf("%w: base %q", ErrChange, ch.Base)
	}
	for reg, v := range ch.Values {
		if !known(reg) {
			return fmt.Errorf("%w: register %q", ErrChange, reg)
		}
		if reg == RegStatus && v != Active && v != Retired {
			return fmt.Errorf("%w: status %q", ErrChange, v)
		}
		if reg == RegContent && v == "" {
			return fmt.Errorf("%w: empty content", ErrChange)
		}
	}
	if ch.Create {
		for _, reg := range Registers {
			if _, ok := ch.Values[reg]; !ok {
				return fmt.Errorf("%w: create without %s", ErrChange, reg)
			}
		}
		if ch.Created.IsZero() {
			return fmt.Errorf("%w: create without a creation time", ErrChange)
		}
	}
	return nil
}

func known(reg string) bool {
	for _, r := range Registers {
		if r == reg {
			return true
		}
	}
	return false
}

// Register is the value of one register of f.
func Register(f Fact, reg string) string {
	switch reg {
	case RegContent:
		return f.Content
	case RegKind:
		return f.Kind
	case RegScope:
		return f.Scope
	case RegExpires:
		return f.Expires
	case RegStatus:
		return f.Status
	case RegOrigin:
		return f.Origin
	}
	return ""
}

func setRegister(f *Fact, reg, v string) {
	switch reg {
	case RegContent:
		f.Content = v
	case RegKind:
		f.Kind = v
	case RegScope:
		f.Scope = v
	case RegExpires:
		f.Expires = v
	case RegStatus:
		f.Status = v
	case RegOrigin:
		f.Origin = v
	}
}

// Diff holds the registers whose values differ between before and after, with the values of after.
func Diff(before, after Fact) map[string]string {
	values := map[string]string{}
	for _, reg := range Registers {
		if Register(before, reg) != Register(after, reg) {
			values[reg] = Register(after, reg)
		}
	}
	return values
}
