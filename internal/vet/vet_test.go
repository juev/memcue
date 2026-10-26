package vet

import (
	"strings"
	"testing"

	"github.com/juev/memcue/internal/store"
)

func fact(id, content, origin, source string) store.Fact {
	return store.Fact{ID: id, Content: content, Origin: origin, Source: source, Status: store.Active}
}

const note = "Пользователь пишет коммиты по-английски и не любит маркеры TODO в коде."

func TestRulesDecideMirrorsTestsAndCoveredFacts(t *testing.T) {
	cands := []store.Fact{
		fact("01mirror-live", "Пользователь пишет коммиты по-английски.", "hermes-memory 2026-10-20", "memory"),
		fact("01mirror-gone", "В контейнере нет sqlite3.", "hermes-memory 2026-10-20", "memory"),
		fact("01test", "Проект demo: база PostgreSQL 18 слушает порт 55432.", "test 2026-10-19", "tool"),
		fact("01covered", "Плагин memcue читает usage.log только при MEMCUE_USAGE_LOG=1 и XDG_STATE_HOME в env контейнера.",
			"hermes 2026-10-20", "tool"),
		fact("01fresh", "Ключ DeepSeek для разового прогона берут из /proc/<pid>/environ шлюза.", "hermes 2026-10-21", "tool"),
	}
	approved := []store.Fact{
		fact("01old", "Плагин memcue читает usage.log только при MEMCUE_USAGE_LOG=1 и XDG_STATE_HOME в env контейнера, "+
			"иначе файла нет и прокси использования считают по state.db.", "hermes 2026-10-19", "tool"),
	}
	decided, rest := Rules(cands, approved, note, true)

	got := map[string]Decision{}
	for _, d := range decided {
		got[d.Fact.ID] = d
	}
	if d := got["01mirror-live"]; d.Verdict != Keep || d.By != "rule" {
		t.Errorf("live mirror: %+v", d)
	}
	if d := got["01mirror-gone"]; d.Verdict != Drop {
		t.Errorf("stale mirror: %+v", d)
	}
	if d := got["01test"]; d.Verdict != Drop || !strings.Contains(d.Reason, "test") {
		t.Errorf("test trace: %+v", d)
	}
	if d := got["01covered"]; d.Verdict != Drop || !strings.Contains(d.Reason, "01old") {
		t.Errorf("covered fact: %+v", d)
	}
	if len(rest) != 1 || rest[0].ID != "01fresh" {
		t.Errorf("left for the model: %+v", rest)
	}
}

// A live mirror whose words an approved fact already holds is a second copy of a live rule: dropped, not approved
// (#458). The mirror whose note is new is still kept.
func TestRulesDropAMirrorOfALiveRule(t *testing.T) {
	notes := "Навыки держат только процедуры: шаги, команды и критерии приёмки.\n§\nПользователь пишет коммиты по-английски."
	cands := []store.Fact{
		fact("01dup", "Навыки держат только процедуры: шаги, команды и критерии приёмки.", "hermes-memory 2026-10-24", "memory"),
		fact("01new", "Пользователь пишет коммиты по-английски.", "hermes-memory 2026-10-24", "memory"),
	}
	approved := []store.Fact{fact("01rule", "Навыки держат только процедуры: шаги, команды и критерии приёмки.", "x 2026-10-20", "user")}
	decided, rest := Rules(cands, approved, notes, true)
	got := map[string]Decision{}
	for _, d := range decided {
		got[d.Fact.ID] = d
	}
	if d := got["01dup"]; d.Verdict != Drop || !strings.Contains(d.Reason, "01rule") {
		t.Errorf("mirror of a live rule: %+v", d)
	}
	if d := got["01new"]; d.Verdict != Keep {
		t.Errorf("new mirror: %+v", d)
	}
	if len(rest) != 0 {
		t.Errorf("left for the model: %+v", rest)
	}
}

// Origins are "<tier> <date>", and only a tier that is exactly "test" marks a test run. A repository named test-utils
// (or testing, or test-infra) is real, and its facts must not be retired on every vet run.
func TestRulesOnlyExactTestTierIsATrace(t *testing.T) {
	cands := []store.Fact{
		fact("01test", "Проект demo: база PostgreSQL 18 слушает порт 55432.", "test 2026-10-19", "tool"),
		fact("01utils", "Проект demo: интеграционные тесты запускают через test-utils/run.sh.", "test-utils 2026-10-19", "tool"),
		fact("01testing", "Проект demo: пакет testing подключают явно, без фреймворка.", "testing 2026-10-19", "tool"),
		fact("01infra", "Проект demo: стенд поднимают из test-infra.", "test-infra 2026-10-19", "tool"),
	}
	decided, rest := Rules(cands, nil, "", false)
	if len(decided) != 1 || decided[0].Fact.ID != "01test" || decided[0].Verdict != Drop {
		t.Errorf("test trace: decided %+v", decided)
	}
	left := map[string]bool{}
	for _, f := range rest {
		left[f.ID] = true
	}
	for _, id := range []string{"01utils", "01testing", "01infra"} {
		if !left[id] {
			t.Errorf("%s was decided instead of left for the model: %+v", id, decided)
		}
	}
}

// Without the notes the mirror rule must not fire: an unreadable directory is not an empty note, and retiring every
// mirror on a machine that keeps its notes elsewhere would lose the user's memory.
func TestRulesSkipMirrorsWithoutNotes(t *testing.T) {
	cands := []store.Fact{fact("01m", "Пользователь пишет коммиты по-английски.", "hermes-memory 2026-10-20", "memory")}
	decided, rest := Rules(cands, nil, "", false)
	if len(decided) != 0 || len(rest) != 1 {
		t.Errorf("decided %+v, rest %+v", decided, rest)
	}
}

// A short fact that happens to share its words with a long one is usually a detail the long one does not state, so
// the duplicate rule leaves it to the model.
func TestRulesLeaveShortFactsToTheModel(t *testing.T) {
	approved := []store.Fact{fact("01long", "Workflow nightly-sync.yml в репозитории example/nightly-sync каждую ночь "+
		"подписывает bot на все свои репозитории кроме форков и архивных, и это видно в логах Actions.", "x 2026-10-19", "user")}
	cands := []store.Fact{fact("01short", "Workflow nightly-sync.yml подписывает бота на репозитории.", "x 2026-10-20", "user")}
	decided, rest := Rules(cands, approved, "", false)
	if len(decided) != 0 || len(rest) != 1 {
		t.Errorf("decided %+v, rest %+v", decided, rest)
	}
}

// A mirror is present only when its words appear as whole words: "Go" must not match "Django" or "Golang".
func TestRulesMirrorMatchesWholeWords(t *testing.T) {
	cands := []store.Fact{
		fact("01go", "Go", "hermes-memory 2026-10-20", "memory"),
		fact("01django", "Django", "hermes-memory 2026-10-20", "memory"),
	}
	decided, rest := Rules(cands, nil, "Проект на Django и Golang.", true)
	got := map[string]Decision{}
	for _, d := range decided {
		got[d.Fact.ID] = d
	}
	if d := got["01go"]; d.Verdict != Drop {
		t.Errorf("a mirror inside Django/Golang was kept: %+v", d)
	}
	if d := got["01django"]; d.Verdict != Keep {
		t.Errorf("a mirror of a whole word was dropped: %+v", d)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %+v", rest)
	}
}

func TestRulesTreatEqualTextAsDuplicate(t *testing.T) {
	approved := []store.Fact{fact("01a", "Gradle требует JAVA_HOME=/opt/jdk-21, иначе сборка не находит JDK.", "x 2026-10-19", "user")}
	cands := []store.Fact{fact("01b", "gradle требует JAVA_HOME=/opt/jdk-21, иначе сборка не находит JDK", "y 2026-10-20", "user")}
	decided, rest := Rules(cands, approved, "", false)
	if len(rest) != 0 || len(decided) != 1 || decided[0].Verdict != Drop || !strings.Contains(decided[0].Reason, "01a") {
		t.Errorf("decided %+v, rest %+v", decided, rest)
	}
}

func kinded(id, kind, content string) store.Fact {
	f := fact(id, content, "hermes 2026-10-25", "tool")
	f.Kind = kind
	return f
}

// Installation state is read by a command, and a stored copy is stale after the next release (#426). Only the kinds
// that can be mere state are dropped: a pitfall or a rule that names a version or a default is knowledge.
func TestRulesDropInstallationState(t *testing.T) {
	cands := []store.Fact{
		kinded("01wrapper", "environment", "На хосте loffe обёртка /opt/data/bin/tool версии 1.2.3 экспортирует TOOL_FACTS=1."),
		kinded("01path", "", "Бинарь memcue лежит в /usr/local/bin/memcue на хосте loffe."),
		kinded("01default", "other", "В Hermes ключ memory.memcue.search_facts по умолчанию включён."),
		kinded("01size", "environment", "База memcue на loffe держит 396 живых фактов и занимает 12 МБ."),
		kinded("01pitfall", "pitfall", "memcue 0.40.1 падает на пустом запросе, обход — передавать хотя бы одно слово."),
		kinded("01rule", "rule", "Ключ memory.memcue.search_facts по умолчанию не трогать: его включает только пользователь."),
		kinded("01plain", "environment", "Grafana на хосте ams слушает порт 3000 за nginx."),
		kinded("01address", "environment", "Сервис установлен на 10.0.0.5, вход по ключу deploy."),
		kinded("01file", "environment", "Hermes читает config.yaml из /opt/data по умолчанию."),
		kinded("01branch", "environment", "Ветка по умолчанию репозитория juev/skills на git.example.org — main."),
	}
	decided, rest := Rules(cands, nil, "", false)
	got := map[string]Decision{}
	for _, d := range decided {
		got[d.Fact.ID] = d
	}
	for id, part := range map[string]string{"01wrapper": "version", "01path": "path", "01default": "config default", "01size": "size"} {
		want := "installation state: the command that reads " + part + " is the answer"
		if d := got[id]; d.Verdict != Drop || d.By != "rule" || d.Reason != want {
			t.Errorf("%s: %+v, want a drop with %q", id, d, want)
		}
	}
	left := map[string]bool{}
	for _, f := range rest {
		left[f.ID] = true
	}
	for _, id := range []string{"01pitfall", "01rule", "01plain", "01address", "01file", "01branch"} {
		if !left[id] {
			t.Errorf("%s was decided instead of left for the model: %+v", id, got[id])
		}
	}
}

// A mirror of the built-in notes is decided by the mirror rule first, even when its text looks like installation state.
func TestRulesMirrorBeforeInstallationState(t *testing.T) {
	text := "На хосте loffe обёртка /opt/data/bin/tool версии 1.2.3 экспортирует TOOL_FACTS=1."
	cands := []store.Fact{fact("01m", text, "hermes-memory 2026-10-20", "memory")}
	decided, rest := Rules(cands, nil, text, true)
	if len(rest) != 0 || len(decided) != 1 || decided[0].Verdict != Keep || !strings.Contains(decided[0].Reason, "mirror") {
		t.Errorf("decided %+v, rest %+v", decided, rest)
	}
}
