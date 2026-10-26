package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// add writes a procedure and an event since the model stores every fact itself (#505): a procedure is found by its
// kind, an event gets the short life of #287, and the extraction-era labels repo and other stay closed to add.
func TestAddTakesProcedureAndEvent(t *testing.T) {
	env(t)
	t.Setenv("MEMCUE_EVENT_TTL_DAYS", "")
	proc := addFactForTest(t, "-kind", "procedure", "-keywords", "gateway, restart",
		"Рестарт gateway: `s6-svc -r /run/service/gateway-default` от root.")
	if f := storedFact(t, proc); f.Kind != "procedure" || f.Expires != "" {
		t.Errorf("procedure stored as %+v", f)
	}
	var out bytes.Buffer
	if err := runSearch([]string{"-kind", "procedure", "рестарт gateway"}, &out); err != nil || !strings.Contains(out.String(), proc) {
		t.Errorf("search -kind procedure: %q, %v", out.String(), err)
	}

	ev := addFactForTest(t, "-kind", "event", "Релиз 0.19.0 поставлен на все хосты.")
	if f, want := storedFact(t, ev), time.Now().AddDate(0, 0, defaultEventTTL).Format(time.DateOnly); f.Kind != "event" || f.Expires != want {
		t.Errorf("event stored as %+v, want expiry %s", f, want)
	}
	ev = addFactForTest(t, "-kind", "event", "-expires", "2026-12-01", "Миграция базы назначена на декабрь.")
	if f := storedFact(t, ev); f.Expires != "2026-12-01" {
		t.Errorf("an explicit expiry was replaced: %+v", f)
	}

	for _, kind := range []string{"repo", "other"} {
		if err := runAdd([]string{"-kind", kind, "Факт."}, &bytes.Buffer{}); err == nil {
			t.Errorf("add -kind %s succeeded", kind)
		}
	}
}

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
