package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/juev/memcue/internal/jsonctx"
	"github.com/juev/memcue/internal/store"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// invocation is local to a command entry, never ambient or mutable global state.
// Its context exists before flags, input parsing, filesystem discovery or stores.
type invocation struct {
	ctx    context.Context
	stop   context.CancelFunc
	effect bool
}

func newInvocation() *invocation {
	signalCtx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// A copy of the whole base into PostgreSQL is bounded by signals, as local maintenance is, not by the budget of
	// a fact operation.
	if os.Getenv("MEMCUE_POSTGRES_DSN") == "" || migratesToPostgres(os.Args[1:]) {
		return &invocation{ctx: signalCtx, stop: stopSignal}
	}
	budget := 4 * time.Second
	// Explicit onboarding is not a fact operation. Its configured lifecycle
	// budget must cover both startup and delivery of the authorization response.
	if len(os.Args) > 2 && os.Args[1] == "connect" && os.Args[2] == "up" {
		budget = 5 * time.Second
		if value, err := time.ParseDuration(os.Getenv("MEMCUE_TAILSCALE_CONNECT_TIMEOUT")); err == nil && value >= time.Second && value <= 30*time.Second {
			budget = value
		}
	}
	ctx, cancel := context.WithTimeout(signalCtx, budget)
	return &invocation{ctx: ctx, stop: func() { cancel(); stopSignal() }}
}
func (i *invocation) close() { i.stop() }

func (inv *invocation) inputBytesLimit(in io.Reader, limit int) ([]byte, error) {
	var stop func() bool
	if f, ok := in.(*os.File); ok {
		if deadline, has := inv.ctx.Deadline(); has {
			_ = f.SetReadDeadline(deadline)
		}
		stop = context.AfterFunc(inv.ctx, func() { _ = f.SetReadDeadline(time.Now()) })
		defer func() { stop(); _ = f.SetReadDeadline(time.Time{}) }()
	}
	var out []byte
	buf := make([]byte, 4096)
	depth := 0
	started, quoted, escaped := false, false, false
	for {
		if e := inv.ctx.Err(); e != nil {
			return nil, e
		}
		n, e := in.Read(buf)
		for i, c := range buf[:n] {
			if len(out) >= limit {
				return nil, fmt.Errorf("control JSON input exceeds %d byte limit", limit)
			}
			out = append(out, c)
			if len(out)%256 == 0 {
				if e := inv.ctx.Err(); e != nil {
					return nil, e
				}
			}
			if quoted {
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					quoted = false
				}
				continue
			}
			if c == '"' {
				quoted = true
				started = true
				continue
			}
			if c == '{' || c == '[' {
				depth++
				started = true
			}
			if c == '}' || c == ']' {
				depth--
				if started && depth == 0 {
					return out, inv.ctx.Err()
				}
			}
			if !started && c != ' ' && c != '\n' && c != '\r' && c != '\t' {
				started = true
			}
			if started && depth == 0 && (c == '\n' || c == '\r' || c == ' ' || c == '\t') {
				return out, inv.ctx.Err()
			}
			_ = i
		}
		if e != nil {
			if e := invocationContextError(inv.ctx); e != nil {
				return nil, e
			}
			if e == io.EOF && len(out) > 0 {
				return out, nil
			}
			return nil, e
		}
	}
}
func (inv *invocation) decodeInput(in io.Reader, target any) error {
	b, e := inv.inputBytes(in)
	if e != nil {
		return e
	}
	return jsonctx.Unmarshal(inv.ctx, b, target)
}
func (inv *invocation) readFile(path string) ([]byte, error) {
	if e := inv.ctx.Err(); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input must be a regular file")
	}
	var out []byte
	buf := make([]byte, 4096)
	for {
		if e := inv.ctx.Err(); e != nil {
			return nil, e
		}
		n, e := f.Read(buf)
		out = append(out, buf[:n]...)
		if e == io.EOF {
			return out, inv.ctx.Err()
		}
		if e != nil {
			return nil, e
		}
	}
}

func (inv *invocation) local(s *store.Store, e error) (*store.Store, error) {
	if e == nil {
		s.BindLocalContext(inv.ctx)
	}
	return s, e
}

// Control envelopes have an explicit 64MiB ceiling. Stored fact/history caps
// and local fact formats are unchanged; oversize input is refused, never cut.
func (inv *invocation) inputBytes(in io.Reader) ([]byte, error) {
	return inv.inputBytesLimit(in, 64<<20)
}
func (inv *invocation) readAll(in io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		if e := inv.ctx.Err(); e != nil {
			return nil, e
		}
		n, e := in.Read(buf)
		if len(out)+n > 64<<20 {
			return nil, fmt.Errorf("control JSON input exceeds 64 MiB")
		}
		out = append(out, buf[:n]...)
		if e == io.EOF {
			return out, inv.ctx.Err()
		}
		if e != nil {
			return nil, e
		}
	}
}

type invocationWriter struct {
	inv *invocation
	out io.Writer
}

func (inv *invocation) writer(out io.Writer) io.Writer {
	if _, ok := out.(invocationWriter); ok {
		return out
	}
	return invocationWriter{inv, out}
}
func (w invocationWriter) Write(p []byte) (total int, err error) {
	if f, ok := w.out.(*os.File); ok {
		dataOutput := f == os.Stdout
		// Inherited stdout may be a blocking descriptor that Go did not enroll
		// in its poller. Give this write an owned pollable duplicate, retaining
		// the original file and restoring its open-description flags afterwards.
		file, release, e := deadlineOutputFile(f)
		if e != nil {
			return 0, e
		}
		defer release()
		f = file
		w.out = file
		if d, yes := w.inv.ctx.Deadline(); yes {
			// Leave diagnostic delivery and cleanup inside the aggregate budget.
			if dataOutput {
				d = d.Add(-50 * time.Millisecond)
			}
			_ = f.SetWriteDeadline(d)
		}
		stop := context.AfterFunc(w.inv.ctx, func() { _ = f.SetWriteDeadline(time.Now()) })
		defer func() { stop(); _ = f.SetWriteDeadline(time.Time{}) }()
	}
	for total < len(p) {
		if e := w.inv.ctx.Err(); e != nil {
			return total, e
		}
		end := min(total+4096, len(p))
		n, e := w.out.Write(p[total:end])
		total += n
		if e != nil {
			if e := invocationContextError(w.inv.ctx); e != nil {
				return total, e
			}
			return total, e
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, w.inv.ctx.Err()
}

type invocationEncoder struct {
	inv    *invocation
	out    io.Writer
	escape bool
}

func (inv *invocation) encoder(out io.Writer) *invocationEncoder {
	return &invocationEncoder{inv: inv, out: out, escape: true}
}
func (e invocationEncoder) Encode(v any) error {
	b, err := jsonctx.Marshal(e.inv.ctx, v, e.escape)
	if err != nil {
		return err
	}
	_, err = e.inv.writer(e.out).Write(append(b, '\n'))
	return err
}

func (e *invocationEncoder) SetEscapeHTML(escape bool) { e.escape = escape }

// The poller deadline can fire just before the context timer is scheduled.
// Both represent the same invocation deadline; do not publish an unrelated
// I/O timeout merely because the timer goroutine has not run yet.
func invocationContextError(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func deadlineOutputFile(f *os.File) (*os.File, func(), error) {
	noop := func() {}
	if !errors.Is(f.SetWriteDeadline(time.Time{}), os.ErrNoDeadline) {
		return f, noop, nil
	}
	info, e := f.Stat()
	if e != nil {
		return nil, noop, e
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return f, noop, nil
	}
	raw, e := f.SyscallConn()
	if e != nil {
		return nil, noop, e
	}
	fd, flags := -1, 0
	var syscallErr error
	e = raw.Control(func(original uintptr) {
		flags, syscallErr = unix.FcntlInt(original, unix.F_GETFL, 0)
		if syscallErr != nil {
			return
		}
		fd, syscallErr = syscall.Dup(int(original))
		if syscallErr != nil {
			return
		}
		syscall.CloseOnExec(fd)
		syscallErr = syscall.SetNonblock(fd, true)
	})
	if e != nil || syscallErr != nil {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
		if e != nil {
			return nil, noop, e
		}
		return nil, noop, syscallErr
	}
	owned := os.NewFile(uintptr(fd), "invocation output")
	return owned, func() {
		if flags&syscall.O_NONBLOCK == 0 {
			_ = syscall.SetNonblock(fd, false)
		}
		_ = owned.Close()
	}, nil
}

// FlagSet diagnostics and library errors also use os.Stderr. At the native
// process boundary give those writes the same deadline as command output.
func (inv *invocation) bindStderr() func() {
	original := os.Stderr
	file, release, err := deadlineOutputFile(original)
	if err != nil {
		return func() {}
	}
	os.Stderr = file
	if d, yes := inv.ctx.Deadline(); yes {
		_ = file.SetWriteDeadline(d)
	}
	stop := context.AfterFunc(inv.ctx, func() { _ = file.SetWriteDeadline(time.Now()) })
	return func() { stop(); os.Stderr = original; _ = file.SetWriteDeadline(time.Time{}); release() }
}
