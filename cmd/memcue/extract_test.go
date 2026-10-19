package main

import (
	"strings"
	"testing"
)

func TestChunksSplitAtParagraphs(t *testing.T) {
	got := chunks("aaaa\n\nbbbb\n\ncccccccccc\n\n", 12)
	want := []string{"aaaa\n\nbbbb\n\n", "cccccccccc\n\n"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
	if chunks("  \n", 10) != nil {
		t.Error("blank text must give no chunks")
	}
}
