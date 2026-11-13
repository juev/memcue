package history

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

// ReadOpenCode reads the user's and the agent's text of every OpenCode session of this machine. OpenCode keeps its
// transcripts in one SQLite base, not in a file per session: session_v2 holds the sessions (their directory, their
// parent) and session_message holds the messages, the parts inline in a JSON column. Only the text parts are read:
// tool output, reasoning, patches and the machine-written types (synthetic, system, compaction, idle) are left out,
// as are child sessions (a subagent or a fork), which repeat the parent's text.
//
// A base written before the v1 to v2 migration still holds sessions whose messages were never copied into
// session_message; those are read from the v1 tables (message, part), so a reader does not silently lose them.
// The layout is not documented and has no trustworthy version marker (PRAGMA user_version is 0 and the migration
// ledger lags the schema), so the shape is checked before any row is trusted. An unknown layout yields no messages
// instead of an error: session history is a fallback for what memcue did not store, not a command that may fail.
// dataDir is the OpenCode data directory (~/.local/share/opencode); OPENCODE_DB, when set, names the base instead,
// and a relative name is resolved against dataDir.
func ReadOpenCode(dataDir string) ([]Message, Stats, error) {
	var st Stats
	path := openCodeBase(dataDir)
	if path == "" {
		return nil, st, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, st, nil // no OpenCode base here: nothing in the history
	}
	file, err := filepath.Abs(path)
	if err != nil {
		return nil, st, nil
	}
	// mode=ro keeps SQLite from writing: the base belongs to OpenCode. immutable=1 would hide the newest sessions,
	// because the base runs in WAL mode and its -wal file carries them.
	source := url.URL{Scheme: "file", Path: file}
	source.RawQuery = "_pragma=busy_timeout(5000)&mode=ro"
	db, err := sql.Open("sqlite", source.String())
	if err != nil {
		return nil, st, nil
	}
	defer func() { _ = db.Close() }()
	// One connection: the shape check and the reading must see the same rows.
	db.SetMaxOpenConns(1)
	st.Files = 1

	read := &openCodeRead{stats: st, seen: map[string]bool{}}
	switch {
	case hasColumns(db, "session_v2", "id", "parent_id", "directory", "time_created") &&
		hasColumns(db, "session_message", "id", "session_id", "type", "seq", "time_created", "data"):
		read.v2(db)
		read.v1Fill(db)
	case hasColumns(db, "session", "id", "parent_id", "directory", "time_created") &&
		hasColumns(db, "message", "id", "session_id", "time_created", "data") &&
		hasColumns(db, "part", "message_id", "session_id", "data"):
		read.v1(db)
	default:
		return nil, st, nil // an unknown layout: no history rather than a wrong one
	}
	sort.SliceStable(read.messages, func(i, j int) bool {
		if read.messages[i].Time.Equal(read.messages[j].Time) {
			return read.messages[i].UID < read.messages[j].UID
		}
		return read.messages[i].Time.Before(read.messages[j].Time)
	})
	read.stats.Kept = len(read.messages)
	return read.messages, read.stats, nil
}

// openCodeBase resolves the transcript base: OPENCODE_DB when set, else opencode.db in the data directory.
func openCodeBase(dataDir string) string {
	if named := os.Getenv("OPENCODE_DB"); named != "" {
		if filepath.IsAbs(named) {
			return named
		}
		if dataDir == "" {
			return ""
		}
		return filepath.Join(dataDir, named)
	}
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "opencode.db")
}

// openCodeRead is the working set of one reading: what was kept, and what was left out and why.
type openCodeRead struct {
	messages []Message
	stats    Stats
	seen     map[string]bool
}

func hasColumns(db *sql.DB, table string, columns ...string) bool {
	rows, err := db.Query("select name from pragma_table_info(?)", table)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return false
	}
	for _, column := range columns {
		if !have[column] {
			return false
		}
	}
	return true
}

type openCodeSession struct {
	id, directory string
	created       int64
}

// sessions returns the top-level sessions of a table, oldest first: a child session repeats the parent's text.
func (r *openCodeRead) sessions(db *sql.DB, table string) []openCodeSession {
	rows, err := db.Query(`select id, coalesce(directory, ''), coalesce(time_created, 0) from ` + table + `
		where coalesce(parent_id, '') = '' order by time_created, id`)
	if err != nil {
		r.stats.BadLines++
		return nil
	}
	defer func() { _ = rows.Close() }()
	var list []openCodeSession
	for rows.Next() {
		var value openCodeSession
		if err := rows.Scan(&value.id, &value.directory, &value.created); err != nil {
			r.stats.BadLines++
			continue
		}
		list = append(list, value)
	}
	if err := rows.Err(); err != nil {
		r.stats.BadLines++
	}
	return list
}

// v2 reads the sessions and their messages of the current schema.
func (r *openCodeRead) v2(db *sql.DB) {
	statements, err := db.Prepare(`select id, type, coalesce(time_created, 0), data
		from session_message where session_id = ? order by seq, time_created, id`)
	if err != nil {
		r.stats.BadLines++
		return
	}
	defer func() { _ = statements.Close() }()
	for _, session := range r.sessions(db, "session_v2") {
		r.stats.Lines++
		rows, err := statements.Query(session.id)
		if err != nil {
			r.stats.BadLines++
			continue
		}
		for rows.Next() {
			var id, kind, data string
			var created int64
			if err := rows.Scan(&id, &kind, &created, &data); err != nil {
				r.stats.BadLines++
				continue
			}
			text := openCodeText(kind, data)
			if text == "" && (kind == "user" || kind == "assistant") {
				r.stats.BadLines++
				continue
			}
			r.add(session.id, session.directory, id, kind, created, text)
		}
		if err := rows.Err(); err != nil {
			r.stats.BadLines++
		}
		_ = rows.Close()
	}
}

// v1Fill reads the sessions the migration to v2 left without messages, the way the pre-2 base stored them: the role
// in message.data and the text in part.data.
func (r *openCodeRead) v1Fill(db *sql.DB) {
	rows, err := db.Query(`select id, coalesce(directory, ''), coalesce(time_created, 0) from session s
		where coalesce(parent_id, '') = ''
		and not exists (select 1 from session_message m where m.session_id = s.id)`)
	if err != nil {
		return
	}
	var list []openCodeSession
	for rows.Next() {
		var value openCodeSession
		if err := rows.Scan(&value.id, &value.directory, &value.created); err != nil {
			r.stats.BadLines++
			continue
		}
		list = append(list, value)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		r.stats.BadLines++
	}
	r.v1Sessions(db, list)
}

// v1 reads every top-level session from the pre-2 tables, for a base that has no v2 tables at all.
func (r *openCodeRead) v1(db *sql.DB) {
	r.v1Sessions(db, r.sessions(db, "session"))
}

func (r *openCodeRead) v1Sessions(db *sql.DB, sessions []openCodeSession) {
	for _, session := range sessions {
		r.stats.Lines++
		rows, err := db.Query(`select m.id, coalesce(m.time_created, 0), m.data, p.data
			from message m join part p on p.message_id = m.id
			where m.session_id = ? and json_extract(p.data, '$.type') = 'text'
			order by m.time_created, m.id, p.time_created, p.id`, session.id)
		if err != nil {
			r.stats.BadLines++
			continue
		}
		for rows.Next() {
			var id, messageData, partData string
			var created int64
			if err := rows.Scan(&id, &created, &messageData, &partData); err != nil {
				r.stats.BadLines++
				continue
			}
			var message struct {
				Role string `json:"role"`
			}
			if err := json.Unmarshal([]byte(messageData), &message); err != nil {
				r.stats.BadLines++
				continue
			}
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(partData), &part); err != nil {
				r.stats.BadLines++
				continue
			}
			r.add(session.id, session.directory, id, message.Role, created, part.Text)
		}
		if err := rows.Err(); err != nil {
			r.stats.BadLines++
		}
		_ = rows.Close()
	}
}

// add keeps one text of a session as a message, with OpenCode's message id as its identity.
func (r *openCodeRead) add(sessionID, directory, id, role string, created int64, text string) {
	if role != "user" && role != "assistant" {
		r.stats.Machine++ // a machine-written entry (synthetic, compaction, idle, system) or an unknown role
		return
	}
	if text == "" {
		r.stats.BadLines++
		return
	}
	uid := id
	if uid == "" {
		uid = sessionID + ":" + role + ":" + text[:min(len(text), 64)]
	}
	if r.seen[uid] {
		r.stats.Duplicates++
		return
	}
	r.seen[uid] = true
	r.messages = append(r.messages, Message{
		UID:     uid,
		Session: sessionID,
		Role:    role,
		Text:    text,
		Time:    time.UnixMilli(created),
		CWD:     directory,
	})
}

// openCodeText extracts the text of a message: the prompt of a user message, the text blocks of an agent message.
// A reasoning block, a tool call and its output, a patch and a step marker carry no text of their own.
func openCodeText(kind, data string) string {
	switch kind {
	case "user":
		var message struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(data), &message); err != nil {
			return ""
		}
		return message.Text
	case "assistant":
		var message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal([]byte(data), &message); err != nil {
			return ""
		}
		var text string
		for _, block := range message.Content {
			if block.Type != "text" || block.Text == "" {
				continue
			}
			if text != "" {
				text += "\n"
			}
			text += block.Text
		}
		return text
	default:
		return ""
	}
}
