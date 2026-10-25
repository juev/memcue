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

// With MEMCUE_NUMBER_TERMS=0 the lexer is word-only: numbers, versions, ports and dates leave no stem at all.
func TestNumbersVersionsAndPortsNeverMatch(t *testing.T) {
	t.Setenv("MEMCUE_NUMBER_TERMS", "0")
	for _, s := range []string{"1.27", "Go 1.27", "v0.21.6", "port 55432", "2026-10-19", "42", "(v1.2.3)"} {
		for _, st := range Stems(s) {
			if st != "go" && st != "port" {
				t.Errorf("%q gave stem %q", s, st)
			}
		}
	}
}

// A ticket key is one exact term: PROJ-1234, #123 and !45 must survive the number filter with their number.
func TestTicketKeysAreOneExactTerm(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"PROJ-1234", "tkproj1234"},
		{"JIRA-12", "tkjira12"},
		{"#123", "tkh123"},
		{"!45", "tkb45"},
	} {
		got := Stems(tc.in)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q gave %q, want [%q]", tc.in, got, tc.want)
		}
		if !IsTicket(got[0]) {
			t.Errorf("%q is not recognized as a ticket term", got[0])
		}
	}
	for _, s := range []string{"proj", "s3", "k8s", "gpt6", "1234"} {
		if IsTicket(s) {
			t.Errorf("%q is a ticket term", s)
		}
	}
	got := Terms("давай брать PROJ-1234")
	if !slices.Contains(got, "tkproj1234") {
		t.Errorf("prompt lost its ticket: %q", got)
	}
	if slices.Contains(Terms("PROJ-1235"), "tkproj1234") {
		t.Error("a different ticket produced the same term")
	}
}

// A versioned name is not a ticket: its word must stay in the index and only the number is dropped.
func TestVersionedNamesKeepTheirWord(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"PostgreSQL-16", []string{"postgresql"}},
		{"Python-3.12", []string{"python"}},
		{"go-1.27", []string{"go"}},
		{"claude-opus-5-5", []string{"claud", "opus"}},
	} {
		got := Stems(tc.in)
		for _, want := range tc.want {
			if !slices.Contains(got, want) {
				t.Errorf("%q gave %q, missing %q", tc.in, got, want)
			}
		}
		for _, st := range got {
			if IsTicket(st) {
				t.Errorf("%q produced the ticket term %q", tc.in, st)
			}
		}
	}
}

// Only an uppercase project key is a ticket; a lowercase one is an ordinary word and its number is dropped.
func TestLowercaseTicketShapeIsNotATicket(t *testing.T) {
	got := Stems("proj-1234")
	if !slices.Equal(got, []string{"proj"}) {
		t.Errorf("lowercase key gave %q, want [proj]", got)
	}
	if got := Terms("Какой порт у postgres-16?"); slices.ContainsFunc(got, IsTicket) {
		t.Errorf("a versioned prompt produced a ticket: %q", got)
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
	for _, s := range []string{"да, запускай эксперимент", "давай закрывать", "комит и создавай мр",
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
	if got := Terms("давай закрывать"); len(got) != 0 {
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
