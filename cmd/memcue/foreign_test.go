package main

import (
	"os"
	"testing"
)

func TestForeignFromEnv(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		unset    bool
		coverage float64
		filter   bool
	}{
		{name: "unset keeps the default", unset: true, coverage: 0.25},
		{name: "zero is the hard filter", value: "0", coverage: 0.25, filter: true},
		{name: "a gate", value: "0.5", coverage: 0.5},
		{name: "upper bound", value: "1", coverage: 1},
		{name: "spaces around", value: " 0.3 ", coverage: 0.3},
		{name: "negative", value: "-0.1", coverage: 0.25},
		{name: "above one", value: "1.5", coverage: 0.25},
		{name: "not a number", value: "abc", coverage: 0.25},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MEMCUE_FOREIGN_COVERAGE", c.value)
			if c.unset {
				// t.Setenv restores the variable after the test
				if err := os.Unsetenv("MEMCUE_FOREIGN_COVERAGE"); err != nil {
					t.Fatal(err)
				}
			}
			coverage, filter := foreignFromEnv(0.25)
			if coverage != c.coverage || filter != c.filter {
				t.Errorf("MEMCUE_FOREIGN_COVERAGE=%q: got %v %v, want %v %v", c.value, coverage, filter, c.coverage, c.filter)
			}
		})
	}
}
