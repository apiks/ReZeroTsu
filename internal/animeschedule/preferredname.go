package animeschedule

import (
	"strings"

	"ReZeroTsu/internal/database/anime_subs"
)

// PreferredName is which of a show's names a server or a member wants it called by: Title, Romaji, English or
// Native. Its zero value is Title.
type PreferredName int

const (
	PreferTitle PreferredName = iota
	PreferRomaji
	PreferEnglish
	PreferNative
)

// ParsePreferredName reads "title", "romaji", "english" or "native"; ok is false for anything else.
func ParsePreferredName(s string) (PreferredName, bool) {
	switch s {
	case "title":
		return PreferTitle, true
	case "romaji":
		return PreferRomaji, true
	case "english":
		return PreferEnglish, true
	case "native":
		return PreferNative, true
	}
	return PreferTitle, false
}

// Next is the preference after p in the settings button's cycle: Title, Romaji, English, Native, Title.
func (p PreferredName) Next() PreferredName {
	switch p {
	case PreferTitle:
		return PreferRomaji
	case PreferRomaji:
		return PreferEnglish
	case PreferEnglish:
		return PreferNative
	}
	return PreferTitle
}

// MemberPreferredName is the preferred name a member's subscription document holds. No document, and a missing or
// unknown stored value, read as Title; a member's preferred name never comes from a server.
func MemberPreferredName(doc *anime_subs.AnimeSubs) PreferredName {
	if doc == nil {
		return PreferTitle
	}
	name, _ := ParsePreferredName(doc.PreferredName)
	return name
}

// Title is the capitalised name shown to members: Title, Romaji, English or Native.
func (p PreferredName) Title() string {
	s := p.String()
	return strings.ToUpper(s[:1]) + s[1:]
}

// String is the lower-case name ParsePreferredName reads.
func (p PreferredName) String() string {
	switch p {
	case PreferRomaji:
		return "romaji"
	case PreferEnglish:
		return "english"
	case PreferNative:
		return "native"
	}
	return "title"
}

// ShownName is the name the show is called by under p: the name p prefers when the show has it, else the title.
// The fallback never tries another name.
func (e ShowEntry) ShownName(p PreferredName) string {
	var name string
	switch p {
	case PreferRomaji:
		name = e.Romaji
	case PreferEnglish:
		name = e.English
	case PreferNative:
		name = e.Native
	}
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return e.Name
}
