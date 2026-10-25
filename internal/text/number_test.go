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
