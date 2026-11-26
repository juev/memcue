package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
)

func TestCaptureExpires(t *testing.T) {
	want := time.Now().AddDate(0, 0, defaultReviewTTL).Format(time.DateOnly)
	for _, kind := range []string{"pitfall", "environment"} {
		for _, source := range []string{"agent", "tool"} {
			if got := captureExpires(kind, source, "repo:x", "", false); got != want {
				t.Errorf("%s/%s expiry = %q, want %q", kind, source, got, want)
			}
		}
	}
	for _, tc := range []struct{ kind, source, scope string }{
		{"rule", "user", "global"}, {"decision", "user", "repo:x"},
		{"pitfall", "user", "repo:x"}, {"pitfall", "tool", "shelf:research"}, {"", "tool", "repo:x"},
	} {
		if got := captureExpires(tc.kind, tc.source, tc.scope, "", false); got != "" {
			t.Errorf("%s/%s/%s expiry = %q, want none", tc.kind, tc.source, tc.scope, got)
		}
	}
	if got := captureExpires("pitfall", "tool", "repo:x", "2026-12-14", false); got != "2026-12-14" {
		t.Errorf("explicit expiry = %q", got)
	}
	if got := captureExpires("pitfall", "tool", "repo:x", "", true); got != "" {
		t.Errorf("durable expiry = %q, want none", got)
	}
	if got := captureExpires("event", "tool", "host:x", "", false); got != time.Now().AddDate(0, 0, defaultEventTTL).Format(time.DateOnly) {
		t.Errorf("event expiry = %q", got)
	}
}

func TestAddCaptureNoiseAndExpiry(t *testing.T) {
	env(t)
	for _, args := range [][]string{
		{"-kind", "pitfall", "-source", "tool", "Fixed in main 7b218eb; the workaround is no longer needed."},
		{"-force", "-kind", "pitfall", "-source", "tool", "Fixed in main 7b218eb; the workaround is no longer needed."},
	} {
		var out bytes.Buffer
		if err := runAdd(args, &out); err == nil || !strings.Contains(err.Error(), "completed work") {
			t.Fatalf("add %v: %v", args, err)
		}
	}
	var out bytes.Buffer
	if err := runAdd([]string{"-kind", "pitfall", "-source", "tool", "-expires", "2026-12-14", "-no-expires", "Durable fact."}, &out); err == nil {
		t.Fatal("add accepted both expiry flags")
	}

	out.Reset()
	if err := runAdd([]string{"-kind", "pitfall", "-source", "tool", "Release 2.13 rejects export data version 5; use the pinned 2.14 binary."}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := storedFact(t, strings.TrimSpace(out.String())).Expires, time.Now().AddDate(0, 0, defaultReviewTTL).Format(time.DateOnly); got != want {
		t.Errorf("pitfall expiry = %q, want %q", got, want)
	}
	out.Reset()
	if err := runAdd([]string{"-kind", "environment", "-source", "tool", "-no-expires", "On host x, use the Unix socket at /srv/memcue/socket for local requests."}, &out); err != nil {
		t.Fatal(err)
	}
	if got := storedFact(t, strings.TrimSpace(out.String())).Expires; got != "" {
		t.Errorf("durable environment expiry = %q", got)
	}
	out.Reset()
	if err := runAdd([]string{"-kind", "rule", "-source", "user", "-scope", "global", "Always check the current source before retiring a fact."}, &out); err != nil {
		t.Fatal(err)
	}
	if got := storedFact(t, strings.TrimSpace(out.String())).Expires; got != "" {
		t.Errorf("rule expiry = %q", got)
	}
}

func TestBatchCaptureNoiseAndExpiry(t *testing.T) {
	dir := env(t)
	path := filepath.Join(dir, "batch.json")
	write := func(t *testing.T, inputs []store.FactInput) {
		t.Helper()
		data, err := json.Marshal(inputs)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(t, []store.FactInput{{Content: "Fixed in main 7b218eb; the workaround is obsolete.", Meta: store.Meta{Kind: "pitfall", Source: "tool", Scope: "repo:x"}}})
	if err := runAdd([]string{"-batch", path}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "completed work") {
		t.Fatalf("batch accepted one-off work: %v", err)
	}
	write(t, []store.FactInput{{Content: "For host x, bind the test socket inside the short task scratch directory.", Meta: store.Meta{Kind: "environment", Source: "tool", Scope: "host:x"}}})
	var out bytes.Buffer
	if err := runAdd([]string{"-batch", path}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := storedFact(t, strings.TrimSpace(out.String())).Expires, time.Now().AddDate(0, 0, defaultReviewTTL).Format(time.DateOnly); got != want {
		t.Errorf("batch expiry = %q, want %q", got, want)
	}
}
