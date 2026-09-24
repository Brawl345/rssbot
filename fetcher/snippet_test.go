package fetcher

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSnippetKeepsValidUTF8(t *testing.T) {
	body := []byte(strings.Repeat("ä", bodySnippetLen+10) + "\xff")
	s := snippet(body)
	if !utf8.ValidString(s) {
		t.Fatalf("snippet is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(s); n != bodySnippetLen+1 {
		t.Errorf("snippet has %d runes, want %d", n, bodySnippetLen+1)
	}
}
