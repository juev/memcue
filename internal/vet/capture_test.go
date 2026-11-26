package vet

import (
	"testing"

	"github.com/juev/memcue/internal/store"
)

func TestCaptureNoise(t *testing.T) {
	for _, tc := range []struct {
		name, kind, source, scope, content, want string
	}{
		{"fixed in main", "pitfall", "tool", "repo:x", "The inherited setting broke tests; fixed in main 7b218eb and the regression now covers it.", "completed work"},
		{"fix merged", "pitfall", "tool", "repo:x", "The fix was merged in PR #731 and the workaround is obsolete.", "completed work"},
		{"resolved in PR", "pitfall", "tool", "repo:x", "Resolved in PR #735: the suite clears the inherited server.", "completed work"},
		{"fixed in main Russian", "pitfall", "tool", "repo:x", "Исправлено в main 7b218eb: helper очищает интервал.", "completed work"},
		{"finished experiment", "pitfall", "tool", "repo:x", "This finished experiment measured twelve groups.", "finished experiment"},
		{"rejected candidate", "pitfall", "tool", "repo:x", "In the rejected Hermes candidate 24a7ef69, the sentinel hid a fact.", "rejected candidate"},
		{"first publish", "pitfall", "tool", "repo:x", "First npm publish of memcue-opencode created a staging placeholder.", "release note"},
		{"release run", "pitfall", "tool", "repo:x", "The release run 42400343386 failed before publication.", "release note"},
		{"logged out", "environment", "tool", "host:x", "On host x, claude auth status reported loggedIn false.", "transient state"},
		{"rerun", "pitfall", "tool", "repo:x", "The CI job was green after gh run rerun <run> --failed passes.", "transient state"},
		{"one off", "pitfall", "tool", "repo:x", "This one-off observation records the completed migration.", "one-off observation"},
		{"retired count", "pitfall", "tool", "repo:x", "The cleanup retired 13 facts and completed the scope move.", "one-off observation"},
		{"durable workaround", "pitfall", "tool", "repo:x", "Version 2.13 fails with export data version 5; use 2.14 or run the tests with the pinned toolchain.", ""},
		{"fixed old version remains useful", "pitfall", "tool", "repo:x", "Release 0.21.1 stored the separator literally; current releases take a lone -- after the ID as a separator.", ""},
		{"research limitation", "pitfall", "tool", "shelf:research", "The rejected candidate measured only lexical neighbours.", ""},
		{"user rule", "rule", "user", "global", "Fixed in main is not, by itself, a reason to retire a fact.", ""},
		{"event", "event", "tool", "host:x", "The host was loggedIn false during the incident.", ""},
		{"agent conclusion", "pitfall", "agent", "repo:x", "Fixed in main 7b218eb; no workaround is needed now.", "completed work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := store.Fact{Content: tc.content, Meta: store.Meta{Kind: tc.kind, Source: tc.source, Scope: tc.scope}}
			if got := CaptureNoise(f); got != tc.want {
				t.Errorf("CaptureNoise(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}
