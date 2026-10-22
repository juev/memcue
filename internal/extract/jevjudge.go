package extract

import "time"

// JevJudge is the decisions endpoint the judge roles share: dream triage here, recall rerank in internal/rerank. It is
// off unless the URL, the model and the key are all set. vet asked it about duplicates until #266: the review, which
// sees each candidate's nearest approved facts, retired as many without it. Numbers and limits:
// docs/research/jev-judge-2026-10.md.
var JevJudge struct {
	URL           string        // e.g. https://openrouter.ai/api/alpha/decisions
	Model         string        // e.g. typesafe/jev-1.14
	Key           string        // bearer token
	TriageTimeout time.Duration // one triage request; zero uses DefaultTriageTimeout
}

// DefaultTriageTimeout bounds one triage request. Rerank gives ten candidates 2 s; triage asks about a group of at most
// six facts, so it gets the same bound, far below the ten-minute context the callers pass: a hung endpoint costs
// seconds and then the shared breaker pauses it, not a held dream lock.
const DefaultTriageTimeout = 2 * time.Second

// JevJudgeTriageTimeout is the timeout of one triage request, with the default when the settings carry none.
func JevJudgeTriageTimeout() time.Duration {
	if JevJudge.TriageTimeout > 0 {
		return JevJudge.TriageTimeout
	}
	return DefaultTriageTimeout
}

// JevJudgeOn says whether the judge endpoint is configured.
func JevJudgeOn() bool { return JevJudge.URL != "" && JevJudge.Model != "" && JevJudge.Key != "" }
