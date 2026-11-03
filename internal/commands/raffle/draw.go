package raffle

import (
	"context"
	"math/rand"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	msgNoSuchRaffle  = "No such raffle."
	msgNobodyToDraw  = "There is nobody to draw in that raffle."
	msgRaffleEnded   = "This raffle has ended"
	raffleNameOption = "raffle"
)

// drawStore is what draw and remove need beyond raffleStore. SetRaffleWinner changes the winner field alone,
// so a draw never rewrites the record and cannot lose an entrant who joined meanwhile.
type drawStore interface {
	raffleStore
	SetRaffleWinner(ctx context.Context, guildID, raffleName, winnerID string) error
	DeleteRaffle(ctx context.Context, guildID, name string) error
}

// drawStoreOf picks the draw store of a database client; tests replace it.
var drawStoreOf = func(db *database.Client) drawStore { return db.Guilds() }

func drawSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name:       "draw",
		Desc:       "Pick a random entrant as the raffle's latest winner.",
		Visibility: interaction.Public,
		Response:   commands.Deferred,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: raffleNameOption, Description: "The name of the raffle to draw from.", Required: true, Autocomplete: true},
		},
		Run:                runDraw,
		AutocompleteOption: autocompleteRaffleName,
	}
}

func removeSubcommand() *commands.Subcommand {
	return &commands.Subcommand{
		Name:       "remove",
		Desc:       "Close a raffle and delete it.",
		Visibility: interaction.Private,
		Response:   commands.Deferred,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: raffleNameOption, Description: "The name of the raffle to remove.", Required: true, Autocomplete: true},
		},
		Run:                runRemove,
		AutocompleteOption: autocompleteRaffleName,
	}
}

// findNamedRaffle reads the raffle option and finds the raffle, replying privately when it cannot.
// Guard homes, moved from /pick-raffle-winner and /remove-raffle:
//   - "Please provide a raffle name." is msgNameRequired below;
//   - "No such raffle." is msgNoSuchRaffle below;
//   - "Could not load raffles." is Fail, which words a store error as "Something went wrong.";
//   - "This command can only be used in a server." is the group's ContextGuildOnly.
func findNamedRaffle(ic *interaction.Context, store raffleStore) (*guilds.Raffle, error) {
	opt := commands.ParseOption(ic.Command.Options, raffleNameOption)
	name := ""
	if opt != nil {
		name = strings.TrimSpace(opt.StringValue())
	}
	if name == "" {
		return nil, ic.Refuse(msgNameRequired)
	}
	raffles, err := store.GetRaffles(ic.Context(), ic.GuildID)
	if err != nil {
		return nil, ic.Fail(err, interaction.ErrorContext{})
	}
	raffle := findRaffleByName(raffles, name)
	if raffle == nil {
		return nil, ic.Refuse(msgNoSuchRaffle)
	}
	return raffle, nil
}

// winnerLine is the raffle message's winner text.
func winnerLine(r *guilds.Raffle) string {
	if r.WinnerID == "" {
		return msgNoWinnerYet
	}
	return "Latest winner: <@" + r.WinnerID + ">"
}

// closedMessage is the raffle message of a removed raffle: ended, the last winner kept, the buttons disabled.
func closedMessage(r *guilds.Raffle) interaction.Response {
	desc := msgRaffleEnded + "\nEntrants: " + strconv.Itoa(len(r.ParticipantIDs)) + "\n" + winnerLine(r)
	return interaction.Response{
		Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, r.Name, desc)},
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Enter", Style: discordgo.SuccessButton, CustomID: buttonPrefix + actionEnter, Disabled: true},
			discordgo.Button{Label: "Leave", Style: discordgo.SecondaryButton, CustomID: buttonPrefix + actionLeave, Disabled: true},
		}}},
	}
}

// runDraw announces a random entrant publicly, stores them as the latest winner and edits the raffle message if
// there is one. The raffle stays open.
// Guard home: "nobody to pick" lives here, as a private refusal before anything is stored or edited.
func runDraw(ic *interaction.Context) error {
	store := drawStoreOf(ic.DB)
	raffle, err := findNamedRaffle(ic, store)
	if raffle == nil {
		return err
	}
	if len(raffle.ParticipantIDs) == 0 {
		return ic.Refuse(msgNobodyToDraw)
	}
	winnerID := raffle.ParticipantIDs[rand.Intn(len(raffle.ParticipantIDs))]
	if err := store.SetRaffleWinner(ic.Context(), ic.GuildID, raffle.Name, winnerID); err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	raffle.WinnerID = winnerID
	if raffle.MessageID != "" {
		// The winner is stored and announced either way, so a failed edit is logged, not shown.
		if err := ic.EditMessage(raffle.ChannelID, raffle.MessageID, raffleMessage(raffle)); err != nil {
			text, _ := interaction.ExplainError(err, raffleErrorContext(raffle.ChannelID))
			logger.For("raffle").Error("raffle message edit failed", "guild_id", ic.GuildID, "channel_id", raffle.ChannelID, "reason", text, "err", err)
		}
	}
	return ic.EditReply(interaction.Response{Content: "**" + raffle.Name + "** winner is <@" + winnerID + ">! Congratulations!"})
}

// runRemove closes the raffle message, then deletes the raffle. A message that no longer exists does not block
// the removal; any other edit failure is explained and keeps the raffle, so the message is not left looking live.
func runRemove(ic *interaction.Context) error {
	store := drawStoreOf(ic.DB)
	raffle, err := findNamedRaffle(ic, store)
	if raffle == nil {
		return err
	}
	if raffle.MessageID != "" {
		if err := ic.EditMessage(raffle.ChannelID, raffle.MessageID, closedMessage(raffle)); err != nil && !discord.IsUnknownMessage(err) {
			return ic.Fail(err, raffleErrorContext(raffle.ChannelID))
		}
	}
	if err := store.DeleteRaffle(ic.Context(), ic.GuildID, raffle.Name); err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	return ic.EditReply(interaction.Response{Content: "Removed raffle **" + raffle.Name + "**."})
}
