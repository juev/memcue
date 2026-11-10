package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/juev/memcue/internal/jsonctx"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/tier"
)

const pullResponseBytes = 4096
const pullTaskBytes = 16384
const pullInstructionBytes = 2048

// Local only: hashes, byte counters and explicit observations, never command history.
type pullReceipt struct {
	Request string   `json:"request"`
	Token   string   `json:"token"`
	Facts   []string `json:"facts"`
}
type pullTask struct {
	Pending     *pullReceipt    `json:"pending,omitempty"`
	LastAck     string          `json:"last_ack,omitempty"`
	LastRequest string          `json:"last_request,omitempty"`
	Spent       int             `json:"spent"`
	Epoch       string          `json:"epoch"`
	Seen        map[string]bool `json:"seen"`
	Updated     int64           `json:"updated"`
}
type problemIncident struct {
	Fact string `json:"fact"`
	At   int64  `json:"at"`
}
type pullState struct {
	Version   int                        `json:"version"`
	Tasks     map[string]*pullTask       `json:"tasks"`
	Incidents map[string]problemIncident `json:"incidents"`
}
type pullAnswer struct {
	Receipt   string       `json:"receipt,omitempty"`
	Facts     []searchFact `json:"facts"`
	Omitted   int          `json:"omitted"`
	Blocked   bool         `json:"blocked"`
	Remaining int          `json:"remaining_bytes"`
	Hint      string       `json:"hint"`
}

func pullHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Serializes counters across CLI processes. Save and sync the reservation before exposing a packet.
func (inv *invocation) withPullState(fn func(*pullState) error) error {
	dir, err := stateDir()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "pre-command.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		select {
		case <-inv.ctx.Done():
			return inv.ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	path := filepath.Join(dir, "pre-command.json")
	st := pullState{Tasks: map[string]*pullTask{}, Incidents: map[string]problemIncident{}}
	data, err := inv.readFile(path)
	if err == nil {
		if err := jsonctx.Unmarshal(inv.ctx, data, &st); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if st.Tasks == nil || st.Incidents == nil {
		return errors.New("invalid pre-command state")
	}
	// Old seen flags meant CLI output, not host acknowledgment. Preserve every byte
	// counter and observation, but require facts to be delivered/acknowledged again.
	if st.Version == 0 {
		for _, task := range st.Tasks {
			if task != nil {
				task.Seen = map[string]bool{}
				task.Pending = nil
				task.LastAck, task.LastRequest = "", ""
			}
		}
		st.Version = 1
	}
	if st.Version != 1 {
		return errors.New("unsupported pre-command state version")
	}
	now := time.Now().Unix()
	for k, v := range st.Tasks {
		if v == nil || v.Seen == nil {
			return errors.New("invalid pre-command task account")
		}
		if now-v.Updated > 30*86400 {
			delete(st.Tasks, k)
		}
	}
	for k, v := range st.Incidents {
		if now-v.At > 90*86400 {
			delete(st.Incidents, k)
		}
	}
	if err := fn(&st); err != nil {
		return err
	}
	if err := inv.ctx.Err(); err != nil {
		return err
	}
	data, err = jsonctx.Marshal(inv.ctx, st)
	if e := inv.ctx.Err(); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path+".new", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(path+".new", path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

// runPreCommand is an explicit pull, independent of adapters' general search defaults.
func (inv *invocation) runPreCommand(args []string, out io.Writer) error {
	out = inv.writer(out)
	fs := flag.NewFlagSet("pre-command", flag.ContinueOnError)
	cwd := fs.String("cwd", "", "actual command working directory (required)")
	session := fs.String("session", "", "stable task ID (required; do not change after compaction)")
	action := fs.String("action", "", "action family, not private command arguments (required)")
	ack := fs.String("ack", "", "receipt of whole facts the host has received and read; never acknowledge a failed transport")
	epoch := fs.String("epoch", "initial", "context generation; change after compaction, keeping the same session")
	words, err := parseAnywhere(fs, args)
	if err != nil {
		return err
	}
	if *cwd == "" || *session == "" || *action == "" || len(words) == 0 {
		return errors.New("pre-command requires -cwd, -session, -action and action search words")
	}
	abs, err := filepath.Abs(*cwd)
	if err != nil {
		return err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return errors.New("pre-command cwd must be a directory")
	}
	keys := tier.Keys(abs, claudeDir(), inv.ctx)
	if len(keys) == 0 {
		return errors.New("pre-command: no project identity; resolve the target repository first")
	}
	var packet []byte
	err = inv.withPullState(func(st *pullState) error {
		key := pullHash(*session)
		task := st.Tasks[key]
		if task == nil {
			if len(st.Tasks) >= 128 {
				return errors.New("pre-command: 128 active task accounts; wait for expiry or request explicit state/budget review")
			}
			task = &pullTask{Spent: pullInstructionBytes, Seen: map[string]bool{}}
			st.Tasks[key] = task
		}
		if task.Epoch != pullHash(*epoch) {
			if task.Epoch != "" {
				task.Spent += pullInstructionBytes
			}
			task.Seen = map[string]bool{}
			task.Pending = nil
			task.LastAck, task.LastRequest = "", ""
			task.Epoch = pullHash(*epoch)
		}
		task.Updated = time.Now().Unix()
		q := recall.Query{OriginOnlyUnscoped: true, Text: strings.Join(words, " "), N: 256, MinScore: recall.MinScore, Project: keys, Host: currentHost(),
			In: []string{"project", "host", "global"}, Kinds: []string{"rule", "decision", "pitfall", "environment", "procedure"}, Recurrence: map[string]int{}}
		for _, v := range st.Incidents {
			if q.Recurrence[v.Fact] < 3 {
				q.Recurrence[v.Fact]++
			}
		}
		return inv.withStore(func(s *store.Store) error {
			hits, err := recall.Search(s, q)
			if err != nil {
				return err
			}
			request := pullHash(key + "\x00" + abs + "\x00" + *action + "\x00" + q.Text + "\x00" + task.Epoch)
			type candidateFact struct {
				fact searchFact
				key  string
			}
			candidates := make([]candidateFact, 0, len(hits))
			present := map[string]bool{}
			for _, h := range hits {
				fact := searchJSON([]store.Hit{h})[0]
				fingerprint, err := jsonctx.Marshal(inv.ctx, fact)
				if err != nil {
					return err
				}
				key := pullHash(abs + "\x00" + string(fingerprint))
				candidates = append(candidates, candidateFact{fact, key})
				present[key] = true
			}
			if *ack != "" {
				switch {
				case task.Pending != nil && task.Pending.Request == request && task.Pending.Token == *ack:
					current := true
					for _, key := range task.Pending.Facts {
						if !present[key] {
							current = false
						}
					}
					if current {
						for _, key := range task.Pending.Facts {
							task.Seen[key] = true
						}
						task.LastAck, task.LastRequest = *ack, request
					}
					// A changed/retired fact invalidates the pending set; fresh retrieval
					// below must supply any current qualifications before approval.
					task.Pending = nil
				case task.LastAck == *ack && task.LastRequest == request:
					// Retrying an acknowledgment after lost transport is idempotent.
				default:
					return errors.New("pre-command: unknown or stale receipt; retry without -ack; do not execute")
				}
			}
			// At most one pending set per task. Changing the request abandons only
			// that receipt, never marks its facts seen or resets expenditure.
			task.Pending = nil
			answer := pullAnswer{Facts: []searchFact{}, Remaining: pullTaskBytes - task.Spent, Hint: "Validate scope and qualifications before action. No matches is not proof of safety."}
			var delivered []string
			for _, candidate := range candidates {
				seen := candidate.key
				if task.Seen[seen] {
					continue
				}
				answer.Facts = append(answer.Facts, candidate.fact)
				b, _ := jsonctx.Marshal(inv.ctx, answer)
				// Reserve room for overflow diagnostics and all numeric fields.
				if len(b)+512 > pullResponseBytes || len(b)+512 > answer.Remaining || len(task.Seen)+len(delivered) >= 256 {
					answer.Facts = answer.Facts[:len(answer.Facts)-1]
					answer.Omitted++
					continue
				}
				delivered = append(delivered, seen)
			}
			if len(hits) == q.N {
				answer.Omitted++
			}
			if len(delivered) > 0 {
				answer.Receipt = pullHash(request + "\x00" + strings.Join(delivered, "\x00"))
				task.Pending = &pullReceipt{Request: request, Token: answer.Receipt, Facts: delivered}
				answer.Blocked = true
				answer.Hint = "Read whole facts, then repeat this request with -ack RECEIPT before -- (tool: ack). Execute only after an unblocked response. After a transport failure retry without ack; CLI exit is not host delivery."
			}
			if answer.Omitted > 0 {
				answer.Blocked = true
				answer.Hint = "Incomplete pull: do not execute. Read and acknowledge any received facts using receipt, then narrow words with the same session. Whole facts are never truncated. If a fact cannot fit or budget runs out, request explicit review outside this budget."
			}
			b, _ := jsonctx.Marshal(inv.ctx, answer)
			if len(b)+1 > answer.Remaining {
				return errors.New("pre-command: accumulated context budget exhausted; do not execute; request explicit review")
			}
			answer.Remaining -= len(b) + 1
			// Remaining has at most the original width; charge the conservative encoded size.
			task.Spent += len(b) + 1
			b, err = jsonctx.Marshal(inv.ctx, answer)
			if err != nil {
				return err
			}
			packet = append(b, '\n')
			return nil
		})
	})
	if err != nil {
		return err
	}
	n, err := out.Write(packet)
	if err == nil && n != len(packet) {
		return io.ErrShortWrite
	}
	return err
}

// Only callers with command evidence should record an occurrence. Reusing the incident
// ID across retries/sessions deduplicates it; retrieval never calls this function.
func (inv *invocation) runObserveProblem(args []string, out io.Writer) error {
	out = inv.writer(out)
	fs := flag.NewFlagSet("observe-problem", flag.ContinueOnError)
	fact := fs.String("fact", "", "fact ID whose problem actually occurred")
	incident := fs.String("incident", "", "stable public-safe incident ID, reused across retries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fact == "" || *incident == "" || fs.NArg() != 0 {
		return errors.New("observe-problem requires -fact and -incident")
	}
	return inv.withStore(func(s *store.Store) error {
		f, err := s.Get(*fact)
		if err != nil {
			return err
		}
		if f.ID == "" {
			return fmt.Errorf("unknown fact %s", *fact)
		}
		return inv.withPullState(func(st *pullState) error {
			k := pullHash(*fact + "\x00" + *incident)
			if _, ok := st.Incidents[k]; !ok {
				if len(st.Incidents) >= 4096 {
					return errors.New("problem observation limit reached (4096 in 90 days)")
				}
				st.Incidents[k] = problemIncident{Fact: *fact, At: time.Now().Unix()}
			}
			_, err := fmt.Fprintln(out, "Problem observation recorded; this does not record successful application.")
			return err
		})
	})
}

func runPreCommand(args []string, out io.Writer) error {
	inv := newInvocation()
	defer inv.close()
	return inv.runPreCommand(args, out)
}
