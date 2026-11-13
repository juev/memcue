package history

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// dshHeader is the first line of a journal: it names the session, its working directory, the journal version and
// the delegation depth (a depth over 0 marks a subagent's session).
type dshHeader struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	CWD             string `json:"cwd"`
	DelegationDepth int    `json:"delegationDepth"`
}

// dshLine is one record line of a journal. The data stays raw until the type says it carries text: a tool/call or
// tool/result line can hold a fetched page, and its data is never unmarshalled.
type dshLine struct {
	Type string          `json:"type"`
	Seq  int64           `json:"seq"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

// dshData is the data of a message record: a user message carries its content and source directly, an assistant
// message carries them in message. The stream field of an assistant record repeats the text as deltas and is
// never read.
type dshData struct {
	ID      string     `json:"id"`
	Content []dshPart  `json:"content"`
	Source  *dshSource `json:"source"`
	Message struct {
		ID      string    `json:"id"`
		Content []dshPart `json:"content"`
	} `json:"message"`
}

// dshPart is one part of a message's content: text, reasoning or tool-call; only text is read.
type dshPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// dshSource names the writer of a user message; only the kind "user" is the human.
type dshSource struct {
	Kind string `json:"kind"`
}

// ReadDSH reads the user's and the agent's text of every DeepSeek Harness journal under root, the sessions
// directory of the dsh home (DSH_HOME, else ~/.dsh/sessions), in the order of the file paths. A journal lies in
// <encoded working directory>/<session id>/session.v<N>.jsonl.zstd, JSON lines compressed with zstd; dsh can also
// keep it plaintext (.jsonl), which is read the same way. Left out: reasoning and tool-call parts, tool output,
// the machine-written user messages (a source kind other than user, or a machine opening when the source is
// absent), and delegated sessions (delegationDepth over 0), the briefs and reports of subagents. Tool output
// carries secrets and fetched pages and is never read. An entry without a time cannot be ordered and is dropped.
// Only journal versions 3 and 4 are read; an unknown version yields no messages instead of an error: session
// history is a fallback for what memcue did not store, not a command that may fail.
func ReadDSH(root string) ([]Message, Stats, error) {
	// One decoder for the whole reading: DecodeAll is stateless and safe to run concurrently, so the parallel
	// walker shares it instead of allocating a decoder per journal.
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, Stats{}, err
	}
	defer decoder.Close()
	return readTranscriptsMatching(root, func(path string) bool {
		return strings.HasSuffix(path, ".jsonl.zstd") || strings.HasSuffix(path, ".jsonl")
	}, func(path string, st *Stats) ([]Message, error) {
		return dshReadFile(path, decoder, st)
	})
}

// dshReadFile decompresses one journal whole and walks its lines in place, reusing the line handling of the
// uncompressed readers. dsh writes a frame per record, so a journal it is still writing can end mid-frame: the
// frames that decoded are kept and the damaged tail counted, rather than losing the whole session.
func dshReadFile(path string, decoder *zstd.Decoder, st *Stats) ([]Message, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // deleted since the walk
	}
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, ".zstd") {
		data, err = decoder.DecodeAll(data, nil)
		if err != nil {
			st.BadLines++
		}
		if len(data) == 0 {
			return nil, nil
		}
	}
	st.Files++
	dir := filepath.Base(filepath.Dir(path))
	var head dshHeader
	seen := map[string]bool{}
	return eachLine(data, st, func(raw []byte) (Message, bool) {
		return dshParse(raw, dir, &head, seen, st)
	}), nil
}

// dshParse reads one journal line into a message, or says why not through the stats. head keeps the session
// header, which a journal names once at the start.
func dshParse(raw []byte, dir string, head *dshHeader, seen map[string]bool, st *Stats) (Message, bool) {
	var l dshLine
	if err := json.Unmarshal(raw, &l); err != nil {
		st.BadLines++
		return Message{}, false
	}
	if l.Type == "session" {
		var h dshHeader
		if err := json.Unmarshal(raw, &h); err != nil {
			st.BadLines++
			return Message{}, false
		}
		*head = h
		return Message{}, false
	}
	if l.Type != "user/message" && l.Type != "assistant/message" {
		return Message{}, false
	}
	if head.Version != 3 && head.Version != 4 {
		return Message{}, false // an unknown journal version: no history rather than a wrong one
	}
	if head.DelegationDepth > 0 {
		return Message{}, false // the briefs and reports of a subagent, as Claude's sidechains
	}
	var d dshData
	if err := json.Unmarshal(l.Data, &d); err != nil {
		st.BadLines++
		return Message{}, false
	}
	var id string
	var parts []dshPart
	if l.Type == "user/message" {
		id, parts = d.ID, d.Content
	} else {
		id, parts = d.Message.ID, d.Message.Content
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	if len(texts) == 0 {
		return Message{}, false
	}
	session := head.ID
	if session == "" {
		session = dir
	}
	uid := id
	if uid == "" {
		uid = session + ":" + strconv.FormatInt(l.Seq, 10)
	}
	if seen[uid] {
		st.Duplicates++
		return Message{}, false
	}
	seen[uid] = true
	if l.Time == 0 {
		st.NoTime++
		return Message{}, false
	}
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	if text == "" {
		return Message{}, false
	}
	if l.Type == "user/message" {
		// The source kind names the writer of a user message: only user is the human. A record without a
		// source falls back to the machine openings, as the Qwen reading does.
		if (d.Source != nil && d.Source.Kind != "user") || (d.Source == nil && machine(text)) {
			st.Machine++
			return Message{}, false
		}
	}
	st.Kept++
	return Message{UID: uid, Session: session, Role: strings.TrimSuffix(l.Type, "/message"),
		Text: text, Time: time.UnixMilli(l.Time).UTC(), CWD: head.CWD}, true
}
