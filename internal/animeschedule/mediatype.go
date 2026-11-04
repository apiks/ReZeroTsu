package animeschedule

import "strings"

// MediaType is the closed set of AnimeSchedule media types the bot distinguishes. The zero value is TV, which is
// also what an unknown or missing type decodes to.
type MediaType int

const (
	MediaTV MediaType = iota
	MediaTVShort
	MediaONA
	MediaOVA
	MediaSpecial
	MediaMovie
)

// MediaTypeRef is one element of the API's mediaTypes list.
type MediaTypeRef struct {
	Name  string `json:"name"`
	Route string `json:"route"`
}

// mediaTypeByRoute maps each route to its base type. A Chinese route maps to the same base type as its Japanese
// twin. What tells them apart is the Donghua fact: a timetable entry takes it from the API's own Donghua flag, and an
// upcoming show takes it from isDonghua over its routes.
var mediaTypeByRoute = map[string]MediaType{
	"tv":               MediaTV,
	"tv-chinese":       MediaTV,
	"tv-short":         MediaTVShort,
	"tv-chinese-short": MediaTVShort,
	"ona":              MediaONA,
	"ona-chinese":      MediaONA,
	"ova":              MediaOVA,
	"ova-chinese":      MediaOVA,
	"special":          MediaSpecial,
	"special-chinese":  MediaSpecial,
	"movie":            MediaMovie,
	"movie-chinese":    MediaMovie,
}

// decodeMediaType returns the first recognised media type in refs, matching the route; an unknown or missing type is TV.
func decodeMediaType(refs []MediaTypeRef) MediaType {
	for _, r := range refs {
		if mt, ok := mediaTypeByRoute[strings.ToLower(strings.TrimSpace(r.Route))]; ok {
			return mt
		}
	}
	return MediaTV
}

// isDonghua applies AnimeSchedule's own rule: a show is donghua when any of its media-type routes contains "chinese".
// The match ignores case. The six such routes are tv-chinese, tv-chinese-short, ona-chinese, ova-chinese, special-chinese and movie-chinese.
func isDonghua(refs []MediaTypeRef) bool {
	for _, r := range refs {
		if strings.Contains(strings.ToLower(r.Route), "chinese") {
			return true
		}
	}
	return false
}

// Label is the short name of a non-TV media type; TV has none. TypeLabel adds Donghua.
func (m MediaType) Label() string {
	switch m {
	case MediaTVShort:
		return "TV Short"
	case MediaONA:
		return "ONA"
	case MediaOVA:
		return "OVA"
	case MediaSpecial:
		return "Special"
	case MediaMovie:
		return "Movie"
	}
	return ""
}

// TypeLabel is the label shown beside an entry: its media type's label, then "Donghua" for a donghua, as in
// "Movie · Donghua". A Chinese TV series reads "Donghua" and a plain TV show has none.
func (e ShowEntry) TypeLabel() string {
	label := e.MediaType.Label()
	switch {
	case !e.Donghua:
		return label
	case label == "":
		return "Donghua"
	}
	return label + " · Donghua"
}
