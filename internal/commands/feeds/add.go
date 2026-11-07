package feeds

import (
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/reddit"

	"github.com/bwmarrin/discordgo"
)

// REMOVE on 2026-11-27 with the rest of the Reddit retirement code.
const (
	msgAddRefusedBeforeCutoff = "Reddit feeds can't be added any more. Reddit shuts down its RSS feeds on 13 November 2026, and existing feeds stop that day."
	msgAddRefusedAfterCutoff  = "Reddit feeds were retired on 13 November 2026, when Reddit shut down its RSS feeds."
)

// addSubcommand keeps every option of the retired /reddit add, so the command's shape does not change.
func addSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name: "add",
		Desc: "Retired: Reddit feeds can't be added any more.",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionChannel, Name: optChannel, Description: "The channel to add the feed to.", Required: true, ChannelTypes: feedChannelTypes},
			{Type: discordgo.ApplicationCommandOptionString, Name: optSubreddit, Description: "The subreddit (e.g. r/anime or anime).", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: optSort, Description: "Which listing to follow. Default: Hot.", Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "Hot", Value: "hot"}, {Name: "Rising", Value: "rising"}, {Name: "New", Value: "new"},
			}},
			{Type: discordgo.ApplicationCommandOptionString, Name: optAuthor, Description: "Filter to posts from this user (optional)."},
			{Type: discordgo.ApplicationCommandOptionString, Name: optTitle, Description: "Filter to posts starting with this title (optional)."},
			{Type: discordgo.ApplicationCommandOptionBoolean, Name: optPin, Description: "Auto-pin the latest post. Default: false."},
		},
		Run: runAdd,
	}
}

// runAdd refuses every call before options, permissions, the store or Reddit are touched.
// Guard homes, for what the retired command checked: none is needed, because nothing is saved or changed any more.
// The server-only check is still the command's ContextGuildOnly.
func runAdd(ic *interaction.Context) error {
	if reddit.Retired() {
		return ic.Refuse(msgAddRefusedAfterCutoff)
	}
	return ic.Refuse(msgAddRefusedBeforeCutoff)
}
