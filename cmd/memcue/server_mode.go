package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/replica"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tailnet"
)

// serverName is MEMCUE_SERVER. In server mode (#714) every command reads and writes this machine's replica of that
// server and never waits for the network; the tailnet helper syncs the replica.
func serverName() string { return strings.TrimSpace(os.Getenv("MEMCUE_SERVER")) }

// openReplica opens this machine's replica of the server: one per server, whatever MEMCUE_FACTS and MEMCUE_STATE_DIR
// say (R16).
func openReplica() (*store.Store, error) {
	path, err := replica.Path(serverName())
	if err != nil {
		return nil, err
	}
	return store.OpenReplica(path)
}

// serverCommandAllowed refuses in server mode the commands that work on a local base or on git: the replica belongs to
// the server, and dream runs there (R11).
func serverCommandAllowed(args []string) error {
	if serverName() == "" || len(args) == 0 {
		return nil
	}
	if os.Getenv("MEMCUE_POSTGRES_DSN") != "" {
		return errors.New("set MEMCUE_SERVER or MEMCUE_POSTGRES_DSN, not both")
	}
	switch args[0] {
	case "dream":
		return errors.New("dream runs on the server in server mode (MEMCUE_SERVER); this machine does not dream")
	case "install", "merge", "dump":
		return fmt.Errorf("%s works on a base in git and is unavailable in server mode (MEMCUE_SERVER)", args[0])
	case "migrate":
		if !migratesToServer(args) {
			return errors.New("migrate works on a local base and is unavailable in server mode (MEMCUE_SERVER); " +
				"migrate -server loads a local base into the server")
		}
	}
	return nil
}

// migratesToServer reports whether the command line is migrate -server, as migratesToPostgres does for -postgres.
func migratesToServer(args []string) bool {
	if len(args) < 1 || args[0] != "migrate" {
		return false
	}
	for _, a := range args[1:] {
		switch a {
		case "-server", "--server", "-server=true", "--server=true":
			return true
		case "--":
			return false
		}
	}
	return false
}

// helperConfig is the helper's configuration for the server of MEMCUE_SERVER, as connect builds it.
func helperConfig() (tailnet.Config, error) {
	name := serverName()
	if err := replica.ValidServer(name); err != nil {
		return tailnet.Config{}, err
	}
	cfg, err := tailnet.Settings(name, replica.Port)
	if err != nil {
		return tailnet.Config{}, err
	}
	cfg.Server, cfg.Version = true, version
	return cfg, nil
}

// helperWait bounds what a session start spends on the local helper: its socket answers in milliseconds, and a
// session must not wait for a helper that hangs (R1).
const helperWait = 500 * time.Millisecond

// startHelper starts the helper detached; tests replace it.
var startHelper = startHelperDetached

// startHelperDetached runs memcue connect serve -takeover in its own session and returns without waiting for it (R10).
// The new helper waits for one that is exiting to let go of the tailnet identity: a helper asked to exit closes its
// socket at once but holds the identity until its node is down, and a plain start in that window would find it owned
// and exit, leaving the machine without a helper.
func startHelperDetached() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = null.Close() }()
	cmd := exec.Command(exe, "connect", "serve", "-takeover")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, connectNotifyEnv+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ensureHelper makes sure the helper of this machine runs a binary at least as new as this one, starting or replacing
// it without waiting (R10). A helper of a newer release stays: two agents whose plugins ship different releases would
// otherwise replace each other's helper on every session start (#718). It returns the state of a helper that stays
// running, and ok false when there was none or it was replaced.
func (inv *invocation) ensureHelper(cfg tailnet.Config) (state tailnet.State, ok bool) {
	ctx, cancel := context.WithTimeout(inv.ctx, helperWait)
	defer cancel()
	state, err := tailnet.Admin(ctx, cfg, "status")
	switch {
	case err != nil:
		if tailnet.ValidateExistingProfile(cfg) == nil {
			_ = startHelper()
		}
		return state, false
	case helperOlder(state.Version, version):
		_, _ = tailnet.Admin(ctx, tailnet.Config{Dir: cfg.Dir}, "off")
		_ = startHelper()
		return state, false
	}
	return state, true
}

// helperOlder says whether a helper of version helper must give way to a session of version session. Two releases
// compare by number. A source build cannot be ordered against a release, so there the session's binary wins, as it
// did before #718: a developer who runs a build of the tree wants its helper.
func helperOlder(helper, session string) bool {
	if helper == session {
		return false
	}
	h, ok1 := parseVersion(helper)
	s, ok2 := parseVersion(session)
	if !ok1 || !ok2 {
		return true
	}
	for i := range h {
		if h[i] != s[i] {
			return h[i] < s[i]
		}
	}
	return false
}

// wakeHelper starts the helper after a write in server mode, so that a helper which died during a session does not
// leave the write in the outbox until the next session start (#718). It never waits for the helper.
func (inv *invocation) wakeHelper() {
	if serverName() == "" {
		return
	}
	if cfg, err := helperConfig(); err == nil {
		inv.ensureHelper(cfg)
	}
}

// writesFacts says whether a command writes facts into the base, so a helper must carry them to the server.
func writesFacts(cmd string) bool {
	switch cmd {
	case "add", "correct", "retire", "reopen", "import":
		return true
	}
	return false
}

// serverHints makes sure the helper of this machine runs (ensureHelper) and returns the lines of R15: a helper that
// waits for a login, a replica that never synced, changes the server refused. A hint that cannot be worked out says
// nothing: the session must start either way.
func (inv *invocation) serverHints() string {
	if serverName() == "" {
		return ""
	}
	cfg, err := helperConfig()
	if err != nil {
		return "memcue: " + err.Error()
	}
	var lines []string
	state, running := inv.ensureHelper(cfg)
	if running && (state.State == "NeedsLogin" || state.State == "NeedsMachineAuth" || state.State == "Stopped") {
		lines = append(lines, fmt.Sprintf("memcue: the tailnet helper of this machine waits for a login (%s), so the "+
			"replica of %s does not sync; tell the user to run memcue connect up.", state.State, cfg.Host))
	}
	if r, err := openReplica(); err == nil {
		st, err := r.State()
		_ = r.Close()
		if err == nil && st.LastSync.IsZero() {
			lines = append(lines, fmt.Sprintf("memcue: this machine's replica of %s has never synced, so facts "+
				"written on other machines are not here yet.", cfg.Host))
		}
	}
	if running {
		if l := rejectedHint(state.Sync); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// rejectedHint is the line for outbox changes the server refused (R8, R15).
func rejectedHint(sync json.RawMessage) string {
	var st replica.Status
	if len(sync) == 0 || json.Unmarshal(sync, &st) != nil || len(st.Rejected) == 0 {
		return ""
	}
	reasons := map[string]int{}
	for _, r := range st.Rejected {
		reasons[r.Reason]++
	}
	var parts []string
	for reason, n := range reasons {
		parts = append(parts, fmt.Sprintf("%d %s", n, reason))
	}
	sort.Strings(parts)
	return fmt.Sprintf("memcue: the server refused %d changes of this replica (%s); they wait in its outbox, and "+
		"memcue connect status lists them.", len(st.Rejected), strings.Join(parts, ", "))
}
