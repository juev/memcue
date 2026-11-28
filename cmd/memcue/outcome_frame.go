package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"

	"github.com/juev/memcue/internal/store"
)

const outcomePrefix = "MEMCUE_OUTCOME_V1 "

type outcomeStream struct {
	out    io.Writer
	digest hash.Hash
	bytes  int64
}

func (s *outcomeStream) Write(b []byte) (int, error) {
	n, e := s.out.Write(b)
	if n > 0 {
		_, _ = s.digest.Write(b[:n])
		s.bytes += int64(n)
	}
	return n, e
}

type outcomeRecorder struct {
	inv            *invocation
	nonce          string
	stdout, stderr *outcomeStream
}

func newOutcomeRecorder(inv *invocation) *outcomeRecorder {
	nonce := os.Getenv("MEMCUE_OUTCOME_NONCE")
	b, e := hex.DecodeString(nonce)
	if e != nil || len(b) != 16 {
		nonce = ""
	}
	if len(os.Args) < 2 || !outcomeMutation(os.Args[1:]) {
		nonce = ""
	}
	return &outcomeRecorder{inv: inv, nonce: nonce, stdout: &outcomeStream{out: inv.writer(os.Stdout), digest: sha256.New()}, stderr: &outcomeStream{out: inv.writer(os.Stderr), digest: sha256.New()}}
}
func outcomeMutation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "add", "correct", "retire", "reopen":
		return true
	}
	return false
}
func (r *outcomeRecorder) finish(err error, code int) {
	if r.nonce == "" {
		return
	}
	status := "success"
	if code != 0 {
		status = "refused"
		if r.inv.effect || errors.Is(err, store.ErrAmbiguous) {
			status = "unknown"
		}
	}
	frame := struct {
		Version    int    `json:"version"`
		Nonce      string `json:"nonce"`
		Status     string `json:"status"`
		Bytes      int64  `json:"bytes"`
		SHA        string `json:"sha256"`
		ErrorBytes int64  `json:"error_bytes"`
		ErrorSHA   string `json:"error_sha256"`
	}{1, r.nonce, status, r.stdout.bytes, hex.EncodeToString(r.stdout.digest.Sum(nil)), r.stderr.bytes, hex.EncodeToString(r.stderr.digest.Sum(nil))}
	b, e := json.Marshal(frame)
	if e != nil {
		return
	}
	// Framing is opt-in; normal stdout/stderr text remains the CLI interface.
	_, _ = fmt.Fprintf(r.inv.writer(os.Stderr), "\n%s%s\n", outcomePrefix, b)
}
