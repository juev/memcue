package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Run the real entrypoint in a bounded subprocess, without a second Go runner.
func TestBridgeProcess(t *testing.T) {
	if os.Getenv("PG674_BRIDGE_TEST_HELPER") != "1" {
		return
	}
	os.Args = append([]string{"sqlite_bridge"}, os.Args[len(os.Args)-2:]...)
	main()
	os.Exit(0)
}

func runBridge(t *testing.T, fixture, output string, input ...string) ([]byte, error) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestBridgeProcess$", "--", fixture, output)
	cmd.Env = append(os.Environ(), "PG674_BRIDGE_TEST_HELPER=1")
	if len(input) > 0 {
		cmd.Stdin = strings.NewReader(input[0])
	}
	return cmd.CombinedOutput()
}

// O_NOATIME keeps verification from changing the sentinel's metadata.
func sentinel(t *testing.T, path string) (string, syscall.Stat_t) {
	t.Helper()
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOATIME, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	return string(data), stat
}

func scratchFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir() // The gate sets TMPDIR to owned scratch outside Git discovery.
	if destination := os.Getenv("PG674_FIXTURE_ARCHIVES"); destination != "" {
		t.Cleanup(func() {
			command := exec.Command("python3", "-B", "archive_fixtures.py", root, destination)
			if output, err := command.CombinedOutput(); err != nil {
				t.Errorf("archive fixtures: %v: %s", err, output)
			}
		})
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel")
	if err := cmd.Run(); err == nil {
		t.Fatal("scratch must be outside Git discovery")
	}
	fixture := filepath.Join(root, "fixture.json")
	if err := os.WriteFile(fixture, []byte(`{"facts":[],"cases":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	return root, fixture
}

func TestBridgeRefusesExistingOutputBeforeAnyWrite(t *testing.T) {
	for _, name := range []string{"normalized.json", "sqlite-full.db", "sqlite-content.db", "sqlite-full-history.json", "sqlite-content-history.json", "sqlite-results.json", "sqlite-full.db-wal", "sqlite-content.db-shm"} {
		for _, kind := range []string{"file", "symlink", "hardlink"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				root, fixture := scratchFixture(t)
				out := filepath.Join(root, "output")
				must(os.Mkdir(out, 0700))
				victim := filepath.Join(root, "victim")
				target := filepath.Join(out, name)
				must(os.WriteFile(victim, []byte("retained evidence\n"), 0640))
				switch kind {
				case "file":
					must(os.Rename(victim, target))
					victim = target
				case "symlink":
					must(os.Symlink(victim, target))
				case "hardlink":
					must(os.Link(victim, target))
				}
				beforeData, beforeStat := sentinel(t, victim)
				beforeTarget, err := os.Lstat(target)
				must(err)
				beforeDir, err := os.Stat(out)
				must(err)
				log, err := runBridge(t, fixture, out)
				if err == nil {
					t.Error("existing output must be refused")
				}
				afterData, afterStat := sentinel(t, victim)
				if beforeData != afterData || beforeStat != afterStat {
					t.Errorf("sentinel bytes or metadata changed: %s", log)
				}
				afterTarget, err := os.Lstat(target)
				if err != nil {
					t.Fatalf("refused output was removed: %v", err)
				}
				afterDir, err := os.Stat(out)
				must(err)
				if !reflect.DeepEqual(beforeTarget.Sys(), afterTarget.Sys()) || !reflect.DeepEqual(beforeDir.Sys(), afterDir.Sys()) {
					t.Error("refused output metadata changed")
				}
				entries, err := os.ReadDir(out)
				must(err)
				if len(entries) != 1 {
					t.Errorf("refused output gained files: %v", entries)
				}
			})
		}
	}
}

func TestBridgeJSONWriteRefusesExistingAliases(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "hardlink", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := scratchFixture(t)
			target, victim := filepath.Join(root, "output.json"), filepath.Join(root, "victim")
			must(os.WriteFile(victim, []byte("retained evidence\n"), 0640))
			switch kind {
			case "file":
				target = victim
			case "symlink":
				must(os.Symlink(victim, target))
			case "hardlink":
				must(os.Link(victim, target))
			case "dangling":
				must(os.Symlink(filepath.Join(root, "absent"), target))
			}
			beforeData, beforeStat := sentinel(t, victim)
			var failure any
			func() {
				defer func() { failure = recover() }()
				write(target, map[string]any{"new": true})
			}()
			if failure == nil {
				t.Error("existing JSON output must be refused")
			}
			afterData, afterStat := sentinel(t, victim)
			if beforeData != afterData || beforeStat != afterStat {
				t.Error("sentinel bytes or metadata changed")
			}
			if _, err := os.Stat(filepath.Join(root, "absent")); !os.IsNotExist(err) {
				t.Error("dangling symlink target was created")
			}
		})
	}
}

func TestBridgeFreshOutput(t *testing.T) {
	root, fixture := scratchFixture(t)
	out := filepath.Join(root, "new-output")
	log, err := runBridge(t, fixture, out)
	if err != nil || !bytes.Contains(log, []byte("SQLite controls complete")) {
		t.Fatalf("fresh output failed: %v: %s", err, log)
	}
	for _, name := range []string{"normalized.json", "sqlite-full.db", "sqlite-content.db", "sqlite-results.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatalf("missing fresh output %s: %v", name, err)
		}
	}
}

func TestBridgeRefusesDirectoryAliases(t *testing.T) {
	for _, kind := range []string{"existing", "dangling", "parent"} {
		t.Run(kind, func(t *testing.T) {
			root, fixture := scratchFixture(t)
			victim, output := filepath.Join(root, "victim"), filepath.Join(root, "alias")
			if kind != "dangling" {
				must(os.Mkdir(victim, 0700))
			}
			must(os.Symlink(victim, output))
			if kind == "parent" {
				output = filepath.Join(output, "new-child")
			}
			before, _ := os.Stat(victim)
			log, err := runBridge(t, fixture, output)
			if err == nil {
				t.Errorf("directory alias must be refused: %s", log)
			}
			after, err := os.Stat(victim)
			if kind == "dangling" {
				if !os.IsNotExist(err) {
					t.Fatal("dangling directory target was created")
				}
				return
			}
			must(err)
			if !reflect.DeepEqual(before.Sys(), after.Sys()) {
				t.Error("refused directory metadata changed")
			}
			entries, err := os.ReadDir(victim)
			must(err)
			if len(entries) != 0 {
				t.Error("refused directory gained files")
			}
		})
	}
}

func TestBridgeNormalizeWithoutOutputDirectory(t *testing.T) {
	log, err := runBridge(t, "normalize", "stems", "caches")
	if err != nil || string(log) != "\"cach\"\n" {
		t.Fatalf("normalizer changed: %v: %s", err, log)
	}
}

func TestBridgeRefusesExistingEmptyDirectoryAndFile(t *testing.T) {
	for _, kind := range []string{"directory", "file"} {
		t.Run(kind, func(t *testing.T) {
			root, fixture := scratchFixture(t)
			output := filepath.Join(root, "existing")
			if kind == "directory" {
				must(os.Mkdir(output, 0700))
			} else {
				must(os.WriteFile(output, []byte("retained output sentinel"), 0600))
			}
			before, err := os.Stat(output)
			must(err)
			log, err := runBridge(t, fixture, output)
			if err == nil {
				t.Errorf("existing output must be refused: %s", log)
			}
			after, err := os.Stat(output)
			must(err)
			if !reflect.DeepEqual(before.Sys(), after.Sys()) {
				t.Error("refused output metadata changed")
			}
			if kind == "file" {
				data, _ := sentinel(t, output)
				if data != "retained output sentinel" {
					t.Error("refused output bytes changed")
				}
			} else {
				entries, err := os.ReadDir(output)
				must(err)
				if len(entries) != 0 {
					t.Error("refused directory gained files")
				}
			}
		})
	}
}

// The adapter preserves native deduplication, history, primary text and metadata.
func TestCorpusNativeHistory(t *testing.T) {
	root := t.TempDir()
	facts := filepath.Join(root, "facts")
	if err := os.MkdirAll(filepath.Join(facts, "V"), 0700); err != nil {
		t.Fatal(err)
	}
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	one := `{"id":"` + id + `","version":1,"created":"2026-01-01T00:00:00Z","status":"active","content":"Original primary text","keywords":["auxiliarytoken"],"queries":["querytoken"],"channel":"legacy","scope":"global","prompt_version":3}`
	two := `{"id":"` + id + `","version":2,"created":"2026-01-02T00:00:00Z","status":"retired","content":"Latest primary text","source":"user","evidence":"verbatim"}`
	if err := os.WriteFile(filepath.Join(facts, "V", id+".jsonl"), []byte(two+"\n"+one+"\n"+one+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := filepath.Join(root, "cases.json")
	if err := os.WriteFile(cases, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	corpus(facts, cases, output)
	raw, err := os.ReadFile(filepath.Join(output, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture Fixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Facts) != 2 {
		t.Fatalf("history length %d", len(fixture.Facts))
	}
	if fixture.Facts[0]["content"] != "Original primary text" || fixture.Facts[0]["keywords"] != "auxiliarytoken" || fixture.Facts[0]["queries"] != "querytoken" || fixture.Facts[0]["channel"] != "legacy" || fixture.Facts[1]["status"] != "retired" || fixture.Facts[1]["evidence"] != "verbatim" {
		t.Fatal("native metadata changed")
	}
}
