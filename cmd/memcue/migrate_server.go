package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/juev/memcue/internal/replica"
	"github.com/juev/memcue/internal/server"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tailnet"
)

// serverDial opens a connection to the server of MEMCUE_SERVER through the helper of this machine; tests replace it.
var serverDial = func(ctx context.Context) (net.Conn, error) {
	cfg, err := helperConfig()
	if err != nil {
		return nil, err
	}
	ips, err := tailnet.Lookup(ctx, cfg, cfg.Host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s is not a tailnet peer", cfg.Host)
	}
	return tailnet.Dial(ctx, cfg, "tcp", net.JoinHostPort(ips[0], cfg.Port))
}

// runMigrateServer loads the local base of MEMCUE_FACTS into the empty server of MEMCUE_SERVER (#714, R12): memcue
// migrate -server [-apply]. Without -apply it reports what it would send and sends nothing. Each fact goes with the
// text and labels of its latest version and the creation time of its first; the server stamps every register with
// that time. A server that holds any fact refuses, and nothing is stored.
func (inv *invocation) runMigrateServer(apply bool, out io.Writer) error {
	name := serverName()
	if name == "" {
		return errors.New("migrate -server: set MEMCUE_SERVER to the server to load")
	}
	source := factsBase().path
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("migrate -server: no local fact base at %s", source)
	}
	s, err := inv.openLocalBase()
	if err != nil {
		return err
	}
	rows, err := s.Rows()
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	facts := latestFacts(rows)
	if len(facts) == 0 {
		return fmt.Errorf("migrate -server: the local base at %s holds no facts", source)
	}
	active := 0
	for _, f := range facts {
		if f.Status == store.Active {
			active++
		}
	}
	if _, err := fmt.Fprintf(out, "source %s: %d facts (%d active, %d retired) from %d versions\n", source, len(facts),
		active, len(facts)-active, len(rows)); err != nil {
		return err
	}
	if !apply {
		_, err := fmt.Fprintf(out, "target %s: run with -apply to load them into the empty server\n", name)
		return err
	}
	n, err := replica.NewHTTP(serverDial).Import(inv.ctx, facts)
	if err != nil {
		return fmt.Errorf("migrate -server: %w", err)
	}
	_, err = fmt.Fprintf(out, "target %s: loaded %d facts; replicas fetch them from the server\n", name, n)
	return err
}

// latestFacts is every fact of a history of versions (Rows order: by id, then version) as the wire carries it: the
// latest version, created when its first version was.
func latestFacts(rows []store.Fact) []server.Row {
	var out []server.Row
	for i, f := range rows {
		if i > 0 && rows[i-1].ID == f.ID {
			last := &out[len(out)-1]
			created := last.Created
			*last = server.WireRow(store.Row{Fact: f})
			last.Created = created
			continue
		}
		out = append(out, server.WireRow(store.Row{Fact: f}))
	}
	return out
}
