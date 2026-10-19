package text

import (
	"slices"
	"testing"
)

func TestWordFormsCoverEachOther(t *testing.T) {
	pairs := [][2]string{
		{"перезагрузить", "перезагрузка"},
		{"перезагрузка", "перезагрузить"},
		{"публичный", "публичного"},
		{"окружении", "окружение"},
		{"Caddy", "caddy"},
		{"reloading", "reload"},
	}
	for _, p := range pairs {
		q, d := Stems(p[0]), Stems(p[1])
		if len(q) != 1 || len(d) != 1 || !Covers(q[0], d[0]) {
			t.Errorf("%q does not cover %q (%q, %q)", p[0], p[1], q, d)
		}
	}
}

func TestShortAndLatinTermsMatchExactly(t *testing.T) {
	if Covers(Stems("рука")[0], Stems("руководство")[0]) {
		t.Error("short Russian term matched by prefix")
	}
	if Covers(Stems("go")[0], Stems("golang")[0]) {
		t.Error("Latin term matched by prefix")
	}
}

func TestQueryMarksPrefixTerms(t *testing.T) {
	if got := Query(Stems("перезагрузка")[0]); got != `"перезагруз"*` {
		t.Errorf("got %s", got)
	}
	if got := Query("caddi"); got != `"caddi"` {
		t.Errorf("got %s", got)
	}
}

func TestStemsFoldYo(t *testing.T) {
	if !slices.Equal(Stems("ещё всё ёлка"), Stems("еще все елка")) {
		t.Fatalf("ё not folded: %q vs %q", Stems("ещё всё ёлка"), Stems("еще все елка"))
	}
}

func TestNumbersVersionsAndIssueRefsNeverMatch(t *testing.T) {
	for _, s := range []string{"1.27", "Go 1.27", "v0.21.6", "#20", "!123", "2026-10-19", "42", "(v1.2.3)"} {
		for _, st := range Stems(s) {
			if st != "go" {
				t.Errorf("%q gave stem %q", s, st)
			}
		}
	}
}

func TestIdentifiersWithDigitsKeptAsWritten(t *testing.T) {
	got := Stems("бакет s3 в k8s и gpt6")
	for _, want := range []string{"s3", "k8s", "gpt6"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestStopWordsAndShortWordsDropped(t *testing.T) {
	if got := Stems("и в на по да the of a x"); len(got) != 0 {
		t.Fatalf("want nothing, got %q", got)
	}
}

func TestTermsDropActionWordsOfPrompts(t *testing.T) {
	for _, s := range []string{"да, запускай эксперимент", "давай закрывать #20", "комит и создавай мр",
		"ok, go ahead", "продолжаем"} {
		got := Terms(s)
		for _, st := range got {
			if promptStopStems[st] {
				t.Errorf("%q kept action stem %q", s, st)
			}
		}
	}
	if got := Terms("да, запускай эксперимент"); !slices.Equal(got, Stems("эксперимент")) {
		t.Errorf("want only the topic, got %q", got)
	}
	if got := Terms("давай закрывать #20"); len(got) != 0 {
		t.Errorf("want nothing, got %q", got)
	}
}

func TestTermsAreDistinct(t *testing.T) {
	got := Terms("caddy Caddy caddy reload")
	if len(got) != 2 {
		t.Fatalf("want 2 distinct terms, got %q", got)
	}
}

func TestTermsKeepTopicWords(t *testing.T) {
	got := Terms("Хочу запустить gradlew руками, без make. Что выставить в окружении?")
	for _, w := range []string{"gradlew", "make", "окружении"} {
		st, _ := stem(w)
		if !slices.Contains(got, st) {
			t.Errorf("missing %q (%q) in %q", w, st, got)
		}
	}
}
