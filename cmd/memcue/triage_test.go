package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
)

// fakeTriage answers triage with answer for each group, keyed by its first fact's text, and counts the calls.
func fakeTriage(t *testing.T, answer func(facts []string) (extract.TriageResult, error)) *int {
	t.Helper()
	n := 0
	old := triageFn
	triageFn = func(facts []string) (extract.TriageResult, error) {
		n++
		return answer(facts)
	}
	t.Cleanup(func() { triageFn = old })
	return &n
}

// triageEnv sets the triage variables and reads them as main does; empty values stay unset.
func triageEnv(t *testing.T, url, model, key, minP string) {
	t.Helper()
	old := extract.Triage
	t.Cleanup(func() { extract.Triage = old })
	for k, v := range map[string]string{"MEMCUE_TRIAGE_URL": url, "MEMCUE_TRIAGE_MODEL": model,
		"MEMCUE_TRIAGE_KEY": key, "MEMCUE_TRIAGE_MIN": minP} {
		t.Setenv(k, v)
	}
	configureTriage()
}

// Without the triage variables, or with only some of them, or with a bad threshold, a dream is what it was before
// triage existed: no triage call, every group to the model, nothing about triage in the log.
func TestDreamWithoutTriageIsUnchanged(t *testing.T) {
	for name, cfg := range map[string][4]string{
		"unset":     {"", "", "", ""},
		"no key":    {"https://triage.example/decisions", "jev", "", ""},
		"no url":    {"", "jev", "k", ""},
		"bad min":   {"https://triage.example/decisions", "jev", "k", "much"},
		"min above": {"https://triage.example/decisions", "jev", "k", "1.5"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := env(t)
			_, a, b, _ := dreamBase(t)
			triageEnv(t, cfg[0], cfg[1], cfg[2], cfg[3])
			triaged := fakeTriage(t, func([]string) (extract.TriageResult, error) {
				t.Error("triage called")
				return extract.TriageResult{}, nil
			})
			calls := fakeDream(t, func(string) []extract.Action { return nil })
			if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if *triaged != 0 || len(*calls) != 1 || !strings.Contains((*calls)[0], a) || !strings.Contains((*calls)[0], b) {
				t.Errorf("triage %d, model calls %q", *triaged, *calls)
			}
			log, _ := os.ReadFile(filepath.Join(dir, "state", "memcue", "dream.log"))
			if strings.Contains(string(log), "triage") {
				t.Errorf("log mentions triage: %s", log)
			}
		})
	}
}

// With triage on, a group under the threshold makes no model call; one at it, or one triage failed on, goes on.
func TestDreamTriageDropsGroupsBelowThreshold(t *testing.T) {
	for name, tc := range map[string]struct {
		p      float64
		err    error
		sent   bool
		record triageRecord
	}{
		"below":     {p: 0.1, record: triageRecord{Dropped: 1, Input: 100}},
		"at":        {p: 0.3, sent: true, record: triageRecord{Input: 100}},
		"triage up": {err: errors.New("502"), sent: true, record: triageRecord{Errors: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := env(t)
			_, a, _, _ := dreamBase(t)
			triageEnv(t, "https://triage.example/decisions", "jev", "k", "0.3")
			triaged := fakeTriage(t, func(facts []string) (extract.TriageResult, error) {
				if len(facts) != 2 {
					t.Errorf("triage got %q", facts)
				}
				if tc.err != nil {
					return extract.TriageResult{}, tc.err
				}
				return extract.TriageResult{PAct: tc.p, Input: 100}, nil
			})
			calls := fakeDream(t, func(string) []extract.Action { return nil })
			if err := runDream([]string{"-all"}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if *triaged != 1 {
				t.Errorf("triage calls %d", *triaged)
			}
			if sent := len(*calls) == 1 && strings.Contains((*calls)[0], a); sent != tc.sent {
				t.Errorf("sent %v, model calls %q", sent, *calls)
			}
			rec := readDreamLog(t, dir)[0]
			if rec.Triage == nil || *rec.Triage != tc.record || rec.Groups != 1 {
				t.Errorf("record %+v triage %+v", rec, rec.Triage)
			}
		})
	}
}
