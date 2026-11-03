package commands

import "strings"

// MatchAnywhere is MatchAnywhereAll cut to Discord's 25 autocomplete choices.
func MatchAnywhere[T any](items []T, typed string, texts func(T) []string) []T {
	return Cap(MatchAnywhereAll(items, typed, texts))
}

// Cap cuts items to Discord's 25 autocomplete choices.
func Cap[T any](items []T) []T {
	if len(items) > maxAutocompleteChoices {
		return items[:maxAutocompleteChoices]
	}
	return items
}

// MatchAnywhereAll filters items for an autocomplete: an item matches when any
// of its texts contains the typed text, ignoring case. Items with a text that
// starts with the typed text come first; each group keeps the input order. The
// result is not cut, for a caller that orders further before applying the cap.
// Blank typed text matches everything.
func MatchAnywhereAll[T any](items []T, typed string, texts func(T) []string) []T {
	typed = strings.ToLower(strings.TrimSpace(typed))
	var starts, inside []T
	for _, item := range items {
		if typed == "" {
			starts = append(starts, item)
			continue
		}
		matched, atStart := false, false
		for _, t := range texts(item) {
			idx := strings.Index(strings.ToLower(t), typed)
			if idx < 0 {
				continue
			}
			matched = true
			if idx == 0 {
				atStart = true
				break
			}
		}
		switch {
		case atStart:
			starts = append(starts, item)
		case matched:
			inside = append(inside, item)
		}
	}
	return append(starts, inside...)
}
