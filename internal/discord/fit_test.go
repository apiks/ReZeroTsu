package discord

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func unitsOf(s string) int { return len(utf16.Encode([]rune(s))) }

func TestFitKeepsTextThatFits(t *testing.T) {
	for _, s := range []string{"", "abc", "日本語", "😀😀"} {
		if got := Fit(s, unitsOf(s)); got != s {
			t.Errorf("Fit(%q, %d) = %q, want it unchanged", s, unitsOf(s), got)
		}
	}
}

func TestFitCutsByUTF16UnitsNotBytes(t *testing.T) {
	// 100 Japanese characters are 300 bytes but 100 units: they fit a limit of 100 whole.
	jp := strings.Repeat("日", 100)
	if got := Fit(jp, 100); got != jp {
		t.Errorf("100 characters were cut to %d units, want all of them", unitsOf(got))
	}
	// 101 characters do not fit: 99 characters and an ellipsis.
	got := Fit(jp+"日", 100)
	if want := strings.Repeat("日", 99) + "…"; got != want {
		t.Errorf("Fit = %q (%d units), want %q", got, unitsOf(got), want)
	}
}

func TestFitNeverSplitsACharacterOrExceedsTheLimit(t *testing.T) {
	// Astral characters take two units; a cut must not land between them.
	s := strings.Repeat("😀", 10)
	for limit := 1; limit <= 21; limit++ {
		got := Fit(s, limit)
		if !utf8.ValidString(got) {
			t.Errorf("limit %d: %q is not valid UTF-8", limit, got)
		}
		if unitsOf(got) > limit {
			t.Errorf("limit %d: result holds %d units", limit, unitsOf(got))
		}
	}
	if got := Fit("abc", 0); got != "" {
		t.Errorf("limit 0 = %q, want empty", got)
	}
}

func TestHeadSplitsIntoChunksThatReassemble(t *testing.T) {
	s := strings.Repeat("a日😀", 700)
	var rebuilt strings.Builder
	rest := s
	for rest != "" {
		var head string
		head, rest = Head(rest, MaxContentLength)
		if head == "" || unitsOf(head) > MaxContentLength || !utf8.ValidString(head) {
			t.Fatalf("bad chunk of %d units (valid=%v)", unitsOf(head), utf8.ValidString(head))
		}
		rebuilt.WriteString(head)
	}
	if rebuilt.String() != s {
		t.Error("the chunks do not reassemble into the original text")
	}
}
