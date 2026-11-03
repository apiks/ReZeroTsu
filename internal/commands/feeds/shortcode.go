package feeds

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"ReZeroTsu/internal/database/guilds"
)

// shortCode identifies a Reddit feed by a hash of its full identity (channel, subreddit, sort, author, title),
// short enough for a select option value and an autocomplete choice value. Two feeds with different identities
// get different codes however long their filters are.
func shortCode(f guilds.FeedEntry) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{f.ChannelID, f.Subreddit, f.PostType, f.Author, f.Title}, "\x00")))
	return hex.EncodeToString(sum[:8])
}
