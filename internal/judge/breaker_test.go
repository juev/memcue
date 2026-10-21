package judge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFirstFailurePausesAndOKClears(t *testing.T) {
	file := filepath.Join(t.TempDir(), "judge.pause")
	b := New(file)
	if err := b.Check(); err != nil {
		t.Fatalf("fresh breaker: %v", err)
	}
	if err := b.Fail(errors.New("boom")); err == nil {
		t.Fatal("Fail must return the error")
	}
	if err := b.Check(); !errors.Is(err, ErrPaused) {
		t.Fatalf("after failure = %v, want ErrPaused", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("pause file: %v", err)
	}
	b.OK()
	if err := b.Check(); err != nil {
		t.Fatalf("after OK: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("pause file after OK: %v", err)
	}
}

func TestPauseDoublesAndCaps(t *testing.T) {
	b := New("")
	_ = b.Fail(errors.New("1"))
	if _, left := b.Paused(); left < 50*time.Second || left > PauseFirst+time.Second {
		t.Fatalf("first pause = %v", left)
	}
	_ = b.Fail(errors.New("2"))
	if _, left := b.Paused(); left < 100*time.Second {
		t.Fatalf("second pause = %v, want doubling", left)
	}
	for i := 0; i < 10; i++ {
		_ = b.Fail(errors.New("again"))
	}
	if _, left := b.Paused(); left > PauseMax || left < PauseMax-2*time.Second {
		t.Fatalf("capped pause = %v", left)
	}
}

func TestPauseFromAnotherProcessExpires(t *testing.T) {
	file := filepath.Join(t.TempDir(), "judge.pause")
	future := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	if err := os.WriteFile(file, []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New(file).Check(); !errors.Is(err, ErrPaused) {
		t.Fatalf("file pause = %v, want ErrPaused", err)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if err := os.WriteFile(file, []byte(past), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New(file).Check(); err != nil {
		t.Fatalf("expired file pause = %v", err)
	}
}

func TestConfigureKeepsTheFilePause(t *testing.T) {
	file := filepath.Join(t.TempDir(), "judge.pause")
	if err := os.WriteFile(file, []byte(time.Now().Add(time.Minute).UTC().Format(time.RFC3339)), 0o600); err != nil {
		t.Fatal(err)
	}
	Configure(file)
	if err := Shared.Check(); !errors.Is(err, ErrPaused) {
		t.Fatalf("configured shared breaker = %v, want ErrPaused", err)
	}
	Shared.Reset()
	if err := Shared.Check(); err != nil {
		t.Fatalf("after reset = %v", err)
	}
}
