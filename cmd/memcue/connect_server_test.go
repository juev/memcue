package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectInServerModeNeedsNoPostgres(t *testing.T) {
	t.Setenv("MEMCUE_TAILSCALE_STATE_DIR", filepath.Join(t.TempDir(), "tailnet"))
	t.Setenv("MEMCUE_TAILSCALE_MODE", "")
	t.Setenv("MEMCUE_POSTGRES_DSN", "")
	for _, c := range []struct{ server, dsn, want string }{
		{"memcue-server", "postgres://u@db/memcue", "not both"},
		{"Not A Host", "", "not a tailnet node name"},
		// A valid server passes the configuration and reaches the helper's identity, which does not exist here.
		{"memcue-server", "", "tailnet state directory"},
	} {
		t.Setenv("MEMCUE_SERVER", c.server)
		t.Setenv("MEMCUE_POSTGRES_DSN", c.dsn)
		err := runConnect([]string{"login"}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("MEMCUE_SERVER=%q MEMCUE_POSTGRES_DSN=%q: %v, want %q", c.server, c.dsn, err, c.want)
		}
	}
}
