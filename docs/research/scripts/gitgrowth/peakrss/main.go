//go:build research

// Command peakrss runs a command and writes its wall time and peak resident set size to a file (#530).
//
// A process forked from Python starts with Python's resident set, and ru_maxrss keeps that figure across exec, so a
// small git run measured from Python reads as about 60 MB. This wrapper is small, and it starts the command with
// os/exec (clone with CLONE_VFORK), so the floor is its own few megabytes. ru_maxrss from wait4 covers the command
// and the children it waited for: pack-objects under gc, index-pack under fetch.
//
//	peakrss OUT.json git -C repo gc
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: peakrss OUT.json COMMAND [ARGS...]")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		fmt.Fprintln(os.Stderr, "peakrss:", err)
		os.Exit(127)
	}
	var maxrss int64
	if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
		maxrss = ru.Maxrss // KiB on Linux
	}
	b, _ := json.Marshal(map[string]any{"seconds": elapsed.Seconds(), "maxrss_kib": maxrss, "exit": code})
	if err := os.WriteFile(os.Args[1], b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "peakrss:", err)
		os.Exit(127)
	}
	os.Exit(code)
}
