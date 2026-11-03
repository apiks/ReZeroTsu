package raffle

import (
	"context"
	"sort"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"

	"github.com/bwmarrin/discordgo"
)

// autocompleteRaffleName offers the server's raffle names, matched anywhere in the name. It serves draw, remove
// and post.
func autocompleteRaffleName(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	if i.GuildID == "" {
		return nil
	}
	raffles, err := storeOf(db).GetRaffles(ctx, i.GuildID)
	if err != nil || len(raffles) == 0 {
		return nil
	}
	var names []string
	for _, r := range raffles {
		if r == nil || r.Name == "" {
			continue
		}
		names = append(names, r.Name)
	}
	sort.Strings(names)
	names = commands.MatchAnywhere(names, focusedValue, func(n string) []string { return []string{n} })
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(names))
	for i, name := range names {
		choices[i] = &discordgo.ApplicationCommandOptionChoice{Name: name, Value: name}
	}
	return choices
}

func findRaffleByName(raffles []*guilds.Raffle, name string) *guilds.Raffle {
	for _, r := range raffles {
		if r != nil && strings.EqualFold(r.Name, name) {
			return r
		}
	}
	return nil
}

func participantContains(ids []string, userID string) bool {
	for _, id := range ids {
		if id == userID {
			return true
		}
	}
	return false
}

// truncate cuts s to at most limit runes, ending in an ellipsis when it was cut.
func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// embedTitle is a raffle name cut to Discord's embed title limit. A legacy name can be longer, because the old
// /create-raffle had no length limit.
func embedTitle(name string) string { return truncate(name, discord.MaxEmbedTitleLength) }
