package discord

// Discord counts text in UTF-16 code units, so a limit such as MaxEmbedDescriptionLength is not a byte count.
// Cutting a string with s[:limit] splits a multi-byte character and stops short of the limit for every multi-byte
// character before the cut. Fit and Head cut by the unit Discord counts and never inside a character; a source
// check (internal/archcheck) rejects slicing a string at a Discord length limit.

const ellipsis = "…"

// units is the number of UTF-16 code units r takes.
func units(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// Head splits s after the longest prefix that holds at most limit UTF-16 units and ends on a character boundary.
// rest is empty when all of s fits.
func Head(s string, limit int) (head, rest string) {
	n := 0
	for i, r := range s {
		u := units(r)
		if n+u > limit {
			return s[:i], s[i:]
		}
		n += u
	}
	return s, ""
}

// Fit returns s when it holds at most limit UTF-16 units, otherwise a prefix that ends in an ellipsis and holds at
// most limit units.
func Fit(s string, limit int) string {
	if limit < 1 {
		return ""
	}
	if head, rest := Head(s, limit); rest == "" {
		return head
	}
	head, _ := Head(s, limit-1)
	return head + ellipsis
}
