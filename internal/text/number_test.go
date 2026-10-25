package text

import (
	"slices"
	"testing"
)

// By default a version, a date or a versioned address is one exact term instead of a dropped number: the prompt's
// own version has to reach the index, and "1.27" must never match "1.20".
func TestNumberTermsByDefault(t *testing.T) {
	t.Setenv("MEMCUE_NUMBER_TERMS", "")
	cases := []struct{ in, want string }{
		{"Go 1.27", "nm1x27"},
		{"v0.22.2", "nmv0x22x2"},
		{"2026-10-19", "nm2026x10x19"},
		{"(v1.2.3)", "nmv1x2x3"},
		{"memcue 0.17.4 вышел", "nm0x17x4"},
		{"проверять `dig @1.1.1.1`", "nm1x1x1x1"},
	}
	for _, c := range cases {
		if got := Stems(c.in); !slices.Contains(got, c.want) {
			t.Errorf("Stems(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := Terms("0.17.4"); !slices.Equal(got, []string{"nm0x17x4"}) {
		t.Errorf("Terms(0.17.4) = %q, want the version alone", got)
	}
	if got := Terms("v0.22.2"); !slices.Equal(got, []string{"nmv0x22x2"}) {
		t.Errorf("Terms(v0.22.2) = %q, want the version alone", got)
	}
	// An address after @ is its own exact term: the prompt naming it finds the fact, a neighbour address does not.
	if got := Terms("dig @1.1.1.1"); !slices.Equal(got, []string{"dig", "nm1x1x1x1"}) {
		t.Errorf("Terms(dig @1.1.1.1) = %q", got)
	}
	if got := Terms("1.1.1.1"); !slices.Equal(got, []string{"nm1x1x1x1"}) {
		t.Errorf("Terms(1.1.1.1) = %q, want the address alone", got)
	}
	if got := Stems("dig @1.1.1.10"); slices.Contains(got, "nm1x1x1x1") {
		t.Errorf("Stems(dig @1.1.1.10) = %q holds the term of 1.1.1.1", got)
	}
	// The word next to the version still stems as usual.
	if got := Terms("Go 1.27"); !slices.Equal(got, []string{"go", "nm1x27"}) {
		t.Errorf("Terms(Go 1.27) = %q", got)
	}
}

// MEMCUE_NUMBER_TERMS=0 keeps the word-only lexer: the same inputs yield no number at all.
func TestNumberTermsOptOut(t *testing.T) {
	for _, off := range []string{"0", "false", "OFF", "No"} {
		t.Run(off, func(t *testing.T) {
			t.Setenv("MEMCUE_NUMBER_TERMS", off)
			if got := Terms("Go 1.27"); !slices.Equal(got, []string{"go"}) {
				t.Errorf("Terms(Go 1.27) = %q, want the word alone", got)
			}
			if got := Terms("0.17.4"); len(got) != 0 {
				t.Errorf("Terms(0.17.4) = %q, want nothing", got)
			}
		})
	}
}

func TestNumberTermShape(t *testing.T) {
	cases := map[string]string{
		" 2026-10-19": "nm2026x10x19",
		"\tv0.22.2":   "nmv0x22x2",
		"(1.27":       "nm1x27",
		"'0.5.2":      "nm0x5x2",
		"@1.1.1.1":    "nm1x1x1x1",
	}
	for in, want := range cases {
		if got := numberTerm(in); got != want {
			t.Errorf("numberTerm(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsNumberTerm(t *testing.T) {
	yes := []string{"nm1x27", "nmv0x22x2", "nm2026x10x19", "nm0x5x2"}
	no := []string{"nm", "nmx", "nmap", "tkh123", "s3", "1x27", "nm1.27"}
	for _, s := range yes {
		if !IsNumberTerm(s) {
			t.Errorf("IsNumberTerm(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsNumberTerm(s) {
			t.Errorf("IsNumberTerm(%q) = true, want false", s)
		}
	}
}

// HasNumberTerm stands in for "Stems holds a numeric term" on the index repair's hot path, so the two must agree:
// a number inside a word has no boundary and gives no term, a ticket gives another kind of term, and a bare port
// number is not a term at all.
func TestHasNumberTermAgreesWithStems(t *testing.T) {
	t.Setenv("MEMCUE_NUMBER_TERMS", "")
	cases := []string{
		"Стенд собран под Go 1.27 и только под него.", "версия v0.22.2 вышла", "2026-10-19 из конфига",
		"проверять `dig @1.1.1.1`", "порт 55432 слушает", "PostgreSQL-16 и gpt-6.1-sol", "тикет PROJ-1234 открыт",
		"#123 закрыт", "без чисел вовсе", "s3 и k8s", "(1.27", "'0.5.2", "206.253.89.87 отвечает",
		"модель gpt-6.1-sol, 27.0 часа", "ни 1.2, ни 3.4",
	}
	for _, in := range cases {
		want := slices.ContainsFunc(Stems(in), IsNumberTerm)
		if got := HasNumberTerm(in); got != want {
			t.Errorf("HasNumberTerm(%q) = %v, the stems say %v", in, got, want)
		}
	}
	t.Setenv("MEMCUE_NUMBER_TERMS", "0")
	if HasNumberTerm("Go 1.27") {
		t.Error("HasNumberTerm(Go 1.27) = true with the opt-out")
	}
}
