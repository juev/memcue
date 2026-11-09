package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestRepairLostCommitReplyIsAmbiguous(t *testing.T) {
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
	committed := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
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
			frontDone := make(chan struct{})
			defer func() { _ = up.Close(); _ = b.Close(); <-frontDone }()
			go func() {
				defer close(frontDone)
				defer func() { _ = up.Close() }()
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
				_, _ = io.Copy(up, b)
			}()
			for {
				var header [5]byte
				if _, err := io.ReadFull(up, header[:]); err != nil {
					return
				}
				body := make([]byte, int(binary.BigEndian.Uint32(header[1:]))-4)
				if _, err := io.ReadFull(up, body); err != nil {
					return
				}
				if header[0] == 'C' && bytes.Equal(body, []byte("COMMIT\x00")) {
					once.Do(func() { close(committed) })
					<-release
				}
				if _, err := b.Write(header[:]); err != nil {
					return
				}
				if _, err := b.Write(body); err != nil {
					return
				}
			}
		}()
		return a, nil
	}
	useConfig(s, cfg)
	done := make(chan error, 1)
	go func() { _, err := s.AddWith("synthetic uncertain commit", "fixture", "", Meta{}); done <- err }()
	select {
	case <-committed:
	case err := <-done:
		close(release)
		wg.Wait()
		t.Fatalf("commit response not held: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("commit setup deadline")
	}
	var n int
	if err := db.QueryRow(context.Background(), "select count(*) from memcue.facts").Scan(&n); err != nil || n != 1 {
		close(release)
		<-done
		wg.Wait()
		t.Fatalf("server did not commit before withheld response: %d %v", n, err)
	}
	select {
	case err := <-done:
		close(release)
		wg.Wait()
		if !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("lost commit reply must be explicitly ambiguous: %v", err)
		}
	case <-time.After(11 * time.Second):
		close(release)
		<-done
		wg.Wait()
		t.Fatal("uncertain mutation exceeded bounded deadline")
	}
	if err := db.QueryRow(context.Background(), "select count(*) from memcue.facts").Scan(&n); err != nil || n != 1 {
		t.Fatalf("mutation replayed or history lost: %d %v", n, err)
	}
}
