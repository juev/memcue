package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A temporary non-Git directory has no project identity. pre-command used to fail there, and an agent that executes
// only after an unblocked answer could not write memory from it (#705). It now checks this host's and the global
// facts only: another repository's and another host's facts stay out, so there is no all-repository fallback.
func TestPreCommandWithoutProjectChecksHostAndGlobal(t *testing.T) {
	dir := env(t)
	t.Setenv("MEMCUE_HOST", "synthetic-host")
	repo := filepath.Join(dir, "quarry")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git/config"), []byte("[remote \"origin\"]\nurl = https://synthetic.invalid/quarry.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	global := addFactForTest(t, "-kind", "rule", "-scope", "global", "Orchard deploys always need an amber review.")
	host := addFactForTest(t, "-kind", "pitfall", "-scope", "host:synthetic-host", "Orchard deploys on this host need amber mode.")
	foreign := addFactForTest(t, "-cwd", repo, "-kind", "pitfall", "-scope", "repo", "Orchard deploys in quarry need amber tokens.")
	other := addFactForTest(t, "-kind", "pitfall", "-scope", "host:another-host", "Orchard deploys on another host need amber keys.")
	work := t.TempDir()
	pull := func(args ...string) pullAnswer {
		t.Helper()
		var out bytes.Buffer
		if err := runPreCommand(append([]string{"-cwd", work, "-session", "synthetic-no-project", "-action", "deploy"}, args...), &out); err != nil {
			t.Fatalf("pre-command %q in a directory without project identity: %v", args, err)
		}
		var answer pullAnswer
		if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
			t.Fatalf("pre-command %q: %v\n%s", args, err, out.String())
		}
		return answer
	}
	first := pull("--", "orchard deploy amber")
	got := map[string]bool{}
	for _, f := range first.Facts {
		got[f.ID] = true
	}
	if !got[global] || !got[host] {
		t.Errorf("host and global facts missing without a project: got %v, want %s and %s", got, global, host)
	}
	if got[foreign] || got[other] {
		t.Errorf("another repository's or host's fact applied without a project: %v", got)
	}
	if !first.Blocked || !strings.HasPrefix(first.Hint, "No project identity: only this host's and global facts were checked.") {
		t.Fatalf("first answer: blocked=%v hint=%q", first.Blocked, first.Hint)
	}
	if acked := pull("-ack", first.Receipt, "--", "orchard deploy amber"); acked.Blocked {
		t.Fatalf("acknowledging host and global facts did not unblock: %+v", acked)
	}
}

// The session note shows where -ack goes (#704): placed after --, it is query text and starts a new request, so the
// receipt is never acknowledged. The form the note prints must acknowledge; the form agents tried must not.
func TestPreCommandNoteAckFormAcknowledges(t *testing.T) {
	dir := env(t)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git/config"), []byte("[remote \"origin\"]\nurl = https://synthetic.invalid/orchard.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	addFactForTest(t, "-cwd", dir, "-kind", "pitfall", "-scope", "repo", "Orchard build requires amber mode only for synthetic fixtures.")
	head := []string{"-cwd", dir, "-session", "synthetic-ack-order"}
	pull := func(args ...string) pullAnswer {
		t.Helper()
		var out bytes.Buffer
		if err := runPreCommand(append(append([]string{}, head...), args...), &out); err != nil {
			t.Fatalf("pre-command %q: %v", args, err)
		}
		var answer pullAnswer
		if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
			t.Fatalf("pre-command %q: %v\n%s", args, err, out.String())
		}
		return answer
	}
	request := []string{"-action", "build", "--", "orchard build"}

	first := pull(request...)
	if !first.Blocked || first.Receipt == "" {
		t.Fatalf("the synthetic pitfall did not block: %+v", first)
	}
	if !strings.Contains(first.Hint, "-ack RECEIPT before --") {
		t.Errorf("hint does not say where -ack goes: %q", first.Hint)
	}
	if trailing := pull(append(request, "-ack", first.Receipt)...); !trailing.Blocked {
		t.Fatalf("-ack after -- acknowledged the receipt: %+v", trailing)
	}

	start := strings.Index(preCommandNote, "(`-action")
	end := strings.Index(preCommandNote, "`)")
	if start < 0 || end < start {
		t.Fatalf("the note shows no ack form:\n%s", preCommandNote)
	}
	form := strings.ReplaceAll(preCommandNote[start+2:end], `"<words>"`, "WORDS")
	again := pull(request...)
	var args []string
	for _, token := range strings.Fields(form) {
		switch token {
		case `"<family>"`:
			token = "build"
		case "RECEIPT":
			token = again.Receipt
		case "WORDS":
			token = "orchard build"
		}
		args = append(args, token)
	}
	if acked := pull(args...); acked.Blocked {
		t.Fatalf("the note's ack form %q did not acknowledge: %+v", form, acked)
	}
}

type failedPullOutput struct{ bytes int }

func (w *failedPullOutput) Write(p []byte) (int, error) {
	n := len(p) / 2
	w.bytes += n
	return n, errors.New("synthetic broken output")
}

func TestPreCommandReservesBeforeOutputFailure(t *testing.T) {
	dir := env(t)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git/config"), []byte("[remote \"origin\"]\nurl = https://synthetic.invalid/orchard.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	addFactForTest(t, "-cwd", dir, "-kind", "pitfall", "-scope", "repo", "Orchard build requires amber mode only for synthetic fixtures.")
	args := []string{"-cwd", dir, "-session", "synthetic-output-failure", "-action", "build", "orchard build"}
	writer := &failedPullOutput{}
	for range 100 {
		if err := runPreCommand(args, writer); err == nil {
			t.Fatal("expected failed output or exhausted budget")
		}
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("MEMCUE_STATE_DIR"), "pre-command.json"))
	if err != nil {
		t.Fatalf("failed output lost reserved accounting: %v", err)
	}
	var state pullState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	task := state.Tasks[pullHash("synthetic-output-failure")]
	if task == nil || task.Spent <= pullInstructionBytes || task.Spent > pullTaskBytes {
		t.Fatalf("reservation not conservative: %+v", task)
	}
	if writer.bytes > pullTaskBytes {
		t.Fatalf("failed output escaped cap: %d", writer.bytes)
	}
	t.Logf("partial output=%d bytes, reserved=%d bytes", writer.bytes, task.Spent)
}
