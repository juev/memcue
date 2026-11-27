package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

func TestDreamRunAllNoiseResumesAndVisitsEveryActiveFact(t *testing.T) {
	env(t)
	dir, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	ids := addNoiseCandidates(t, 41)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for i := range 959 {
		f, err := s.AddWith(fmt.Sprintf("Synthetic rule %d preserves the original audit log.", i), "fixture", "", store.Meta{Kind: "rule", Source: "user"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.ID)
	}
	if err := markBase(dir); err != nil {
		t.Fatal(err)
	}
	protected := ids[len(ids)-1]
	calls := fakeDream(t, func(packet string) []extract.Action {
		if strings.Contains(packet, protected) {
			return []extract.Action{{Op: "retire", ID: protected, Kind: "noise", Reason: "incorrect test proposal"}}
		}
		return []extract.Action{}
	})
	fakeDreamRun(t, nil)
	good := dreamRunFn
	first := ""
	dreamRunFn = func(ctx context.Context, packet string) (extract.DreamReply, error) {
		id := flashPacketID(packet)
		if first == "" {
			first = id
		}
		if first != id {
			return extract.DreamReply{}, errModel
		}
		return good(ctx, packet)
	}
	args := []string{"-run", "-all", "-noise", "-j", "1"}
	var out bytes.Buffer
	if err := runDream(args, &out); !errors.Is(err, errModel) {
		t.Fatalf("interrupted sweep: %v", err)
	}
	dreamRunFn = good
	if err := runDream(args, &out); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		seen := 0
		for _, packet := range *calls {
			seen += strings.Count(packet, id)
		}
		if seen != 1 {
			t.Fatalf("fact %s appeared %d times in answered packets", id, seen)
		}
	}
	if !strings.Contains(out.String(), "1000 active") || !strings.Contains(out.String(), "0 remaining") || storedFact(t, protected).Status != store.Active {
		var progress []string
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "dream -noise:") {
				progress = append(progress, line)
			}
		}
		t.Fatalf("protected=%s, progress=%v", storedFact(t, protected).Status, progress)
	}
	*calls = nil
	if err := runDream(args, &out); err != nil || len(*calls) != 0 {
		t.Fatalf("immediate repeat made %d model calls: %v", len(*calls), err)
	}
}

func TestDreamRunAllNoiseUsesFlashWithoutOtherTasks(t *testing.T) {
	env(t)
	ids := addNoiseCandidates(t, 1)
	protected := seedFlashRun(t).useful
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request flashChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 || string(request.Thinking) != `{"type":"disabled"}` {
			t.Errorf("invalid Flash request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		labels := flashNoiseLabels(t, request.Messages[1].Content)
		for id := range labels {
			labels[id] = "noise"
		}
		writeFlashResponse(w, labels)
	}))
	defer endpoint.Close()
	withFlashEndpoint(t, endpoint)
	var out bytes.Buffer
	if err := runDream([]string{"-run", "-all", "-noise", "-j", "4"}, &out); err != nil {
		t.Fatal(err)
	}
	if storedFact(t, ids[0]).Status != store.Retired || storedFact(t, protected).Status != store.Active || !strings.Contains(out.String(), "0 remaining") {
		t.Fatalf("unexpected noise result: %s", out.String())
	}
}

func addNoiseCandidates(t *testing.T, count int) []string {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var ids []string
	for i := range count {
		f, err := s.AddWith(fmt.Sprintf("First npm publish of synthetic project-%d completed with a temporary placeholder.", i),
			"example 2026-11-26", "", store.Meta{Kind: "pitfall", Source: "tool"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestDreamRunNoiseReachesCandidatesAfterAnEmptyAnswer(t *testing.T) {
	env(t)
	ids := addNoiseCandidates(t, 45)
	target := ids[len(ids)-1]
	calls := fakeDream(t, func(packet string) []extract.Action {
		if strings.Contains(packet, target) {
			return []extract.Action{{Op: "retire", ID: target, Kind: "noise", Reason: "The text records a one-off publish."}}
		}
		return []extract.Action{}
	})
	fakeDreamRun(t, nil)
	args := []string{"-run", "-all", "-noise", "-j", "1"}
	var out bytes.Buffer
	if err := runDream(args, &out); err != nil {
		t.Fatal(err)
	}
	if got := storedFact(t, target); got.Status != store.Retired {
		t.Fatalf("last candidate never retired after %d calls: %s", len(*calls), out.String())
	}
	for _, id := range ids {
		var times int
		for _, packet := range *calls {
			if strings.Contains(packet, id) {
				times++
			}
		}
		if times != 1 {
			t.Errorf("candidate %s sent %d times", id, times)
		}
		if id != target && storedFact(t, id).Status != store.Active {
			t.Errorf("kept candidate %s was retired", id)
		}
	}
	if !strings.Contains(out.String(), "0 remaining") || !strings.Contains(out.String(), "done") {
		t.Fatalf("missing completion and remaining count: %s", out.String())
	}
	*calls = nil
	if err := runDream(args, &out); err != nil || len(*calls) != 0 {
		t.Fatalf("reviewed text reached a model again: %d calls, %v", len(*calls), err)
	}

	id := ids[0]
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Relabel(id, 1, "pitfall", "global", ""); err != nil {
		t.Fatal(err)
	}
	if err := runDream(args, &out); err != nil || len(*calls) != 0 {
		t.Fatalf("labels alone reset text progress: %d calls, %v", len(*calls), err)
	}
	if _, err := s.Correct(id, "First npm publish of the synthetic package recorded another temporary outcome.", 2); err != nil {
		t.Fatal(err)
	}
	if err := runDream(args, &out); err != nil || len(*calls) != 1 || !strings.Contains((*calls)[0], id) {
		t.Fatalf("corrected candidate was not reviewed: %d calls, %v", len(*calls), err)
	}
}

func TestDreamPrepareNoiseDoesNotConsumeAnUnansweredOrStaleCandidate(t *testing.T) {
	env(t)
	id := addNoiseCandidates(t, 1)[0]
	fakeDream(t, func(string) []extract.Action { return nil })
	var out bytes.Buffer
	answer, err := prepareAnswer([]string{"-noise"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if err := runDream([]string{"-apply", answer}, &out); err == nil || !strings.Contains(err.Error(), "actions array") {
		t.Fatalf("null actions accepted: %v", err)
	}
	fakeDream(t, func(string) []extract.Action {
		return []extract.Action{{Op: "retire", ID: id, Kind: "noise", Reason: "The text records a one-off publish."}}
	})
	answer, err = prepareAnswer([]string{"-noise"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Correct(id, "First npm publish of the same project recorded a separate temporary outcome.", 1); err != nil {
		t.Fatal(err)
	}
	if err := runDream([]string{"-apply", answer}, &out); err != nil {
		t.Fatal(err)
	}
	if got := storedFact(t, id); got.Status != store.Active || got.Version != 2 {
		t.Fatalf("stale answer changed the current fact: %+v", got)
	}
	calls := fakeDream(t, func(string) []extract.Action { return []extract.Action{} })
	if err := dreamModel([]string{"-noise"}, &out); err != nil || len(*calls) != 1 || !strings.Contains((*calls)[0], id) {
		t.Fatalf("stale retirement consumed the current candidate: %d calls, %v", len(*calls), err)
	}
}
