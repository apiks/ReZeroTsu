package animeschedule

import "strings"

// MediaType is the closed set of AnimeSchedule media types the bot distinguishes. The zero value is TV, which is
// also what an unknown or missing type decodes to.
type MediaType int

const (
	MediaTV MediaType = iota
	MediaTVShort
	MediaONA
	// MediaDonghua is AnimeSchedule's "ONA (Chinese)"; that label is the upstream value, not our term.
	MediaDonghua
	MediaOVA
	MediaSpecial
	MediaMovie
)

// MediaTypeRef is one element of the API's mediaTypes list.
type MediaTypeRef struct {
	Name  string `json:"name"`
	Route string `json:"route"`
}

var mediaTypeByRoute = map[string]MediaType{
	"tv":          MediaTV,
	"tv-short":    MediaTVShort,
	"ona":         MediaONA,
	"ona-chinese": MediaDonghua,
	"ova":         MediaOVA,
	"special":     MediaSpecial,
	"movie":       MediaMovie,
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

// Label is the short name shown beside a non-TV entry; TV has none.
func (m MediaType) Label() string {
	switch m {
	case MediaTVShort:
		return "TV Short"
	case MediaONA:
		return "ONA"
	case MediaDonghua:
		return "Donghua"
	case MediaOVA:
		return "OVA"
	case MediaSpecial:
		return "Special"
	case MediaMovie:
		return "Movie"
	}
	return ""
}

// MediaTypeFromRoute decodes an AnimeSchedule media-type route such as "tv-short" or "ona-chinese".
func MediaTypeFromRoute(route string) (MediaType, bool) {
	mt, ok := mediaTypeByRoute[strings.ToLower(strings.TrimSpace(route))]
	return mt, ok
}
