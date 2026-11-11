// Package replica keeps a machine's replica of a memcue server in step with it (#714, docs/specs/sync-server.md).
// The tailnet helper runs it: it long-polls the server's changes into the replica and sends the replica's outbox.
// Every other process of the machine only reads and writes the replica.
package replica

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/juev/memcue/internal/server"
	"github.com/juev/memcue/internal/store"
)

// Port is the tailnet port of memcue server.
const Port = "80"

var serverName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// ValidServer checks the value of MEMCUE_SERVER: the tailnet name of the server node, short or full.
func ValidServer(name string) error {
	if len(name) > 253 || !serverName.MatchString(name) {
		return fmt.Errorf("MEMCUE_SERVER %q is not a tailnet node name", name)
	}
	return nil
}

// Path is the replica of server on this machine, $XDG_STATE_HOME/memcue/server-<name>/replica.db: one per server,
// whatever base or state directory an agent names (R16).
func Path(name string) (string, error) {
	if err := ValidServer(name); err != nil {
		return "", err
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "memcue", "server-"+name, "replica.db"), nil
}

// Client is the server as the replica sees it.
type Client interface {
	// Pull returns the rows after since, waiting up to wait for one.
	Pull(ctx context.Context, since int64, wait time.Duration, limit int) (server.Changes, error)
	// Push sends changes and returns the server's answer to each.
	Push(ctx context.Context, changes []store.Change) ([]store.Applied, error)
}

// HTTP is the Client of a server reached through dial, the helper's tailnet dialer.
type HTTP struct {
	hc *http.Client
}

// The host name the requests carry; dial decides where they go.
const httpBase = "http://memcue-server"

// NewHTTP returns a Client whose connections dial opens.
func NewHTTP(dial func(context.Context) (net.Conn, error)) *HTTP {
	tr := &http.Transport{
		DialContext:     func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		MaxIdleConns:    2,
		IdleConnTimeout: 90 * time.Second,
	}
	return &HTTP{hc: &http.Client{Transport: tr}}
}

// pullSlack is how long a pull waits for its answer beyond the wait of the long poll. A connection whose peer went
// away without closing it (a server restarted, a laptop that slept) answers nothing, so this bounds how long the
// replica stays deaf to the server: wait plus pullSlack.
var pullSlack = 5 * time.Second

// Pull implements Client.
func (h *HTTP) Pull(ctx context.Context, since int64, wait time.Duration, limit int) (server.Changes, error) {
	ctx, cancel := context.WithTimeout(ctx, wait+pullSlack)
	defer cancel()
	q := url.Values{"since": {strconv.FormatInt(since, 10)}, "wait": {wait.String()}, "limit": {strconv.Itoa(limit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpBase+"/v1/changes?"+q.Encode(), nil)
	if err != nil {
		return server.Changes{}, err
	}
	var c server.Changes
	return c, h.do(req, &c)
}

// Push implements Client.
func (h *HTTP) Push(ctx context.Context, changes []store.Change) ([]store.Applied, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, err := json.Marshal(server.Push{Changes: changes})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpBase+"/v1/changes", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var p server.Pushed
	if err := h.do(req, &p); err != nil {
		return nil, err
	}
	if len(p.Results) != len(changes) {
		return nil, fmt.Errorf("server answered %d of %d changes", len(p.Results), len(changes))
	}
	return p.Results, nil
}

func (h *HTTP) do(req *http.Request, out any) error {
	resp, err := h.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		return fmt.Errorf("server answered %d: %s", resp.StatusCode, e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ErrNotRunning: the helper has not started the sync, or could not open the replica.
var ErrNotRunning = errors.New("replica sync is not running")

// Import loads facts into the empty server (R12). It has no deadline of its own: a whole base may take a while.
func (h *HTTP) Import(ctx context.Context, facts []server.Row) (int, error) {
	b, err := json.Marshal(server.Import{Facts: facts})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpBase+"/v1/import", bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	var got server.Imported
	if err := h.do(req, &got); err != nil {
		return 0, err
	}
	return got.Facts, nil
}
