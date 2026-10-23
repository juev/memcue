package main

import (
	"os"
	"testing"
)

func TestMaxFactsFromEnv(t *testing.T) {
	cases := []struct {
		name  string
		value string
		unset bool
		want  int
	}{
		{name: "unset keeps the default", unset: true, want: 2},
		{name: "in range", value: "3", want: 3},
		{name: "upper bound", value: "10", want: 10},
		{name: "spaces around", value: " 4 ", want: 4},
		{name: "zero would lift the cap", value: "0", want: 2},
		{name: "negative", value: "-1", want: 2},
		{name: "above the bound", value: "11", want: 2},
		{name: "not a number", value: "abc", want: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MEMCUE_MAX_FACTS", c.value)
			if c.unset {
				// t.Setenv restores the variable after the test
				if err := os.Unsetenv("MEMCUE_MAX_FACTS"); err != nil {
					t.Fatal(err)
				}
			}
			if got := maxFactsFromEnv(2); got != c.want {
				t.Errorf("MEMCUE_MAX_FACTS=%q: got %d, want %d", c.value, got, c.want)
			}
		})
	}
}
