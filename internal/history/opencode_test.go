package history

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const openCodeSchema = `
create table session_v2 (
  id text primary key, project_id text not null, parent_id text, directory text not null,
  title text, version text not null, time_created integer not null, time_updated integer not null);
create table session_message (
  id text primary key, session_id text not null, type text not null, seq integer not null,
  time_created integer not null, time_updated integer not null, data text not null,
  unique (session_id, seq));
create table session (
  id text primary key, project_id text not null, parent_id text, directory text not null,
  time_created integer not null, time_updated integer not null);
create table message (
  id text primary key, session_id text not null, time_created integer not null,
  time_updated integer not null, data text not null);
create table part (
  id text primary key, message_id text not null, session_id text not null, time_created integer not null,
  time_updated integer not null, data text not null);
`

// openCodeFixture builds a base with the shape OpenCode 2 writes, at the path the reader resolves.
func openCodeFixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "opencode.db"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(openCodeSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return dir, db
}

func exec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func session2(t *testing.T, db *sql.DB, id, parent, directory string, created int64) {
	exec(t, db, `insert into session_v2 (id, project_id, parent_id, directory, title, version, time_created, time_updated)
		values (?, 'project', ?, ?, 'title', '2.2.7', ?, ?)`, id, nullText(parent), directory, created, created)
}

func message2(t *testing.T, db *sql.DB, id, session, kind string, seq int, created int64, data string) {
	exec(t, db, `insert into session_message (id, session_id, type, seq, time_created, time_updated, data)
		values (?, ?, ?, ?, ?, ?, ?)`, id, session, kind, seq, created, created, data)
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func TestReadOpenCodeKeepsOnlyTheText(t *testing.T) {
	dir, db := openCodeFixture(t)
	session2(t, db, "ses_1", "", "/repo/one", 1_800_000_000_000)
	session2(t, db, "ses_2", "", "/repo/two", 1_800_000_100_000)
	session2(t, db, "ses_child", "ses_1", "/repo/one", 1_800_000_200_000)

	message2(t, db, "msg_1", "ses_1", "user", 1, 1_800_000_000_100,
		`{"text":"why is the proxy broken?","metadata":{"displayText":"why is the proxy broken?"}}`)
	message2(t, db, "msg_2", "ses_1", "assistant", 2, 1_800_000_000_200,
		`{"content":[{"type":"reasoning","text":"REASONING-CANARY"},{"type":"text","text":"the rule is stale"},`+
			`{"type":"tool","name":"shell","state":{"status":"completed","content":"TOOL-CANARY"}}]}`)
	message2(t, db, "msg_3", "ses_1", "synthetic", 3, 1_800_000_000_300, `{"text":"SYNTHETIC-CANARY"}`)
	message2(t, db, "msg_4", "ses_1", "compaction", 4, 1_800_000_000_400, `{"text":"COMPACTION-CANARY"}`)
	message2(t, db, "msg_5", "ses_2", "user", 1, 1_800_000_100_100, `{"text":"second session"}`)
	message2(t, db, "msg_child", "ses_child", "user", 1, 1_800_000_200_100, `{"text":"CHILD-CANARY"}`)

	messages, stats, err := ReadOpenCode(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("kept %d messages, want 3: %+v", len(messages), messages)
	}
	if messages[0].Text != "why is the proxy broken?" || messages[0].Role != "user" {
		t.Errorf("first message = %+v", messages[0])
	}
	if messages[0].CWD != "/repo/one" || messages[0].Session != "ses_1" || messages[0].UID != "msg_1" {
		t.Errorf("provenance of the first message = %+v", messages[0])
	}
	if messages[1].Text != "the rule is stale" || messages[1].Role != "assistant" {
		t.Errorf("second message = %+v", messages[1])
	}
	if !messages[1].Time.Equal(messages[0].Time.Add(100_000_000)) {
		t.Errorf("times are not milliseconds apart: %v then %v", messages[0].Time, messages[1].Time)
	}
	if messages[2].Text != "second session" {
		t.Errorf("third message = %+v", messages[2])
	}
	for _, message := range messages {
		for _, canary := range []string{"REASONING-CANARY", "TOOL-CANARY", "SYNTHETIC-CANARY", "COMPACTION-CANARY", "CHILD-CANARY"} {
			if message.Text == canary {
				t.Errorf("kept %q, which is not the text of a session", canary)
			}
		}
	}
	if stats.Kept != 3 || stats.Machine != 2 || stats.Files != 1 || stats.Lines != 2 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestReadOpenCodeFillsSessionsTheMigrationLeftBehind(t *testing.T) {
	dir, db := openCodeFixture(t)
	session2(t, db, "ses_v2", "", "/repo/v2", 1_800_000_000_000)
	session2(t, db, "ses_old", "", "/repo/old", 1_800_000_100_000)
	message2(t, db, "msg_v2", "ses_v2", "user", 1, 1_800_000_000_100, `{"text":"from the new tables"}`)
	// The v1 rows exist for a session the migration never copied into session_message.
	exec(t, db, `insert into session (id, project_id, parent_id, directory, time_created, time_updated)
		values ('ses_old', 'project', null, '/repo/old', 1800000100000, 1800000100000)`)
	exec(t, db, `insert into message (id, session_id, time_created, time_updated, data)
		values ('msg_old', 'ses_old', 1800000100100, 1800000100100, '{"role":"user"}')`)
	exec(t, db, `insert into part (id, message_id, session_id, time_created, time_updated, data)
		values ('prt_old', 'msg_old', 'ses_old', 1800000100100, 1800000100100, '{"type":"text","text":"from the old tables"}')`)

	messages, stats, err := ReadOpenCode(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("kept %d messages, want 2: %+v", len(messages), messages)
	}
	if messages[0].Text != "from the new tables" || messages[0].Session != "ses_v2" {
		t.Errorf("first message = %+v", messages[0])
	}
	if messages[1].Text != "from the old tables" || messages[1].Session != "ses_old" || messages[1].CWD != "/repo/old" {
		t.Errorf("second message = %+v", messages[1])
	}
	if stats.Kept != 2 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestReadOpenCodeWithoutV2TablesReadsTheOldShape(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "opencode.db"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = db.Close() }()
	exec(t, db, `create table session (id text primary key, project_id text, parent_id text, directory text not null,
		time_created integer not null, time_updated integer not null)`)
	exec(t, db, `create table message (id text primary key, session_id text not null, time_created integer not null,
		time_updated integer not null, data text not null)`)
	exec(t, db, `create table part (id text primary key, message_id text not null, session_id text not null,
		time_created integer not null, time_updated integer not null, data text not null)`)
	exec(t, db, `insert into session values ('ses_1', 'project', null, '/repo', 1800000000000, 1800000000000)`)
	exec(t, db, `insert into message values ('msg_1', 'ses_1', 1800000000100, 1800000000100, '{"role":"assistant"}')`)
	exec(t, db, `insert into part values ('prt_1', 'msg_1', 'ses_1', 1800000000100, 1800000000100, '{"type":"text","text":"old prose"}')`)

	messages, _, err := ReadOpenCode(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(messages) != 1 || messages[0].Text != "old prose" || messages[0].Role != "assistant" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestReadOpenCodeIsSilentAboutWhatItCannotRead(t *testing.T) {
	// No base at all: OpenCode never ran here.
	if messages, stats, err := ReadOpenCode(t.TempDir()); err != nil || len(messages) != 0 || stats.Kept != 0 {
		t.Fatalf("missing base: %v, %+v, %v", messages, stats, err)
	}
	// A base of another shape: no history rather than a wrong one.
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "opencode.db"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = db.Close() }()
	exec(t, db, `create table something (id text)`)
	if messages, _, err := ReadOpenCode(dir); err != nil || len(messages) != 0 {
		t.Fatalf("unknown layout: %v, %v", messages, err)
	}
}

func TestReadOpenCodeHonoursTheDatabaseOverride(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "elsewhere.db")
	db, err := sql.Open("sqlite", named)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(openCodeSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	session2(t, db, "ses_named", "", "/repo/named", 1_800_000_000_000)
	message2(t, db, "msg_named", "ses_named", "user", 1, 1_800_000_000_100, `{"text":"named base"}`)
	t.Setenv("OPENCODE_DB", named)
	messages, _, err := ReadOpenCode("")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(messages) != 1 || messages[0].Text != "named base" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestOpenCodeTextReadsOnlyTheProse(t *testing.T) {
	assistant := `{"content":[{"type":"text","text":"first"},{"type":"reasoning","text":"secret"},` +
		`{"type":"text","text":"second"},{"type":"tool","name":"shell","state":{"content":"output"}}]}`
	if got := openCodeText("assistant", assistant); got != "first\nsecond" {
		t.Errorf("assistant text = %q", got)
	}
	if got := openCodeText("user", `{"text":"prompt"}`); got != "prompt" {
		t.Errorf("user text = %q", got)
	}
	if got := openCodeText("synthetic", `{"text":"injected"}`); got != "" {
		t.Errorf("synthetic text = %q, want nothing", got)
	}
	if got := openCodeText("assistant", `not json`); got != "" {
		t.Errorf("invalid text = %q, want nothing", got)
	}
}
