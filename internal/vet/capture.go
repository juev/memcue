package vet

import (
	"regexp"
	"strings"

	"github.com/juev/memcue/internal/store"
)

var captureNoisePatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"completed work", regexp.MustCompile(`(?:fixed|resolved) in (?:main|#[0-9]+|pr #?[0-9]+|juev/[a-z0-9_-]+)|fix(?:ed)? (?:was )?merged|исправлено в (?:main|#|pr|juev/)`)},
	{"finished experiment", regexp.MustCompile(`\bfinished experiments?\b|заверш[её]нн\p{L}* эксперимент`)},
	{"rejected candidate", regexp.MustCompile(`\brejected (?:[a-z0-9_-]+ )*candidate\b|\bcandidate [a-z0-9_-]+ rejected\b|отклон[её]нн\p{L}* кандидат`)},
	{"release note", regexp.MustCompile(`\bfirst (?:one-tag release|npm publish|publish of the)\b|\brelease notes listed only\b|\brelease run [0-9]+\b`)},
	{"transient state", regexp.MustCompile(`\bloggedin false\b|разлогинен|\bmanaged service port [0-9]+ .*already in use\b|\brerun [a-z0-9_ <>-]+ passes\b|повторный запуск [а-я0-9_ <>-]+ проходит`)},
	{"one-off observation", regexp.MustCompile(`\bone-off\b|разов(?:ая|ой) программ|в прогоне .* сняли|перенесены [0-9]+ [a-z-]*факт|\b(?:[0-9]+ facts (?:were )?retired|retired [0-9]+ facts)\b`)},
}

// CaptureNoise names content that explicitly records a one-off outcome rather than reusable knowledge. A source that
// predates source labels is checked too. The check is deliberately narrow: a doubtful pitfall or environment fact
// goes through with a bounded expiry and can later be reviewed by dream, while a wrong refusal would lose knowledge.
func CaptureNoise(f store.Fact) string {
	if f.Source != "" && f.Source != "agent" && f.Source != "tool" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(f.Kind)) {
	case "rule", "decision", "event", "procedure":
		return ""
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(f.Scope)), "shelf:") {
		return ""
	}
	content := strings.ToLower(f.Content)
	for _, p := range captureNoisePatterns {
		if p.re.MatchString(content) {
			return p.kind
		}
	}
	return ""
}
