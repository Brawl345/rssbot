package handler

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProcessContentKeepsValidUTF8(t *testing.T) {
	out := processContent(strings.Repeat("ü", 300), nil)
	if !utf8.ValidString(out) {
		t.Fatalf("output is not valid UTF-8")
	}
	if !strings.HasSuffix(out, "...") || utf8.RuneCountInString(out) != 273 {
		t.Errorf("unexpected truncation: %d runes", utf8.RuneCountInString(out))
	}
}
