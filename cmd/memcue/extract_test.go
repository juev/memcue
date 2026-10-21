package main

import (
	"strings"
	"testing"
	"unicode/utf8"
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

func TestChunksHardSplitLongParagraph(t *testing.T) {
	const n = 100
	line := "Длинная строка с кириллицей и ASCII text."
	para := strings.Repeat(line+"\n", 8) + "конец"
	got := chunks(para, n)
	if len(got) < 2 {
		t.Fatalf("got %d chunks, want the paragraph hard-split", len(got))
	}
	if strings.Join(got, "") != para {
		t.Error("chunks must preserve the text")
	}
	for i, c := range got {
		if len(c) > n {
			t.Errorf("chunk %d is %d bytes, want at most %d", i, len(c), n)
		}
		if !utf8.ValidString(c) {
			t.Errorf("chunk %d is not valid UTF-8", i)
		}
	}
}

func TestChunksHardSplitLongLine(t *testing.T) {
	const n = 10
	line := strings.Repeat("Привет", 20)
	got := chunks(line, n)
	if strings.Join(got, "") != line {
		t.Error("chunks must preserve the text")
	}
	for i, c := range got {
		if len(c) > n {
			t.Errorf("chunk %d is %d bytes, want at most %d", i, len(c), n)
		}
		if !utf8.ValidString(c) {
			t.Errorf("chunk %d is not valid UTF-8", i)
		}
	}
}

func TestChunksNeverExceedMaxChunk(t *testing.T) {
	texts := []string{
		strings.Repeat("обычный текст с кириллицей и ASCII.\n", 50),
		strings.Repeat("абв", 500),
		strings.Repeat("a short paragraph\n\n", 30),
	}
	for _, n := range []int{4, 7, 64, 100, 1000} {
		for ti, text := range texts {
			for i, c := range chunks(text, n) {
				if len(c) > n {
					t.Errorf("n=%d text=%d: chunk %d is %d bytes", n, ti, i, len(c))
				}
				if !utf8.ValidString(c) {
					t.Errorf("n=%d text=%d: chunk %d is not valid UTF-8", n, ti, i)
				}
			}
		}
	}
}
