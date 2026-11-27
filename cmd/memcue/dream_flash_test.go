package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

type flashChatRequest struct {
	Model    string          `json:"model"`
	Thinking json.RawMessage `json:"thinking"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type flashRunFixture struct {
	noise  string
	left   string
	right  string
	useful string
	global string
}

func seedFlashRun(t *testing.T) flashRunFixture {
	t.Helper()
	base, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = base.Close() }()
	add := func(content, kind, scope string) string {
		f, err := base.AddWith(content, "synthetic fixture", "", store.Meta{
			Kind: kind, Scope: scope, Source: "agent",
		})
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	return flashRunFixture{
		noise: add("The finished experiment selected the saffron menu for a one-off preview; it recorded no reusable result.",
			"other", "repo:example/ui"),
		left: add("Workflow nightly-sync.yml in repo:example/nightly-sync subscribes bot to all repositories except forks and archived ones.",
			"rule", "repo:example/nightly-sync"),
		right: add("In repo:example/nightly-sync, the nightly-sync.yml workflow subscribes bot to all repositories except forks.",
			"rule", "repo:example/nightly-sync"),
		useful: add("In repo:example/metrics, set the umber sampler interval to 5 seconds to preserve short-lived spike measurements.",
			"rule", "repo:example/metrics"),
		global: add("Keep the cobalt audit trail append-only so recovery can replay each acknowledged event.",
			"rule", "global"),
	}
}

func makeFlashRunDue(t *testing.T) {
	t.Helper()
	dir, err := stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := markDream(dir, time.Now().Add(-dreamEvery-time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func flashEndpoint(t *testing.T, failConfirmation bool, updateNoise string) (*httptest.Server, *sync.Mutex, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var kinds []string
	failed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path %q", r.URL.Path)
		}
		var request flashChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(request.Messages) != 2 || request.Model != "deepseek-flash" {
			t.Errorf("unexpected request: %+v", request)
		}
		user := request.Messages[1].Content
		var response any
		kind := ""
		nativeSplitData, nativeScopeData := false, false
		switch {
		case strings.Contains(user, `"groups":`):
			kind = "pairs"
			response = flashPairLabels(t, user)
		case strings.Contains(user, "\"evidence\""):
			kind = "confirmation"
			if updateNoise != "" {
				base, err := store.Open(factsPath())
				if err != nil {
					t.Errorf("open fixture for concurrent edit: %v", err)
				} else {
					if _, err := base.Correct(updateNoise, "The saffron preview established the reusable high-contrast focus palette."); err != nil {
						t.Errorf("edit fixture after classification: %v", err)
					}
					_ = base.Close()
				}
			}
			if failConfirmation && !failed {
				failed = true
				mu.Lock()
				kinds = append(kinds, kind)
				mu.Unlock()
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":"temporary test failure"}`)
				return
			}
			response = map[string]string{"verdict": "confirm"}
		case strings.Contains(user, "## Data:"):
			kind = "native"
			if request.Thinking != nil {
				t.Errorf("native dream request unexpectedly set thinking: %s", request.Thinking)
			}
			if strings.Contains(user, "one-off preview") {
				t.Error("screened noise was sent to native reasoning")
			}
			splitData := ""
			if start := strings.Index(user, "## Data: splitting"); start >= 0 {
				remaining := user[start:]
				if end := strings.Index(remaining, "## Data: scope"); end >= 0 {
					splitData = remaining[:end]
				}
			}
			nativeSplitData = strings.Contains(splitData, "\"facts\":[{")
			if nativeSplitData &&
				(!strings.Contains(user, "nightly-sync") || !strings.Contains(user, "umber sampler")) {
				t.Errorf("native split task did not receive the remaining fresh facts: %s", splitData)
			}
			if start := strings.Index(user, "## Data: scope"); start >= 0 {
				remaining := user[start:]
				if end := strings.Index(remaining, "## Data: groups and noise candidates"); end >= 0 {
					nativeScopeData = strings.Contains(remaining[:end], "\"facts\":[{")
				}
			}
			if nativeScopeData && !strings.Contains(user, "cobalt audit trail") {
				t.Error("native scope task did not receive the global fact")
			}
			packetID := flashPacketID(user)
			if packetID == "" {
				t.Errorf("packet ID missing from native request")
				response = map[string]any{"packet": "", "splits": []any{}, "actions": []any{}, "scopes": []any{}}
			} else {
				response = map[string]any{"packet": packetID, "splits": []any{}, "actions": []any{}, "scopes": []any{}}
			}
		default:
			kind = "noise"
			response = flashNoiseLabels(t, user)
			if request.Thinking == nil || string(request.Thinking) != `{"type":"disabled"}` {
				t.Errorf("noise request thinking = %s", request.Thinking)
			}
		}
		if kind != "native" {
			if request.Thinking == nil || string(request.Thinking) != `{"type":"disabled"}` {
				t.Errorf("%s request thinking = %s", kind, request.Thinking)
			}
		}
		mu.Lock()
		kinds = append(kinds, kind)
		if nativeSplitData {
			kinds = append(kinds, "native-split-data")
		}
		if nativeScopeData {
			kinds = append(kinds, "native-scope-data")
		}
		mu.Unlock()
		writeFlashResponse(w, response)
	}))
	t.Cleanup(server.Close)
	return server, &mu, &kinds
}

func TestFlashJudgeRetriesInvalidIDsThenSkipsMalformedPairOrConfirmation(t *testing.T) {
	for _, task := range []string{"flash-pairs", "flash-confirm"} {
		for _, recoverOnRetry := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/recover=%t", task, recoverOnRetry), func(t *testing.T) {
				var mu sync.Mutex
				requests := 0
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					requests++
					if recoverOnRetry && requests == 2 {
						writeFlashResponse(w, map[string]string{"f01.covered": "yes"})
						return
					}
					writeFlashResponse(w, map[string]string{"f99.covered": "yes", "text that should not enter the diagnostic": "yes"})
				}))
				defer endpoint.Close()
				withFlashEndpoint(t, endpoint)
				pass := flashPass{ctx: t.Context()}
				labels, err := pass.judge(dreamCallRecord{Task: task}, "system", "input", 64, []string{"f01.covered"}, "yes")
				if err != nil || (labels["f01.covered"] == "yes") != recoverOnRetry || len(pass.calls) != 2 {
					t.Fatalf("labels=%v, err=%v, calls=%+v", labels, err, pass.calls)
				}
				for _, id := range []string{"f01.covered", "f99.covered", "<invalid ID>"} {
					if !strings.Contains(pass.calls[0].Error, id) {
						t.Errorf("first attempt omitted label ID %q: %s", id, pass.calls[0].Error)
					}
				}
				if strings.Contains(pass.calls[0].Error, "should not enter") {
					t.Error("diagnostic echoed non-ID text")
				}
				if (pass.calls[1].Error == "") != recoverOnRetry {
					t.Errorf("second attempt error: %q", pass.calls[1].Error)
				}
			})
		}
	}
}

func flashPacketID(user string) string {
	const marker = "packet` must be exactly `"
	start := strings.Index(user, marker)
	if start < 0 {
		return ""
	}
	value := user[start+len(marker):]
	end := strings.IndexByte(value, '`')
	if end < 0 {
		return ""
	}
	return value[:end]
}

func flashNoiseLabels(t *testing.T, user string) map[string]string {
	t.Helper()
	var facts map[string]string
	if err := json.Unmarshal([]byte(user), &facts); err != nil {
		t.Fatalf("decode noise facts %q: %v", user, err)
	}
	labels := make(map[string]string, len(facts))
	for id, content := range facts {
		labels[id] = "useful"
		if strings.Contains(content, "one-off preview") {
			labels[id] = "noise"
		}
	}
	return labels
}

func flashPairLabels(t *testing.T, user string) map[string]string {
	t.Helper()
	var payload struct {
		Groups []struct {
			Neighbors []struct {
				ID string `json:"id"`
			} `json:"neighbors"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(user), &payload); err != nil {
		t.Fatalf("decode pair request %q: %v", user, err)
	}
	labels := map[string]string{}
	for _, group := range payload.Groups {
		for _, neighbor := range group.Neighbors {
			for _, criterion := range []string{"covered", "replaced", "resolved", "conflict", "complement"} {
				labels[neighbor.ID+"."+criterion] = "no"
			}
			labels[neighbor.ID+".covered"] = "yes"
		}
	}
	return labels
}

func writeFlashResponse(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	response, _ := json.Marshal(body)
	_, _ = fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, string(response))
}

func withFlashEndpoint(t *testing.T, server *httptest.Server) {
	t.Helper()
	oldAPI, oldModel := extract.API, extract.Model
	extract.API.URL, extract.API.Key, extract.Model = server.URL, "test-key", "deepseek-flash"
	t.Cleanup(func() { extract.API, extract.Model = oldAPI, oldModel })
}

func TestDreamRunFlashScreensConfirmsAndApplies(t *testing.T) {
	env(t)
	fixture := seedFlashRun(t)
	makeFlashRunDue(t)
	server, mu, kinds := flashEndpoint(t, false, "")
	withFlashEndpoint(t, server)
	var out bytes.Buffer
	if err := runDream([]string{"-run", "-background", "-j", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	base, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = base.Close() }()
	for id, want := range map[string]string{
		fixture.noise:  store.Retired,
		fixture.left:   store.Active,
		fixture.right:  store.Retired,
		fixture.useful: store.Active,
		fixture.global: store.Active,
	} {
		fact, err := base.Get(id)
		if err != nil || fact.Status != want {
			t.Errorf("fact %s: status %q, error %v, want %q", id, fact.Status, err, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	seen := map[string]int{}
	for _, kind := range *kinds {
		seen[kind]++
	}
	if seen["noise"] == 0 || seen["pairs"] != 1 || seen["confirmation"] != 1 ||
		seen["native"] == 0 || seen["native-split-data"] == 0 || seen["native-scope-data"] == 0 {
		t.Errorf("request stages %v, want noise, one packed pair, confirmation and native fallback", seen)
	}
}

func TestDreamRunFlashRetriesFailedConfirmation(t *testing.T) {
	env(t)
	fixture := seedFlashRun(t)
	makeFlashRunDue(t)
	server, mu, kinds := flashEndpoint(t, true, "")
	withFlashEndpoint(t, server)
	var first bytes.Buffer
	err := runDream([]string{"-run", "-j", "1"}, &first)
	if err == nil {
		t.Fatalf("run unexpectedly accepted failed confirmation, request stages were %v", *kinds)
	}
	base, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fixture.noise, fixture.left, fixture.right, fixture.useful, fixture.global} {
		fact, getErr := base.Get(id)
		if getErr != nil || fact.Status != store.Active {
			t.Errorf("fact %s changed on failed pass: status %q, error %v", id, fact.Status, getErr)
		}
	}
	_ = base.Close()

	var second bytes.Buffer
	if err := runDream([]string{"-run", "-j", "1"}, &second); err != nil {
		t.Fatal(err)
	}
	base, err = store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = base.Close() }()
	for id, want := range map[string]string{
		fixture.noise:  store.Retired,
		fixture.left:   store.Active,
		fixture.right:  store.Retired,
		fixture.useful: store.Active,
		fixture.global: store.Active,
	} {
		fact, getErr := base.Get(id)
		if getErr != nil || fact.Status != want {
			t.Errorf("retry fact %s: status %q, error %v, want %q", id, fact.Status, getErr, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	confirmations := 0
	for _, kind := range *kinds {
		if kind == "confirmation" {
			confirmations++
		}
	}
	if confirmations != 2 {
		t.Errorf("confirmation attempts %d, want failed attempt and retry", confirmations)
	}
}

func TestDreamRunFlashKeepsNoiseChangedAfterSnapshot(t *testing.T) {
	dir := env(t)
	fixture := seedFlashRun(t)
	makeFlashRunDue(t)
	server, _, _ := flashEndpoint(t, false, fixture.noise)
	withFlashEndpoint(t, server)
	if err := runDream([]string{"-run", "-j", "1"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	base, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = base.Close() }()
	noise, err := base.Get(fixture.noise)
	if err != nil || noise.Status != store.Active || !strings.Contains(noise.Content, "reusable high-contrast") {
		t.Errorf("changed noise fact %+v, error %v", noise, err)
	}
	duplicate, err := base.Get(fixture.right)
	if err != nil || duplicate.Status != store.Retired {
		t.Errorf("confirmed duplicate %+v, error %v", duplicate, err)
	}
	records := readDreamLog(t, dir)
	if len(records) == 0 || !slices.ContainsFunc(records[len(records)-1].Refused, func(line string) bool {
		return strings.Contains(line, "group changed since the packet")
	}) {
		t.Errorf("latest dream record did not refuse the stale noise action: %+v", records)
	}
}
