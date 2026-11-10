package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/server"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tailnet"
)

// serverPort is the tailnet port of memcue server. The tailnet encrypts the traffic, so it is plain HTTP.
const serverPort = "80"

// runServer serves the base of MEMCUE_FACTS to replicas on the tailnet until a signal stops it (#714). It runs in the
// foreground, under systemd or Docker, and logs to stderr.
func runServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	bootstrap := fs.Bool("bootstrap", false, "log in the first time with the auth key of MEMCUE_TAILSCALE_AUTH_KEY_FILE")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: memcue server [-bootstrap]")
	}
	// The server's own commands (dream) open its base through MEMCUE_FACTS: a backend of a client would take them
	// elsewhere.
	if serverName() != "" || os.Getenv("MEMCUE_POSTGRES_DSN") != "" {
		return errors.New("memcue server serves MEMCUE_FACTS: unset MEMCUE_SERVER and MEMCUE_POSTGRES_DSN in its environment")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := log.New(os.Stderr, "memcue server: ", log.LstdFlags)
	path := factsPath()
	st, err := store.OpenServer(path)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	cfg, err := tailnet.Settings("", serverPort)
	if err != nil {
		return err
	}
	ln, err := tailnet.Listen(cfg, *bootstrap, logger.Printf)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	logger.Printf("serving %s as tailnet node %s, port %s", path, cfg.Hostname, serverPort)
	dreams := make(chan struct{})
	if dreamBackground() {
		inv := newInvocation()
		defer inv.close()
		dir, err := stateDir()
		if err != nil {
			return err
		}
		go func() {
			defer close(dreams)
			runDreams(ctx, logger, func() (int, error) {
				n, _, err := inv.dreamDue(dir, dreamEveryFromEnv(dreamEvery))
				return n, err
			}, func(ctx context.Context) error { return dreamProcess(ctx, dir) })
		}()
	} else {
		close(dreams)
		logger.Printf("dream: off; it needs MEMCUE_API_URL or claude on PATH, and MEMCUE_DREAM_BACKGROUND not 0")
	}
	err = serveBase(ctx, st, ln, ln.Who, logger)
	stop()
	<-dreams
	return err
}

// dreamCheck is how often memcue server looks whether a dream is due.
var dreamCheck = 10 * time.Minute

// runDreams runs a dream of the server's base whenever one is due, until ctx ends (R11). The server is the only
// machine that dreams in server mode; the dream's writes reach the replicas like any other change.
func runDreams(ctx context.Context, logger *log.Logger, due func() (int, error), dream func(context.Context) error) {
	for {
		switch n, err := due(); {
		case err != nil:
			logger.Printf("dream: %v", err)
		case n > 0:
			logger.Printf("dream: %d facts wait, dreaming", n)
			if err := dream(ctx); err != nil && ctx.Err() == nil {
				logger.Printf("dream: %v", err)
			}
		}
		t := time.NewTimer(dreamCheck)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// dreamProcess runs one dream of the base as its own process, as a session start does on a local base, and waits for
// it; dream.out in the state directory keeps what it said.
func dreamProcess(ctx context.Context, dir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(dir, "dream.out"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	cmd := exec.CommandContext(ctx, exe, "dream", "-run", "-background")
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// serveBase serves st on ln until ctx ends. A long poll in progress ends with ctx and answers nothing.
func serveBase(ctx context.Context, st *store.Store, ln net.Listener, who func(*http.Request) string, logger *log.Logger) error {
	hs := &http.Server{
		Handler:           server.New(st, logger, who),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          logger,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- hs.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := hs.Shutdown(shutdown)
	if served := <-done; !errors.Is(served, http.ErrServerClosed) && err == nil {
		err = served
	}
	return err
}
