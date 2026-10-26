package vet

import (
	"testing"

	"github.com/juev/memcue/internal/store"
)

func fact(id, content, origin, source string) store.Fact {
	return store.Fact{ID: id, Content: content, Origin: origin, Source: source, Status: store.Active}
}

// The moment of a process or a file is state as well as a version (#475), and a pitfall's label is trusted only when
// extraction set it from the user's or a tool's words: one with no source or written by the agent is checked by its
// text, and then only a version or a moment counts, since a pitfall names a path or a key as its cause.
func TestState(t *testing.T) {
	for _, c := range []struct {
		name, kind, source, content, want string
	}{
		{"restart time", "environment", "tool", "Шлюз перезапущен 2026-10-19 в 23:30:02 командой gateway run --replace.", "restart"},
		{"restart pid", "", "", "The gateway was restarted with run --replace (PID 46005).", "restart"},
		{"line count", "environment", "tool", "Файл /srv/app/plugin.py (419 строк) совпадает с main.", "line count"},
		{"mtime", "environment", "tool", "File /srv/app/store.db has not changed since 2026-10-19 16:04.", "mtime"},
		{"mtime ru", "environment", "tool", "Файл /srv/app/store.db не менялся с 2026-10-19 16:04.", "mtime"},
		{"a schedule", "environment", "tool", "The backup job runs at 03:00 every night on host1.", ""},
		{"a limit", "environment", "tool", "read_file reads at most 2000 lines of a file.", ""},
		{"a major version", "environment", "tool", "host1 runs Postgres 16 behind pgbouncer.", ""},
		{"agent pitfall version", "pitfall", "agent", "The Claude Code CLI on /srv is version 2.1.301.", "version"},
		{"unsourced pitfall version", "pitfall", "", "The Claude Code CLI on /srv is version 2.1.301.", "version"},
		{"extracted pitfall", "pitfall", "tool", "The Claude Code CLI on /srv is version 2.1.301.", ""},
		{"unsourced pitfall path", "pitfall", "", "Run the suite with TOOL_BIN=/srv/bin/tool-real, not the wrapper /srv/bin/tool: it overrides XDG_STATE_HOME.", ""},
		{"agent rule", "rule", "agent", "We stay on tool 1.2.3 until the schema migration lands.", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := fact("01x", c.content, "x 2026-10-20", c.source)
			f.Kind = c.kind
			if got := State(f); got != c.want {
				t.Errorf("State(%q) = %q, want %q", c.content, got, c.want)
			}
		})
	}
}
