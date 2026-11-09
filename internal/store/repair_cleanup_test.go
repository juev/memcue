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

// A read whose statement the server never answers ends at the operation deadline; a read is one statement without a
// transaction (#700), so the proxy stalls the statement itself.
func TestRepairReadRollbackRespectsDeadline(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	cfg := s.remote.config.Copy()
	direct, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = direct.Close(context.Background()) }()
	if _, err = direct.Exec(context.Background(), "TRUNCATE memcue.facts"); err != nil {
		t.Fatal(err)
	}
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		client, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		up, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer func() { _ = up.Close() }()
		copyDone := make(chan struct{})
		go func() { _, _ = io.Copy(client, up); close(copyDone) }()
		defer func() { _ = up.Close(); _ = client.Close(); <-copyDone }()
		var size [4]byte
		if _, err = io.ReadFull(client, size[:]); err != nil {
			return
		}
		body := make([]byte, int(binary.BigEndian.Uint32(size[:]))-4)
		if _, err = io.ReadFull(client, body); err != nil {
			return
		}
		if _, err = up.Write(size[:]); err != nil {
			return
		}
		if _, err = up.Write(body); err != nil {
			return
		}
		for {
			var header [5]byte
			if _, err = io.ReadFull(client, header[:]); err != nil {
				return
			}
			body = make([]byte, int(binary.BigEndian.Uint32(header[1:]))-4)
			if _, err = io.ReadFull(client, body); err != nil {
				return
			}
			if header[0] == 'P' || header[0] == 'Q' {
				once.Do(func() { close(reached) })
				<-release
			}
			if _, err = up.Write(header[:]); err != nil {
				return
			}
			if _, err = up.Write(body); err != nil {
				return
			}
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	cfg.Host = "127.0.0.1"
	cfg.Port = uint16(addr.Port)
	useConfig(s, cfg)
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := s.Get("synthetic-missing"); done <- err }()
	select {
	case <-reached:
	case err := <-done:
		close(release)
		wg.Wait()
		t.Fatalf("rollback not reached: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("proxy setup timed out")
	}
	timer := time.NewTimer(time.Until(start.Add(10200 * time.Millisecond)))
	<-timer.C
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
		case <-time.After(2 * time.Second):
			t.Fatal("proxy release failed")
		}
	}
	wg.Wait()
	if !completed {
		t.Fatal("native Get cleanup escaped total operation deadline")
	}
}

func TestRepairWriteRollbackRespectsDeadline(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	cfg := s.remote.config.Copy()
	direct, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = direct.Close(context.Background()) }()
	if _, err = direct.Exec(context.Background(), "TRUNCATE memcue.facts"); err != nil {
		t.Fatal(err)
	}
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		client, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		up, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer func() { _ = up.Close() }()
		copyDone := make(chan struct{})
		go func() { _, _ = io.Copy(client, up); close(copyDone) }()
		defer func() { _ = up.Close(); _ = client.Close(); <-copyDone }()
		var size [4]byte
		if _, err = io.ReadFull(client, size[:]); err != nil {
			return
		}
		body := make([]byte, int(binary.BigEndian.Uint32(size[:]))-4)
		if _, err = io.ReadFull(client, body); err != nil {
			return
		}
		if _, err = up.Write(size[:]); err != nil {
			return
		}
		if _, err = up.Write(body); err != nil {
			return
		}
		for {
			var header [5]byte
			if _, err = io.ReadFull(client, header[:]); err != nil {
				return
			}
			body = make([]byte, int(binary.BigEndian.Uint32(header[1:]))-4)
			if _, err = io.ReadFull(client, body); err != nil {
				return
			}
			if header[0] == 'Q' && bytes.Equal(body, []byte("rollback\x00")) {
				once.Do(func() { close(reached) })
				<-release
			}
			if _, err = up.Write(header[:]); err != nil {
				return
			}
			if _, err = up.Write(body); err != nil {
				return
			}
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	cfg.Host = "127.0.0.1"
	cfg.Port = uint16(addr.Port)
	useConfig(s, cfg)
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := s.AddWith("", "fixture", "", Meta{}); done <- err }()
	select {
	case <-reached:
	case err := <-done:
		close(release)
		wg.Wait()
		t.Fatalf("rollback not reached: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("proxy setup timed out")
	}
	timer := time.NewTimer(time.Until(start.Add(10200 * time.Millisecond)))
	<-timer.C
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
		case <-time.After(2 * time.Second):
			t.Fatal("proxy release failed")
		}
	}
	wg.Wait()
	if !completed {
		t.Fatal("native Get cleanup escaped total operation deadline")
	}
}

func TestRepairCleanupDoesNotDialAfterBudget(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	cfg := s.remote.config.Copy()
	direct, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = direct.Close(context.Background()) }()
	if _, err = direct.Exec(context.Background(), "TRUNCATE memcue.facts"); err != nil {
		t.Fatal(err)
	}
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		client, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		up, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer func() { _ = up.Close() }()
		copyDone := make(chan struct{})
		go func() { _, _ = io.Copy(client, up); close(copyDone) }()
		defer func() { _ = up.Close(); _ = client.Close(); <-copyDone }()
		var size [4]byte
		if _, err = io.ReadFull(client, size[:]); err != nil {
			return
		}
		body := make([]byte, int(binary.BigEndian.Uint32(size[:]))-4)
		if _, err = io.ReadFull(client, body); err != nil {
			return
		}
		if _, err = up.Write(size[:]); err != nil {
			return
		}
		if _, err = up.Write(body); err != nil {
			return
		}
		for {
			var header [5]byte
			if _, err = io.ReadFull(client, header[:]); err != nil {
				return
			}
			body = make([]byte, int(binary.BigEndian.Uint32(header[1:]))-4)
			if _, err = io.ReadFull(client, body); err != nil {
				return
			}
			if header[0] == 'P' || header[0] == 'Q' { // a read is one statement (#700)
				once.Do(func() { close(reached) })
				<-release
			}
			if _, err = up.Write(header[:]); err != nil {
				return
			}
			if _, err = up.Write(body); err != nil {
				return
			}
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	cfg.Host = "127.0.0.1"
	cfg.Port = uint16(addr.Port)
	late := make(chan struct{}, 4)
	var began time.Time
	originalDial := cfg.DialFunc
	cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		if !began.IsZero() && time.Since(began) > 10*time.Second {
			late <- struct{}{}
		}
		return originalDial(ctx, network, address)
	}
	useConfig(s, cfg)
	done := make(chan error, 1)
	start := time.Now()
	began = start
	go func() { _, err := s.Get("synthetic-missing"); done <- err }()
	select {
	case <-reached:
	case err := <-done:
		close(release)
		wg.Wait()
		t.Fatalf("rollback not reached: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("proxy setup timed out")
	}
	timer := time.NewTimer(time.Until(start.Add(10200 * time.Millisecond)))
	<-timer.C
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
		case <-time.After(2 * time.Second):
			t.Fatal("proxy release failed")
		}
	}
	wg.Wait()
	if !completed {
		t.Fatal("native Get cleanup escaped total operation deadline")
	}

	_ = ln.Close()
	if len(late) > 0 {
		t.Fatal("pgx cleanup opened a new network connection after the operation deadline")
	}
}

func TestRepairRollbackTimeoutCannotPublishReadSuccess(t *testing.T) {
	dsn := os.Getenv("MEMCUE_REPAIR_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	cfg := s.remote.config.Copy()
	direct, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = direct.Close(context.Background()) }()
	if _, err = direct.Exec(context.Background(), "TRUNCATE memcue.facts"); err != nil {
		t.Fatal(err)
	}
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		client, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		up, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer func() { _ = up.Close() }()
		copyDone := make(chan struct{})
		go func() { _, _ = io.Copy(client, up); close(copyDone) }()
		defer func() { _ = up.Close(); _ = client.Close(); <-copyDone }()
		var size [4]byte
		if _, err = io.ReadFull(client, size[:]); err != nil {
			return
		}
		body := make([]byte, int(binary.BigEndian.Uint32(size[:]))-4)
		if _, err = io.ReadFull(client, body); err != nil {
			return
		}
		if _, err = up.Write(size[:]); err != nil {
			return
		}
		if _, err = up.Write(body); err != nil {
			return
		}
		for {
			var header [5]byte
			if _, err = io.ReadFull(client, header[:]); err != nil {
				return
			}
			body = make([]byte, int(binary.BigEndian.Uint32(header[1:]))-4)
			if _, err = io.ReadFull(client, body); err != nil {
				return
			}
			if header[0] == 'P' || header[0] == 'Q' { // a read is one statement (#700)
				once.Do(func() { close(reached) })
				<-release
			}
			if _, err = up.Write(header[:]); err != nil {
				return
			}
			if _, err = up.Write(body); err != nil {
				return
			}
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	cfg.Host = "127.0.0.1"
	cfg.Port = uint16(addr.Port)
	useConfig(s, cfg)
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := s.Rows(); done <- err }()
	select {
	case <-reached:
	case err := <-done:
		close(release)
		wg.Wait()
		t.Fatalf("rollback not reached: %v", err)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("proxy setup timed out")
	}
	timer := time.NewTimer(time.Until(start.Add(10200 * time.Millisecond)))
	<-timer.C
	completed := false
	var operationErr error
	select {
	case operationErr = <-done:
		completed = true
	default:
	}
	close(release)
	if !completed {
		select {
		case operationErr = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("proxy release failed")
		}
	}
	wg.Wait()
	if !completed {
		t.Fatal("native Get cleanup escaped total operation deadline")
	}

	if !errors.Is(operationErr, context.DeadlineExceeded) {
		t.Fatalf("cleanup timeout published read success: %v", operationErr)
	}
}
