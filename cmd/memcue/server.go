package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	return serveBase(ctx, st, ln, ln.Who, logger)
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
