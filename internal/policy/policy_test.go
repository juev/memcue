package policy

import (
	"strings"
	"testing"

	"github.com/juev/memcue/internal/extract"
)

const source = `USER: впредь пиши коммиты только на английском, так проще искать по истории
ASSISTANT: принято
TOOL Bash: psql -h db1 -p 5432
RESULT: psql: error: connection to server at "db1" (10.0.0.5), port 5432 failed:
FATAL: password authentication failed for user "app"
USER: да, решили держать базу на db1, потому что там SSD`

func TestCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		fact   extract.Fact
		ok     bool
		reason string
	}{
		"rule from the user": {extract.Fact{Kind: "rule", Source: "user", Scope: "global",
			Evidence: "впредь пиши коммиты только на английском"}, true, ""},
		"decision confirmed by the user": {extract.Fact{Kind: "decision", Source: "user_confirmed", Scope: "repo:app",
			Evidence: "решили держать базу на db1, потому что там SSD"}, true, ""},
		"pitfall from a tool, quote across a line break": {extract.Fact{Kind: "pitfall", Source: "tool",
			Scope: "host:db1", Evidence: "port 5432 failed: FATAL: password authentication failed"}, true, ""},
		"environment with scope": {extract.Fact{Kind: "environment", Source: "tool", Scope: "host:db1",
			Evidence: `connection to server at "db1" (10.0.0.5)`}, true, ""},
		"event is dropped": {extract.Fact{Kind: "event", Source: "tool", Evidence: "port 5432 failed"}, false,
			"kind event is not kept"},
		"procedure is dropped": {extract.Fact{Kind: "procedure", Source: "user", Evidence: "принято"}, false,
			"kind procedure is not kept"},
		"no kind": {extract.Fact{Source: "user", Evidence: "принято"}, false, `kind "" is not kept`},
		"rule said by the agent": {extract.Fact{Kind: "rule", Source: "agent", Scope: "global", Evidence: "принято"},
			false, "a rule needs the user's words, source is agent"},
		"decision from a tool": {extract.Fact{Kind: "decision", Source: "tool", Evidence: "port 5432 failed"}, false,
			"a decision needs the user's words, source is tool"},
		"no evidence": {extract.Fact{Kind: "pitfall", Source: "tool", Scope: "host:db1"}, false, "no evidence"},
		"made-up evidence": {extract.Fact{Kind: "pitfall", Source: "tool", Scope: "host:db1",
			Evidence: "connection refused on port 5433"}, false, "evidence is not in the source"},
		"paraphrased user words": {extract.Fact{Kind: "rule", Source: "user", Scope: "global",
			Evidence: "коммиты пишем по-английски"}, false, "evidence is not in the source"},
		"environment without scope": {extract.Fact{Kind: "environment", Source: "tool",
			Evidence: `connection to server at "db1"`}, false, "an environment fact needs a scope"},
		"secret in content": {extract.Fact{Kind: "environment", Source: "tool", Scope: "host:db1",
			Content: "Пароль базы на db1: password=hunter2secret", Evidence: "port 5432 failed"}, false,
			"looks like a secret"},
		"token in content": {extract.Fact{Kind: "environment", Source: "tool", Scope: "service:github",
			Content: "Токен ghp_abcdefghijklmnopqrstuvwxyz0123456789", Evidence: "port 5432 failed"}, false,
			"looks like a secret"},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.fact.Content == "" {
				tc.fact.Content = "факт"
			}
			ok, reason := Check(tc.fact, source)
			if ok != tc.ok || reason != tc.reason {
				t.Errorf("Check = %v %q, want %v %q", ok, reason, tc.ok, tc.reason)
			}
		})
	}
}

func TestSecretPatternLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{"password authentication failed for user", "token budget of 1500",
		"the secret is in the commit message", "api key rotation is monthly"} {
		if secret.MatchString(s) {
			t.Errorf("%q flagged as a secret", s)
		}
	}
	if !strings.Contains(source, "password authentication failed") {
		t.Fatal("fixture changed")
	}
}
