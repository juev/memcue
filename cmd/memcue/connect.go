package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/replica"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tailnet"
)

func runConnect(args []string, out io.Writer) error {
	return runConnectContext(context.Background(), args, out)
}

func runConnectContext(parent context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: memcue connect up [-bootstrap]|status|serve [-bootstrap]|login|off")
	}
	mode, err := tailnet.Mode()
	if err != nil {
		return err
	}
	dir, err := tailnet.StateDir()
	if err != nil {
		return err
	}
	cfg := tailnet.Config{Dir: dir}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if args[0] == "status" && len(args) == 1 {
		state := tailnet.State{State: "Stopped"}
		if _, err := os.Lstat(dir); err == nil {
			got, requestErr := tailnet.Admin(ctx, cfg, "status")
			if requestErr == nil {
				state = got
			} else {
				state.State = "Unavailable"
			}
		} else if !os.IsNotExist(err) {
			return errors.New("cannot inspect private tailnet state directory")
		}
		return json.NewEncoder(out).Encode(struct {
			Mode string `json:"mode"`
			tailnet.State
		}{mode, state})
	}
	if args[0] == "off" && len(args) == 1 {
		state, err := tailnet.Admin(ctx, cfg, "off")
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(state)
	}
	// A memcue server is reached only through the helper, so MEMCUE_SERVER needs no MEMCUE_TAILSCALE_MODE (#714).
	serverName, dsn := os.Getenv("MEMCUE_SERVER"), os.Getenv("MEMCUE_POSTGRES_DSN")
	var replicaPath string
	switch {
	case serverName != "" && dsn != "":
		return errors.New("set MEMCUE_SERVER or MEMCUE_POSTGRES_DSN, not both")
	case serverName != "":
		if replicaPath, err = replica.Path(serverName); err != nil {
			return err
		}
		if cfg, err = tailnet.Settings(serverName, replica.Port); err != nil {
			return err
		}
		cfg.Server = true
	default:
		if mode != "embedded" {
			return errors.New("enable embedded transport explicitly: export MEMCUE_TAILSCALE_MODE=embedded")
		}
		if dsn == "" {
			return errors.New("connect requires an explicit MEMCUE_POSTGRES_DSN backend or MEMCUE_SERVER")
		}
		pg, err := store.PostgresConfig(dsn)
		if err != nil {
			return err
		}
		if cfg, err = tailnet.Settings(pg.Host, strconv.Itoa(int(pg.Port))); err != nil {
			return err
		}
	}
	cfg.Version = version
	switch args[0] {
	case "up":
		fs := flag.NewFlagSet("connect up", flag.ContinueOnError)
		bootstrap := fs.Bool("bootstrap", false, "explicitly bootstrap from MEMCUE_TAILSCALE_AUTH_KEY_FILE")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("connect up accepts only -bootstrap")
		}
		return connectUp(ctx, cfg, *bootstrap, out)
	case "serve":
		fs := flag.NewFlagSet("connect serve", flag.ContinueOnError)
		bootstrap := fs.Bool("bootstrap", false, "explicitly bootstrap from MEMCUE_TAILSCALE_AUTH_KEY_FILE")
		takeover := fs.Bool("takeover", false, "wait for a helper that was asked to exit to release the identity")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("connect serve accepts only -bootstrap and -takeover")
		}
		if replicaPath != "" {
			cfg.Worker = replica.NewWorker(replicaPath)
		}
		if !*takeover {
			return runConnectOwner(ctx, cfg, *bootstrap)
		}
		// A session start replaces a helper of another version (R10): the old one exits on its own time.
		deadline := time.Now().Add(10 * time.Second)
		for {
			err := runConnectOwner(ctx, cfg, *bootstrap)
			if !errors.Is(err, tailnet.ErrOwned) || time.Now().After(deadline) || ctx.Err() != nil {
				return err
			}
			time.Sleep(200 * time.Millisecond)
		}
	case "login":
		if len(args) != 1 {
			return errors.New("usage: memcue connect login")
		}
		state, err := tailnet.Admin(ctx, cfg, "login")
		if err != nil {
			return err
		}
		if state.AuthURL != "" {
			_, err = fmt.Fprintln(out, "Open this URL to authorize the Memcue node:\n"+state.AuthURL)
			return err
		}
		_, err = fmt.Fprintln(out, "Node authorization state: "+state.State)
		return err
	default:
		return errors.New("usage: memcue connect up [-bootstrap]|status|serve [-bootstrap]|login|off")
	}
}
