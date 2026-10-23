package main

import (
	"testing"
	"time"
)

// An event fact gets a short life unless the caller set one; knowledge keeps no default expiry (#287).
func TestEventFactsGetAShortExpiry(t *testing.T) {
	t.Setenv("MEMCUE_EVENT_TTL_DAYS", "")
	if got, want := eventExpires("event", ""), time.Now().AddDate(0, 0, defaultEventTTL).Format(time.DateOnly); got != want {
		t.Errorf("event fact expires %q, want %q", got, want)
	}
	for _, kind := range []string{"rule", "decision", "pitfall", "environment", ""} {
		if got := eventExpires(kind, ""); got != "" {
			t.Errorf("%q fact expires %q, want no default", kind, got)
		}
	}
	if got := eventExpires("event", "2026-12-01"); got != "2026-12-01" {
		t.Errorf("an explicit expiry was replaced: %q", got)
	}
	t.Setenv("MEMCUE_EVENT_TTL_DAYS", "3")
	if got, want := eventExpires("event", ""), time.Now().AddDate(0, 0, 3).Format(time.DateOnly); got != want {
		t.Errorf("MEMCUE_EVENT_TTL_DAYS=3: %q, want %q", got, want)
	}
	t.Setenv("MEMCUE_EVENT_TTL_DAYS", "0")
	if got := eventExpires("event", ""); got != "" {
		t.Errorf("MEMCUE_EVENT_TTL_DAYS=0: %q, want no expiry", got)
	}
	t.Setenv("MEMCUE_EVENT_TTL_DAYS", "nonsense")
	if got, want := eventExpires("event", ""), time.Now().AddDate(0, 0, defaultEventTTL).Format(time.DateOnly); got != want {
		t.Errorf("a malformed setting must keep the default: %q, want %q", got, want)
	}
}
