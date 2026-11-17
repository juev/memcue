package tailnet

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// A helper outlives the outages around it, so what it writes is bounded twice (#777). Every message goes through
// a throttle: a tailscale line above the default verbosity is dropped, as logtail drops it at its stderr level 0,
// and no more than logBudget lines are written per logWindow — the rest becomes one line naming the count and the
// last of them. A helper that owns its log file rotates it at logLimit, so a run nobody watches cannot fill the
// disk it runs on.

const (
	// logWindow is the period a throttle counts in, and logBudget the lines it writes in one before it holds
	// the rest back.
	logWindow = 5 * time.Minute
	logBudget = 20
	// logLimit is the size a helper's log file grows to before it rotates; one rotated file is kept.
	logLimit = 1 << 20
)

// boundedLog is where the helper writes diagnostics: tsnet's Logf and UserLogf, and the standard logger.
type boundedLog struct {
	mu    sync.Mutex
	w     io.Writer
	now   func() time.Time
	start time.Time
	used  int
	held  int
	last  string
	prev  string
}

func newBoundedLog(w io.Writer) *boundedLog {
	now := time.Now
	return &boundedLog{w: w, now: now, start: now()}
}

// Write takes one message of the standard logger.
func (l *boundedLog) Write(p []byte) (int, error) {
	l.Logf("%s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Logf writes one message unless this window already holds its budget.
func (l *boundedLog) Logf(format string, a ...any) {
	line := strings.TrimRight(fmt.Sprintf(format, a...), "\n")
	if verboseLine(line) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.start) >= logWindow {
		l.heldBack(now)
		l.start, l.used, l.held, l.last = now, 0, 0, ""
	}
	// A retry loop writes the same failure again and again: the first one is the message, the rest are a count.
	if line == l.prev {
		l.held++
		l.last = line
		return
	}
	l.prev = line
	if l.used < logBudget {
		l.used++
		l.write(now, line)
		return
	}
	l.held++
	l.last = line
}

// Close names what the current window held back, so a helper that exits leaves its last failure in the log.
func (l *boundedLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.heldBack(l.now())
	return nil
}

// always writes a message the reader is owed whatever the window holds: the reason a helper gives up must not be
// counted as one more line of the flood it explains.
func (l *boundedLog) always(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.write(l.now(), strings.TrimRight(fmt.Sprintf(format, a...), "\n"))
}

func (l *boundedLog) heldBack(now time.Time) {
	if l.held == 0 {
		return
	}
	l.write(now, fmt.Sprintf("%d messages held back in the previous %s; last: %s",
		l.held, now.Sub(l.start).Round(time.Second), l.last))
	l.held, l.last = 0, ""
}

func (l *boundedLog) write(now time.Time, line string) {
	_, _ = fmt.Fprintf(l.w, "%s %s\n", now.Format(time.RFC3339), line)
}

// verboseLine reports a message tailscale marks above the default verbosity. The caller's prefix comes before the
// marker, so it is searched for; logtail removes the same marker to decide what its stderr level 0 prints.
func verboseLine(line string) bool {
	rest := line
	for {
		i := strings.Index(rest, "[v")
		if i < 0 {
			return false
		}
		rest = rest[i+2:]
		if strings.HasPrefix(rest, "\x00JSON]") ||
			(len(rest) >= 2 && rest[0] >= '1' && rest[0] <= '9' && rest[1] == ']') {
			return true
		}
	}
}

// cappedFile is a helper's own log file with a size cap: a write that would pass the limit makes the file
// <path>.1 and starts a new one.
type cappedFile struct {
	mu    sync.Mutex
	path  string
	f     *os.File
	size  int64
	limit int64
}

func openCappedFile(path string) (*cappedFile, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("helper log %s is not a regular file", path)
	}
	return &cappedFile{path: path, f: f, size: fi.Size(), limit: logLimit}, nil
}

func (c *cappedFile) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size+int64(len(p)) > c.limit {
		if err := c.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := c.f.Write(p)
	c.size += int64(n)
	return n, err
}

func (c *cappedFile) rotate() error {
	_ = c.f.Close()
	_ = os.Remove(c.path + ".1")
	if err := os.Rename(c.path, c.path+".1"); err != nil {
		return err
	}
	f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	c.f, c.size = f, 0
	return nil
}

func (c *cappedFile) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.f.Close()
}

// helperDiagnostics is where a run records what happens to it (#777): the log file the caller named, or the
// process's stderr. The second result closes both.
func helperDiagnostics(path string) (*boundedLog, func() error, error) {
	if path == "" {
		diag := newBoundedLog(os.Stderr)
		return diag, diag.Close, nil
	}
	file, err := openCappedFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("helper log: %w", err)
	}
	diag := newBoundedLog(file)
	return diag, func() error {
		err := diag.Close()
		if cerr := file.Close(); err == nil {
			err = cerr
		}
		return err
	}, nil
}
