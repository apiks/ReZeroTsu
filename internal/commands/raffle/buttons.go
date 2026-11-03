package raffle

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	// buttonPrefix starts the custom ID of the raffle message's buttons: raffle:<action>. The raffle is found by
	// the clicked message's ID, so no name or raffle ID travels in the custom ID.
	buttonPrefix = "raffle:"
	actionEnter  = "enter"
	actionLeave  = "leave"

	maxRaffleNameLength = 100

	msgRaffleGone    = "That raffle no longer exists."
	msgNoWinnerYet   = "No winner yet"
	msgNameRequired  = "Please provide a raffle name."
	msgNameTaken     = "A raffle with this name already exists."
	msgGuildOnlyEdit = "Raffles only work in a server."
)

// postNeeds is what the bot needs in a channel to post or edit a raffle message, worded in permission errors.
const postNeeds = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks

// raffleStore is the part of the guild repository the button raffle commands use. Entrants change through the
// single-statement UpdateRaffleParticipant only, so a press never rewrites the whole record.
type raffleStore interface {
	GetRaffles(ctx context.Context, guildID string) ([]*guilds.Raffle, error)
	SetRaffle(ctx context.Context, guildID string, raffle *guilds.Raffle) error
	// UpdateRaffleParticipant reports whether a raffle of that name matched, false when it was deleted meanwhile.
	UpdateRaffleParticipant(ctx context.Context, guildID, raffleName, userID string, remove bool) (bool, error)
}

// storeOf picks the raffle store of a database client; tests replace it.
var storeOf = func(db *database.Client) raffleStore { return db.Guilds() }

func init() {
	commands.Add(&commands.Command{
		Name:       "raffle",
		Desc:       "Create and manage raffles.",
		Visibility: commands.Private,
		Response:   commands.Deferred,
		// Guard home: the bot-side moderator check, until spec 7 moves it to Discord's per-command permissions.
		// The Enter and Leave buttons are not commands, so any member can press them.
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "raffles",
		Subcommands: []*commands.Subcommand{
			{
				Name: "create",
				Desc: "Post a raffle message that members enter with buttons.",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: raffleNameOption, Description: "The name of the raffle.", Required: true, MaxLength: maxRaffleNameLength},
				},
				Run: runCreate,
			},
			drawSubcommand(), removeSubcommand(),
			listSubcommand(),
			postSubcommand(),
		},
	})
	commands.AddComponent(buttonPrefix, handleButton)
}

// messageLink is the URL of a message in a server.
func messageLink(guildID, channelID, messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}

// raffleMessage is the raffle message: the name, the entrant count, the latest winner and the Enter and Leave
// buttons.
func raffleMessage(r *guilds.Raffle) interaction.Response {
	desc := "Entrants: " + strconv.Itoa(len(r.ParticipantIDs)) + "\n" + winnerLine(r)
	return interaction.Response{
		Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitle(r.Name), desc)},
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Enter", Style: discordgo.SuccessButton, CustomID: buttonPrefix + actionEnter},
			discordgo.Button{Label: "Leave", Style: discordgo.SecondaryButton, CustomID: buttonPrefix + actionLeave},
		}}},
	}
}

func raffleErrorContext(channelID string) interaction.ErrorContext {
	return interaction.ErrorContext{ChannelID: channelID, Needs: postNeeds}
}

// runCreate posts the raffle message as the bot's own channel message, so the bot can edit it later, stores the
// raffle with the message's IDs and replies privately with a link.
// Guard home: the duplicate-name refusal that /create-raffle ran lives here, before anything is posted.
func runCreate(ic *interaction.Context) error {
	opt := commands.ParseOption(ic.Command.Options, raffleNameOption)
	name := ""
	if opt != nil {
		name = strings.TrimSpace(opt.StringValue())
	}
	if name == "" {
		return ic.Refuse(msgNameRequired)
	}
	store := storeOf(ic.DB)
	existing, err := store.GetRaffles(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	if findRaffleByName(existing, name) != nil {
		return ic.Refuse(msgNameTaken)
	}

	raffle := &guilds.Raffle{Name: name, ChannelID: ic.ChannelID}
	msg, err := ic.PostMessage(ic.ChannelID, raffleMessage(raffle))
	if err != nil {
		return ic.Fail(err, raffleErrorContext(ic.ChannelID))
	}
	raffle.ChannelID, raffle.MessageID = msg.ChannelID, msg.ID
	if err := store.SetRaffle(ic.Context(), ic.GuildID, raffle); err != nil {
		// Without a record the buttons would dead-end, so take the message back down.
		if delErr := ic.DeleteMessage(msg.ChannelID, msg.ID); delErr != nil {
			logger.For("raffle").Error("raffle message cleanup failed", "guild_id", ic.GuildID, "channel_id", msg.ChannelID, "err", delErr)
		}
		return ic.Fail(err, interaction.ErrorContext{})
	}
	return ic.EditReply(interaction.Response{Content: "Raffle **" + name + "** is posted: " + messageLink(ic.GuildID, msg.ChannelID, msg.ID)})
}

// handleButton enters or removes the member who pressed Enter or Leave on a raffle message.
// Guard homes, moved from /join-raffle and /leave-raffle:
//   - the raffle lookup ("No such raffle") is the message-ID lookup below;
//   - the already-joined and not-joined checks are the two state checks below, run before the store changes;
//   - the entrant change is still the single-statement UpdateRaffleParticipant, whose match count catches a raffle
//     removed between the lookup and the update;
//   - the member checked is always the presser (ic.Invoker), never an ID read from the custom ID.
func handleButton(ic *interaction.Context, _ *discordgo.Session) error {
	action := strings.TrimPrefix(ic.CustomID, buttonPrefix)
	if (action != actionEnter && action != actionLeave) || ic.Interaction.Message == nil {
		return ic.Refuse(msgRaffleGone)
	}
	if ic.GuildID == "" || ic.Invoker == nil {
		return ic.Refuse(msgGuildOnlyEdit)
	}
	store := storeOf(ic.DB)
	raffles, err := store.GetRaffles(ic.Context(), ic.GuildID)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	raffle := findRaffleByMessage(raffles, ic.Interaction.Message.ID)
	if raffle == nil {
		return ic.Refuse(msgRaffleGone)
	}

	entered := participantContains(raffle.ParticipantIDs, ic.Invoker.ID)
	leaving := action == actionLeave
	switch {
	case !leaving && entered:
		return ic.Reply(interaction.Response{Content: "You're already entered in " + raffle.Name})
	case leaving && !entered:
		return ic.Reply(interaction.Response{Content: "You're not entered in " + raffle.Name})
	}
	matched, err := store.UpdateRaffleParticipant(ic.Context(), ic.GuildID, raffle.Name, ic.Invoker.ID, leaving)
	if err != nil {
		return ic.Fail(err, interaction.ErrorContext{})
	}
	if !matched {
		return ic.Refuse(msgRaffleGone)
	}
	confirmation := "You entered " + raffle.Name
	if leaving {
		confirmation = "You left " + raffle.Name
	}
	if err := ic.Reply(interaction.Response{Content: confirmation}); err != nil {
		return err
	}
	refreshMessage(ic, store, raffle)
	return nil
}

// refreshMessage re-reads the raffle just before editing and edits its message to show the current count. A
// raffle removed meanwhile is skipped, so a closed message is not put back to live buttons. A failure is logged
// and does not fail the member's confirmation, because the entry itself was saved.
func refreshMessage(ic *interaction.Context, store raffleStore, raffle *guilds.Raffle) {
	raffles, err := store.GetRaffles(ic.Context(), ic.GuildID)
	if err != nil {
		logger.For("raffle").Error("raffle count reload failed", "guild_id", ic.GuildID, "err", err)
		return
	}
	current := findRaffleByMessage(raffles, raffle.MessageID)
	if current == nil {
		return
	}
	if err := ic.EditMessage(current.ChannelID, current.MessageID, raffleMessage(current)); err != nil {
		text, _ := interaction.ExplainError(err, raffleErrorContext(current.ChannelID))
		logger.For("raffle").Error("raffle message edit failed", "guild_id", ic.GuildID, "channel_id", current.ChannelID, "reason", text, "err", err)
	}
}

func findRaffleByMessage(raffles []*guilds.Raffle, messageID string) *guilds.Raffle {
	if messageID == "" {
		return nil
	}
	for _, r := range raffles {
		if r != nil && r.MessageID == messageID {
			return r
		}
	}
	return nil
}
