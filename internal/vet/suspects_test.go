package vet

import (
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/store"
)

var self = Known{
	Env:      map[string]bool{"MEMCUE_FACTS": true, "MEMCUE_JUDGE_URL": true, "MEMCUE_JUDGE_KEY": true, "MEMCUE_DREAM": true},
	Commands: map[string]bool{"add": true, "dream": true, "vet": true},
	Version:  "0.5.3",
}

var suspectToday = time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local)

func suspectFact(text string) store.Fact {
	return store.Fact{ID: "f", Content: text, Created: time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)}
}

func evidence(t *testing.T, text string, k Known) string {
	t.Helper()
	s := Suspects([]store.Fact{suspectFact(text)}, k, suspectToday)
	if len(s) == 0 {
		return ""
	}
	return strings.Join(s[0].Evidence, "; ")
}

func TestSuspectSignals(t *testing.T) {
	for _, c := range []struct {
		text, want string // want: a piece of the evidence, or "" for no suspect
	}{
		{"Обёртка выставляет MEMCUE_EMBED_URL=https://routerai.ru/api/v1 и MEMCUE_JUDGE_URL.", "names MEMCUE_EMBED_URL"},
		{"Обёртка выставляет MEMCUE_JUDGE_* и MEMCUE_DREAM=1.", ""},
		{"Обёртка экспортирует MEMCUE_EMBED_*.", "names MEMCUE_EMBED_*"},
		{"`memcue label` падает без endpoint.", "names `memcue label`"},
		{"`memcue dream -all` проходит всю базу.", ""},
		{"На loffe стоит бинарь memcue 0.4.0-main.d6a4a9b.", "names memcue 0.4.0, older than 0.5.3"},
		{"memcue v0.5.1 установлен везде.", "names memcue 0.5.1"},
		{"memcue 0.5.3 установлен везде.", ""},
		{"memcue 0.6.0 ещё не вышел.", ""},
		{"memcue установлен 2026-10-19 на Mac и loffe: бинарь 0.1.2, разметку делает label.", "names memcue 0.1.2"},
		{"На обоих хостах memcue, версия 0.4.0.", "names memcue 0.4.0"},
		{"memcue 0.5.3 прекрасно работает рядом с caddy 2.8.4.", ""},
		{"memcue и caddy 2.8.4 работают вместе.", ""},
		{"memcue собирается тем же go, что и caddy 2.8.4, релиз 0.5.3.", ""},
		{"Плагин hermes-agent 0.15.2 совместим.", ""},
		{"Репозиторий memcue-test-052 лежит в scratch.", ""},
		{"Напоминание: 21 октября проверить dream в memcue.", "a reminder whose last date, 2026-10-21"},
		{"Напоминание: 2026-10-30 проверить dream.", ""},
		{"Remind on 22.10.2026 to check the dream.", "2026-10-22"},
		{"Напоминание проверить dream после релиза.", ""},
		{"Dream прошёл 2026-10-21 без ошибок.", ""},
		{"poker-backend ReminderScheduler с 2026-10-19 шлёт уведомления раз в час.", ""},
		{"2026-09-21 из хука убрали напоминание о размере контекста.", ""},
	} {
		got := evidence(t, c.text, self)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%q: evidence %q, want %q", c.text, got, c.want)
		}
	}
}

// A dev build has no release version to compare with, so it skips the version signal and keeps the others.
func TestDevBuildSkipsTheVersionSignal(t *testing.T) {
	dev := self
	dev.Version = ""
	if got := evidence(t, "memcue 0.4.0 установлен везде.", dev); got != "" {
		t.Errorf("dev build: %q", got)
	}
	if got := evidence(t, "`memcue label` падает.", dev); !strings.Contains(got, "not a command of this memcue") {
		t.Errorf("dev build verb: %q", got)
	}
}
