package store

import (
	"context"
	"encoding/binary"
	"github.com/jackc/pgx/v5"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The native pgx Close writes Terminate into a real net.Pipe whose framing
// proxy stops reading after forwarding ROLLBACK. TCP PostgreSQL remains genuine.
func TestRepairCloseRespectsTotalDeadline(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	db, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	if _, err = db.Exec(context.Background(), "TRUNCATE memcue.facts"); err != nil {
		t.Fatal(err)
	}
	cfg := s.remote.config.Copy()
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	reached := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	var once sync.Once
	cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		a, b := net.Pipe()
		up, err := (&net.Dialer{}).DialContext(ctx, "tcp", target)
		if err != nil {
			_ = a.Close()
			_ = b.Close()
			return nil, err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = b.Close(); _ = up.Close() }()
			copied := make(chan struct{})
			go func() { _, _ = io.Copy(b, up); close(copied) }()
			defer func() { _ = b.Close(); _ = up.Close(); <-copied }()
			var size [4]byte
			if _, err := io.ReadFull(b, size[:]); err != nil {
				return
			}
			body := make([]byte, int(binary.BigEndian.Uint32(size[:]))-4)
			if _, err := io.ReadFull(b, body); err != nil {
				return
			}
			_, _ = up.Write(size[:])
			_, _ = up.Write(body)
			for {
				var header [5]byte
				if _, err := io.ReadFull(b, header[:]); err != nil {
					return
				}
				body = make([]byte, int(binary.BigEndian.Uint32(header[1:]))-4)
				if _, err := io.ReadFull(b, body); err != nil {
					return
				}
				if _, err := up.Write(header[:]); err != nil {
					return
				}
				if _, err := up.Write(body); err != nil {
					return
				}
				// A read is one statement (#700): the proxy stops reading after forwarding it.
				if header[0] == 'P' || header[0] == 'Q' {
					once.Do(func() { close(reached) })
					<-release
				}
			}
		}()
		return a, nil
	}
	useConfig(s, cfg)
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := s.Get("missing"); done <- err }()
	select {
	case <-reached:
	case err := <-done:
		close(release)
		wg.Wait()
		t.Fatalf("close path not reached: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("proxy setup deadline")
	}
	<-time.After(time.Until(start.Add(10200 * time.Millisecond)))
	completed := false
	select {
	case <-done:
		completed = true
	default:
	}
	close(release)
	if !completed {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("safety release failed")
		}
	}
	wg.Wait()
	if !completed {
		t.Fatal("native Close exceeded total operation deadline")
	}
}
