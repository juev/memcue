package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/tailnet"
)

const connectNotifyEnv = "_MEMCUE_CONNECT_NOTIFY"

func connectUp(ctx context.Context, cfg tailnet.Config, bootstrap bool, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	if err := tailnet.ValidateExistingProfile(cfg); err != nil {
		return err
	}
	state, err := tailnet.Admin(ctx, cfg, "status")
	if err != nil {
		if err = startConnectHelper(ctx, bootstrap); err != nil {
			return err
		}
		state, err = tailnet.Admin(ctx, cfg, "status")
		if err != nil {
			return errors.New("helper started but status is unavailable; retry memcue connect up or stop with memcue connect off")
		}
	}
	if state, err = settled(ctx, cfg, state); err != nil {
		if ctx.Err() != nil {
			return errors.New("tailnet helper startup timed out or was cancelled; retry memcue connect up")
		}
		return errors.New("helper started but status is unavailable; retry memcue connect up or stop with memcue connect off")
	}
	if !starting(state) && state.State != "Running" && state.State != "NeedsMachineAuth" {
		state, err = tailnet.Admin(ctx, cfg, "login")
		if err != nil {
			return errors.New("helper is running but login is unavailable; check the control server and retry memcue connect up, or stop with memcue connect off")
		}
		if state.AuthURL != "" {
			_, err = fmt.Fprintln(out, "Open this URL to authorize the Memcue node:\n"+state.AuthURL+"\nThe helper stays in the background. After approval, run memcue connect up again to check readiness.")
			return err
		}
	}
	if starting(state) {
		_, err = fmt.Fprintln(out, "Tailnet is still starting ("+state.State+"). Check that the control server is reachable, then run memcue connect up again. The helper stays in the background.")
		return err
	}
	if state.State != "Running" {
		_, err = fmt.Fprintln(out, "Tailnet authorization pending ("+state.State+"). Complete device approval, then run memcue connect up again. The helper stays in the background.")
		return err
	}
	_, err = fmt.Fprintln(out, "Tailnet ready. The helper is running in the background; stop it with memcue connect off.")
	return err
}

// starting is a backend that has not decided yet whether its identity is approved.
func starting(s tailnet.State) bool {
	return s.State == "" || s.State == "NoState" || s.State == "Starting"
}

// settled waits while the backend is starting. A helper restoring an approved identity passes NoState and Starting
// before Running; a login there regenerates its node key and voids the approval (#697). The wait leaves a quarter of
// the deadline for the answer; a backend still starting then is reported as pending, without a login.
func settled(ctx context.Context, cfg tailnet.Config, state tailnet.State) (tailnet.State, error) {
	wait := ctx
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		wait, cancel = context.WithDeadline(ctx, deadline.Add(-time.Until(deadline)/4))
		defer cancel()
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for starting(state) {
		select {
		case <-wait.Done():
			return state, ctx.Err()
		case <-ticker.C:
		}
		next, err := tailnet.Admin(wait, cfg, "status")
		if err != nil {
			if wait.Err() != nil {
				return state, ctx.Err()
			}
			return state, err
		}
		state = next
	}
	return state, nil
}

// startConnectHelper never inherits terminal/consumer pipes. Only the private
// startup pipe is retained, and the child closes it as soon as IPC is ready.
func startConnectHelper(ctx context.Context, bootstrap bool) error {
	unavailable := errors.New("could not start the tailnet helper; check private state/configuration with memcue connect serve")
	exe, err := os.Executable()
	if err != nil {
		return unavailable
	}
	read, write, err := os.Pipe()
	if err != nil {
		return unavailable
	}
	defer func() { _ = read.Close(); _ = write.Close() }()
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return unavailable
	}
	defer func() { _ = null.Close() }()
	args := []string{"connect", "serve"}
	if bootstrap {
		args = append(args, "-bootstrap")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	cmd.ExtraFiles = []*os.File{write}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, connectNotifyEnv+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, connectNotifyEnv+"=1")
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return unavailable
	}
	_ = write.Close()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	success := false
	defer func() {
		if !success {
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		_ = read.SetReadDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = read.Close() })
	defer stop()
	var ack [1]byte
	if _, err := io.ReadFull(read, ack[:]); err != nil || ack[0] != 1 {
		if ctx.Err() != nil {
			return errors.New("tailnet helper startup timed out or was cancelled; retry memcue connect up")
		}
		return unavailable
	}
	success = true
	return nil
}

// The notification descriptor is an inherited pipe, never a path, credential,
// or caller terminal. A failed startup receipt cancels only this new owner.
func runConnectOwner(ctx context.Context, cfg tailnet.Config, bootstrap bool) error {
	if os.Getenv(connectNotifyEnv) != "1" {
		return tailnet.Run(ctx, cfg, bootstrap, make(chan struct{}))
	}
	notify := os.NewFile(3, "tailnet startup")
	defer func() { _ = notify.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready := make(chan struct{})
	finished := make(chan struct{})
	notified := make(chan struct{})
	go func() {
		defer close(notified)
		select {
		case <-ready:
			_ = notify.SetWriteDeadline(time.Now().Add(cfg.Timeout))
			if _, err := notify.Write([]byte{1}); err != nil {
				cancel()
			}
		case <-finished:
		}
		_ = notify.Close()
	}()
	err := tailnet.Run(ctx, cfg, bootstrap, ready)
	close(finished)
	<-notified
	return err
}
