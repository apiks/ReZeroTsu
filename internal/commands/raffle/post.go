package raffle

import (
	"context"
	"sort"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const msgNoSuchRaffle = "No such raffle."

// postStore is the part of the guild repository /raffle post uses. SetRaffleMessage changes only the button
// message's IDs, so entrants, the latest winner and a legacy reaction message are never rewritten.
type postStore interface {
	GetRaffles(ctx context.Context, guildID string) ([]*guilds.Raffle, error)
	SetRaffleMessage(ctx context.Context, guildID, raffleName, channelID, messageID string) error
}

// postStoreOf picks the store of a database client; tests replace it.
var postStoreOf = func(db *database.Client) postStore { return db.Guilds() }

func postSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name: "post",
		Desc: "Post a raffle message in this channel, moving an earlier one here.",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "raffle", Description: "The raffle to post.", Required: true, Autocomplete: true},
		},
		Run:                runPost,
		AutocompleteOption: autocompletePost,
	}
}

// autocompletePost offers the server's raffle names, matched anywhere in the name.
func autocompletePost(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, _, typed string) []*discordgo.ApplicationCommandOptionChoice {
	if i.GuildID == "" {
		return nil
	}
	raffles, err := postStoreOf(db).GetRaffles(ctx, i.GuildID)
	if err != nil {
		logger.For("raffle").Error("raffle autocomplete failed", "guild_id", i.GuildID, "err", err)
		return nil
	}
	var names []string
	for _, r := range raffles {
		if r != nil && r.Name != "" {
			names = append(names, r.Name)
		}
	}
	sort.Strings(names)
	names = commands.MatchAnywhere(names, typed, func(n string) []string { return []string{n} })
	choices := make([]*discordgo.ApplicationCommandOptionChoice, len(names))
	for j, name := range names {
		choices[j] = &discordgo.ApplicationCommandOptionChoice{Name: clip(name), Value: clip(name)}
	}
	return choices
}

// movedMessage is the earlier raffle message after /raffle post put the raffle elsewhere: the link to the new
// message and the buttons disabled.
func movedMessage(r *guilds.Raffle, link string) interaction.Response {
	desc := "This raffle moved: " + link + "\n" + winnerLine(r)
	return interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, r.Name, desc)},
		Components: []discordgo.MessageComponent{disabledButtons()},
	}
}

// runPost posts a new raffle message in the current channel for any raffle, stores it as the raffle's button
// message, and marks the earlier button message moved. Entrants are not touched, and a legacy reaction raffle keeps
// its reaction message: only the button message's IDs change.
// Guard home: the "No such raffle" refusal that /remove-raffle and /pick-raffle-winner ran lives here, before
// anything is posted. The old message is edited only after the new one is posted and stored, so a failure
// before that leaves the old buttons working.
func runPost(ic *interaction.Context) error {
	opt := commands.ParseOption(ic.Command.Options, "raffle")
	name := ""
	if opt != nil {
		name = strings.TrimSpace(opt.StringValue())
	}
	if name == "" {
		return ic.Refuse(msgNameRequired)
	}
	store := postStoreOf(ic.DB)
	raffles, err := store.GetRaffles(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	raffle := findRaffleByName(raffles, name)
	if raffle == nil {
		return ic.Refuse(msgNoSuchRaffle)
	}

	msg, err := ic.PostMessage(ic.ChannelID, raffleMessage(raffle))
	if err != nil {
		return ic.Fail(err, raffleErrorContext(ic.ChannelID))
	}
	if err := store.SetRaffleMessage(ic.Context(), ic.GuildID, raffle.Name, msg.ChannelID, msg.ID); err != nil {
		// Without the new IDs its buttons would dead-end, so take the message back down.
		if delErr := ic.DeleteMessage(msg.ChannelID, msg.ID); delErr != nil {
			logger.For("raffle").Error("raffle message cleanup failed", "guild_id", ic.GuildID, "channel_id", msg.ChannelID, "err", delErr)
		}
		return ic.Fail(err, interaction.ErrorContext{})
	}
	link := messageLink(ic.GuildID, msg.ChannelID, msg.ID)
	if raffle.MessageID != "" {
		if err := ic.EditMessage(raffle.ChannelID, raffle.MessageID, movedMessage(raffle, link)); err != nil {
			reason, _ := interaction.ExplainError(err, raffleErrorContext(raffle.ChannelID))
			logger.For("raffle").Error("old raffle message edit failed", "guild_id", ic.GuildID, "channel_id", raffle.ChannelID, "reason", reason, "err", err)
		}
	}
	return ic.EditReply(interaction.Response{Content: "Raffle **" + raffle.Name + "** is posted: " + link})
}
