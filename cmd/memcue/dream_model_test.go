package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/juev/memcue/internal/extract"
)

// The session's model in the tests: the three functions answer the parts of a packet the way the model would, and
// dreamModel plays the session that runs the memcue-dream skill: -prepare, the answers, -apply.

// dreamReply is the model's answer to the groups of a packet.
type dreamReply struct{ Actions []extract.Action }

// splitReply is the model's answer to the facts a packet gives to split.
type splitReply struct{ Splits []extract.Split }

var (
	dreamFn  = func(string) (dreamReply, error) { return dreamReply{}, nil }
	splitFn  = func(string) (splitReply, error) { return splitReply{}, nil }
	expandFn = func(string) (extract.ExpandResult, error) { return extract.ExpandResult{}, nil }
)

// dreamModel prepares a packet with args, answers it with the functions above and applies the answer; out gets what
// -apply prints. A packet with nothing to do is the end of the dream. A failing function fails the dream before
// anything is applied, as a session that could not answer applies nothing.
func dreamModel(args []string, out io.Writer) error {
	path, err := prepareAnswer(args, out)
	if err != nil || path == "" {
		return err
	}
	return runDream([]string{"-apply", path}, out)
}

// prepareAnswer prepares a packet and writes the functions' answer to it, without applying it; it returns the
// answer's path, or "" when the packet had nothing to do.
func prepareAnswer(args []string, out io.Writer) (string, error) {
	var pkt bytes.Buffer
	if err := runDream(append([]string{"-prepare"}, args...), &pkt); err != nil {
		return "", err
	}
	text := pkt.String()
	if strings.Contains(text, "nothing for the dream") {
		_, err := io.WriteString(out, text)
		return "", err
	}
	answer, err := answerPacket(text)
	if err != nil {
		return "", err
	}
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "answer.json")
	return path, writeJSONFile(path, answer)
}

// answerPacket reads the parts of a printed packet and asks the three functions about them.
func answerPacket(text string) (dreamAnswer, error) {
	var a dreamAnswer
	dir, err := stateDir()
	if err != nil {
		return a, err
	}
	b, err := os.ReadFile(filepath.Join(dir, packetFile))
	if err != nil {
		return a, err
	}
	var p dreamPacket
	if err := json.Unmarshal(b, &p); err != nil {
		return a, err
	}
	a.Packet = p.ID
	if in := packetBlock(text, "## Данные: разделение"); hasFacts(in) {
		r, err := splitFn(in)
		if err != nil {
			return a, err
		}
		a.Splits = r.Splits
	}
	if in := packetBlock(text, "## Данные: поисковые запросы"); hasFacts(in) {
		r, err := expandFn(in)
		if err != nil {
			return a, err
		}
		a.Expansions = r.Facts
	}
	if i := strings.Index(text, "## Данные: группы\n\n"); i >= 0 {
		groups := text[i+len("## Данные: группы\n\n"):]
		if !strings.HasPrefix(groups, "Групп нет.") {
			r, err := dreamFn(groups)
			if err != nil {
				return a, err
			}
			a.Actions = r.Actions
		}
	}
	return a, nil
}

// packetBlock is the JSON block under the heading of a packet.
func packetBlock(text, heading string) string {
	i := strings.Index(text, heading+"\n\n```json\n")
	if i < 0 {
		return ""
	}
	rest := text[i+len(heading)+len("\n\n```json\n"):]
	return rest[:strings.Index(rest, "\n```")]
}

func hasFacts(in string) bool {
	var v struct {
		Facts []json.RawMessage `json:"facts"`
	}
	return in != "" && json.Unmarshal([]byte(in), &v) == nil && len(v.Facts) > 0
}

// errModel is a model call that fails.
var errModel = errors.New("model down")
