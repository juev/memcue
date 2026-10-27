//go:build research

// Command synth is the synthetic side of the Git history study of #530. It drives internal/store, the code memcue
// itself runs, and writes scratch git repositories and JSON results; it never opens a real base unless given one.
//
//	synth workload  -out DIR -result FILE [-seed N -initial N -commits N -writes N]
//	synth semantics -out DIR -memcue BIN -result FILE
//	synth roundtrip -db BASE -out DIR -memcue BIN -result FILE
//	synth interrupt -db BASE -out DIR -result FILE [-n N]
//	synth race      -db BASE -out DIR -result FILE [-for 20s]
//	synth import    -in FILE.jsonl -db BASE
//
// The text encoding and the event log here are prototypes for measurement, not a proposed implementation.
//
// Safety. Before any command runs, every GIT_* variable is removed from the environment and git is told to read
// neither the system nor the global config; every subprocess (git, the memcue merge driver git starts, cp) inherits
// that environment. Each -out must be a new directory: synth creates it 0700 with a marker file and refuses an
// existing path, a symlink included. Each -result must be a new file. Temporary files follow TMPDIR, which run.sh
// points inside its scratch directory.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// scratchMarker marks a directory synth created; import refuses to write a base anywhere else.
const scratchMarker = ".gitgrowth-scratch"

// sanitizeEnv drops the caller's git environment (GIT_DIR, GIT_WORK_TREE, GIT_OBJECT_DIRECTORY,
// GIT_ALTERNATE_OBJECT_DIRECTORIES, GIT_INDEX_FILE, GIT_CONFIG_*, GIT_TEMPLATE_DIR, ...) so that git only ever acts
// on the scratch repository named on its command line, configured by the repository's own config alone.
func sanitizeEnv() error {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			if err := os.Unsetenv(k); err != nil {
				return err
			}
		}
	}
	for k, v := range map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_TERMINAL_PROMPT": "0", "LC_ALL": "C"} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

// newDir creates dir, which must not exist, and marks it as synth's scratch.
func newDir(dir string) error {
	if dir == "" {
		return errors.New("-out is required")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fmt.Errorf("-out must be a new directory: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, scratchMarker), nil, 0o600)
}

// writeResult writes a result file that must not exist yet.
func writeResult(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("-result must be a new file: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: synth workload|semantics|roundtrip|interrupt|race|import ...")
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{"workload": runWorkload, "semantics": runSemantics,
		"roundtrip": runRoundTrip, "interrupt": runInterrupt, "race": runRace, "import": runImport}
	if err := sanitizeEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "synth:", err)
		os.Exit(1)
	}
	run, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "synth: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "synth:", err)
		os.Exit(1)
	}
}
