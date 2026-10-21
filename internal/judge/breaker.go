// Package judge keeps the pause shared by the three roles of the one Jev endpoint: recall rerank, dream triage and
// the duplicate check. A failed endpoint is left alone for a while instead of paying another timeout in every role.
package judge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PauseFirst is the pause after the first failure; every further failure doubles it, up to PauseMax.
const (
	PauseFirst = 60 * time.Second
	PauseMax   = 15 * time.Minute
)

// ErrPaused is returned without a call while the endpoint is being left alone.
var ErrPaused = errors.New("judge endpoint is paused after a failure")

// Breaker is one pause for one endpoint. It is safe for concurrent use; the file, when set, lets a fresh process see
// a pause written by the previous one.
type Breaker struct {
	mu       sync.Mutex
	pausedTo time.Time
	pause    time.Duration
	file     string
}

// New returns a breaker; an empty file keeps the pause in memory only.
func New(file string) *Breaker {
	return &Breaker{file: file}
}

// Shared is the breaker the command configures once and every judge role uses.
var Shared = New("")

// Configure points the shared breaker at stateFile and clears the in-memory pause. A pause already in the file is
// still honoured by the next Check.
func Configure(stateFile string) {
	Shared.mu.Lock()
	defer Shared.mu.Unlock()
	Shared.file = stateFile
	Shared.pause = 0
	Shared.pausedTo = time.Time{}
}

// Check returns ErrPaused, with the time left, while the endpoint is paused.
func (b *Breaker) Check() error {
	if paused, left := b.Paused(); paused {
		return fmt.Errorf("%w: %s left", ErrPaused, left.Round(time.Second))
	}
	return nil
}

// Paused reports whether the endpoint is being left alone, and for how much longer.
func (b *Breaker) Paused() (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	left := b.leftLocked()
	if left <= 0 {
		b.pausedTo = time.Time{}
		return false, 0
	}
	return true, left
}

// Fail records a failed call: the first pause is PauseFirst, then doubling up to PauseMax. It returns err unchanged.
func (b *Breaker) Fail(err error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pause < PauseFirst {
		b.pause = PauseFirst
	} else if b.pause *= 2; b.pause > PauseMax {
		b.pause = PauseMax
	}
	b.pausedTo = time.Now().Add(b.pause)
	if b.file != "" {
		if mkErr := os.MkdirAll(filepath.Dir(b.file), 0o700); mkErr == nil {
			_ = os.WriteFile(b.file, []byte(b.pausedTo.UTC().Format(time.RFC3339)), 0o600)
		}
	}
	return err
}

// OK clears the pause after a valid answer and removes the file.
func (b *Breaker) OK() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pause, b.pausedTo = 0, time.Time{}
	if b.file != "" {
		_ = os.Remove(b.file)
	}
}

// Reset clears the pause and the file; tests use it between cases.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pause, b.pausedTo = 0, time.Time{}
	if b.file != "" {
		_ = os.Remove(b.file)
	}
}

// leftLocked is the remaining pause, from memory and from the file a previous process may have written.
func (b *Breaker) leftLocked() time.Duration {
	until := b.pausedTo
	if b.file != "" {
		if data, err := os.ReadFile(b.file); err == nil {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data))); err == nil && t.After(until) {
				until = t
			}
		}
	}
	return time.Until(until)
}
