package tailnet

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerboseTailscaleLinesAreNotWritten(t *testing.T) {
	var buf bytes.Buffer
	l := newBoundedLog(&buf)
	for _, line := range []string{
		"[v1] PollNetMap: stream=false",
		"control: [v1] TryLogin: fetch control key: EOF",
		"[v2] Device closing",
		"control: [v\x00JSON]1{\"msg\":\"x\"}",
	} {
		l.Logf("%s", line)
	}
	if buf.Len() != 0 {
		t.Fatalf("a helper wrote lines above the default verbosity: %q", buf.String())
	}
	l.Logf("control: %s", "Received error: fetch control key: EOF")
	if !strings.Contains(buf.String(), "Received error") {
		t.Fatalf("a non-verbose line was dropped: %q", buf.String())
	}
}

func TestLogHoldsTheRestBackIntoOneLine(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	l := newBoundedLog(&buf)
	l.now = func() time.Time { return now }
	l.start = now
	for i := range logBudget + 5 {
		l.Logf("control: attempt %d: EOF", i)
	}
	if got := strings.Count(buf.String(), "\n"); got != logBudget {
		t.Fatalf("a window wrote %d lines, want the budget of %d", got, logBudget)
	}
	now = now.Add(logWindow)
	l.Logf("control: attempt after the window")
	out := buf.String()
	if !strings.Contains(out, "5 messages held back in the previous "+logWindow.String()) {
		t.Fatalf("the held-back lines were not reported: %q", out)
	}
	if !strings.Contains(out, "last: control: attempt "+fmt.Sprint(logBudget+4)+": EOF") {
		t.Fatalf("the report does not name the last held-back line: %q", out)
	}
	if !strings.Contains(out, "control: attempt after the window") {
		t.Fatalf("the new window holds no line: %q", out)
	}
}

func TestARepeatedLineIsWrittenOnceAndCounted(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	l := newBoundedLog(&buf)
	l.now = func() time.Time { return now }
	l.start = now
	for range 30 {
		l.Logf("control: %s", "Received error: fetch control key: EOF")
	}
	if got := strings.Count(buf.String(), "\n"); got != 1 {
		t.Fatalf("a repeated failure wrote %d lines, want one and a count", got)
	}
	now = now.Add(logWindow)
	l.Logf("control: something else")
	out := buf.String()
	if !strings.Contains(out, "29 messages held back in the previous "+logWindow.String()) {
		t.Fatalf("the repeats were not counted: %q", out)
	}
	if !strings.Contains(out, "last: control: Received error: fetch control key: EOF") {
		t.Fatalf("the count does not name the repeated line: %q", out)
	}
}

func TestCloseReportsWhatItHeldBack(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	l := newBoundedLog(&buf)
	l.now = func() time.Time { return now }
	l.start = now
	for i := range logBudget + 2 {
		l.Logf("line %d", i)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "2 messages held back") {
		t.Fatalf("close left the held-back lines unreported: %q", buf.String())
	}
}

func TestCappedFileRotatesAtItsLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.log")
	f, err := openCappedFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f.limit = 64
	for range 10 {
		if _, err := f.Write([]byte("0123456789012345678901234567890\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".1"} {
		fi, err := os.Stat(name)
		if err != nil {
			t.Fatalf("rotated log %s: %v", name, err)
		}
		if fi.Size() > 64 {
			t.Fatalf("%s grew to %d bytes, past its limit", name, fi.Size())
		}
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("%s has mode %v, want a private file", name, fi.Mode().Perm())
		}
	}
}

func TestWatchControlGivesUpWithoutAControlConnection(t *testing.T) {
	n := &fixtureNode{}
	var line string
	ended := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchControl(context.Background(), n, time.Millisecond, 20*time.Millisecond,
			func(format string, a ...any) { line = fmt.Sprintf(format, a...) },
			func() { close(ended) })
	}()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("a helper that never held a control connection did not give up")
	}
	<-done
	if !strings.Contains(line, "no control connection") {
		t.Fatalf("the give-up said nothing usable: %q", line)
	}
}

func TestWatchControlKeepsAConnectedNode(t *testing.T) {
	n := &fixtureNode{status: State{Running: true, State: "Running", Online: true}}
	ended := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchControl(ctx, n, time.Millisecond, 5*time.Millisecond, func(string, ...any) {}, func() { close(ended) })
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	select {
	case <-ended:
		t.Fatal("a helper with a control connection gave up")
	default:
	}
}
