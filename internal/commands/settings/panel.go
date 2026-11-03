package settings

import (
	"context"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	// panelPrefix starts the custom ID of every component on the /settings panel and of its form. One handler
	// owns the prefix, so each component re-runs the moderator check in one place.
	panelPrefix = "settings:"
	idBotLog    = panelPrefix + "bot-log"
	idDonghua   = panelPrefix + "donghua"
	idPingEdit  = panelPrefix + "ping-edit"
	idPingForm  = panelPrefix + "ping-form"
	// idPingText is the custom ID of the text box inside the ping-message form.
	idPingText = "ping-message"

	embedTitleSettings = "Settings"
	labelPingMessage   = "Ping message (shown by /ping)"
	// maxPingMessageLength is Discord's limit for a text box; an embed description holds more.
	maxPingMessageLength = 4000
	// maxShownPingLength keeps the panel embed well inside its description limit.
	maxShownPingLength = 1500

	msgLoadFailed   = "Could not load the server's settings."
	msgSaveFailed   = "Could not save that change. The panel shows the stored settings."
	msgNoChannel    = "That channel was not found. Pick it again."
	msgBlankPing    = "The ping message can't be empty."
	msgStaleControl = "That control is out of date. Run /settings again."
)

// settingsStore is the part of the guild repository the panel uses; tests replace it.
type settingsStore interface {
	GetGuildSettings(ctx context.Context, guildID string) (*guilds.GuildSettings, error)
	SetGuildSettings(ctx context.Context, guildID string, settings *guilds.GuildSettings) error
}

// panelStoreOf picks the settings store of a database client; tests replace it.
var panelStoreOf = func(db *database.Client) settingsStore { return db.Guilds() }

func init() {
	commands.Add(&commands.Command{
		Name:       "settings",
		Desc:       "View and change this server's settings.",
		Visibility: commands.Private,
		Response:   commands.Deferred,
		// Guard home: the bot-side moderator check that /bot-log, /donghua and /ping-message ran, until spec 7
		// moves it to Discord's per-command permissions. Every component submission re-runs it in handlePanel.
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Run:        runSettings,
	})
	commands.AddComponent(panelPrefix, handlePanel)
}

func runSettings(ic *interaction.Context) error {
	gs, err := panelStoreOf(ic.DB).GetGuildSettings(ic.Context(), ic.GuildID)
	if err != nil || gs == nil {
		return failedLoad(ic, err)
	}
	return ic.EditReply(panelResponse(gs))
}

func failedLoad(ic *interaction.Context, err error) error {
	if err != nil {
		logger.For("settings").Error("settings load failed", "guild_id", ic.GuildID, "err", err)
	}
	return ic.Refuse(msgLoadFailed)
}

func pingMessageOf(gs *guilds.GuildSettings) string {
	if gs.PingMessage == "" {
		return guilds.DefaultGuildSettings().PingMessage
	}
	return gs.PingMessage
}

func botLogChannelID(gs *guilds.GuildSettings) string {
	if gs.BotLogID == nil {
		return ""
	}
	return gs.BotLogID.ID
}

// panelResponse is the whole panel for the given stored settings: the summary embed and the component rows.
func panelResponse(gs *guilds.GuildSettings) interaction.Response {
	botLog := "Off"
	if id := botLogChannelID(gs); id != "" {
		botLog = commands.ChannelMention(id)
	}
	donghua := "Hidden"
	if gs.Donghua {
		donghua = "Shown"
	}
	ping := []rune(pingMessageOf(gs))
	if len(ping) > maxShownPingLength {
		ping = append(ping[:maxShownPingLength], '…')
	}
	desc := "**Bot log channel:** " + botLog +
		"\n**Donghua in the schedule:** " + donghua +
		"\n\n**" + labelPingMessage + "**\n" + string(ping)
	return interaction.Response{
		Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleSettings, desc)},
		Components: []discordgo.MessageComponent{
			botLogRow(gs),
			// Rows 2 to 4 (new-episodes channel, new-episodes role, daily-schedule channel) are added by the
			// autopost settings ticket.
			toggleRow(gs),
		},
	}
}

// botLogRow is row 1: the bot-log channel picker, carrying the stored channel as its default value.
func botLogRow(gs *guilds.GuildSettings) discordgo.MessageComponent {
	none := 0
	menu := discordgo.SelectMenu{
		MenuType:     discordgo.ChannelSelectMenu,
		CustomID:     idBotLog,
		Placeholder:  "Bot log channel (clear it to turn the bot log off)",
		MinValues:    &none,
		MaxValues:    1,
		ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
	}
	if id := botLogChannelID(gs); id != "" {
		menu.DefaultValues = []discordgo.SelectMenuDefaultValue{{ID: id, Type: discordgo.SelectMenuDefaultValueChannel}}
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}
}

// toggleRow is row 5: the donghua toggle labelled with its state, and the Edit ping message button.
func toggleRow(gs *guilds.GuildSettings) discordgo.MessageComponent {
	donghua := discordgo.Button{Label: "Donghua: Off", Style: discordgo.SecondaryButton, CustomID: idDonghua}
	if gs.Donghua {
		donghua.Label, donghua.Style = "Donghua: On", discordgo.SuccessButton
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		donghua,
		discordgo.Button{Label: "Edit ping message", Style: discordgo.SecondaryButton, CustomID: idPingEdit},
	}}
}

// handlePanel handles every panel component and the ping-message form.
// Guard homes, moved from /bot-log, /donghua and /ping-message:
//   - the bot-side moderator check runs first, on every press, choice and submission, before the store is read
//     (a lookup that does not finish refuses, see commands.ModeratorRefusal);
//   - the guild-only context is part of that check, which refuses an interaction with no server;
//   - the "channel not found" refusal of /bot-log is bot-log choice's resolved-channel check;
//   - the "could not load" and "could not save" answers are failedLoad and apply.
func handlePanel(ic *interaction.Context, _ *discordgo.Session) error {
	store := panelStoreOf(ic.DB)
	if msg := commands.ModeratorRefusal(ic, store); msg != "" {
		return ic.Refuse(msg)
	}
	switch ic.CustomID {
	case idBotLog:
		return apply(ic, store, chooseBotLog)
	case idDonghua:
		return apply(ic, store, func(_ *interaction.Context, gs *guilds.GuildSettings) string {
			gs.Donghua = !gs.Donghua
			return ""
		})
	case idPingEdit:
		return openPingForm(ic, store)
	case idPingForm:
		return apply(ic, store, savePingMessage)
	}
	return ic.Refuse(msgStaleControl)
}

// chooseBotLog sets the bot-log channel to the one chosen, or clears it when the picker was cleared.
func chooseBotLog(ic *interaction.Context, gs *guilds.GuildSettings) string {
	if len(ic.Values) == 0 {
		gs.BotLogID = nil
		return ""
	}
	id := ic.Values[0]
	var ch *discordgo.Channel
	if data, ok := ic.Interaction.Data.(discordgo.MessageComponentInteractionData); ok {
		ch = data.Resolved.Channels[id]
	}
	if ch == nil {
		return msgNoChannel
	}
	gs.BotLogID = &guilds.ChannelRef{Name: ch.Name, ID: ch.ID}
	return ""
}

func savePingMessage(ic *interaction.Context, gs *guilds.GuildSettings) string {
	text := ic.FormValue(idPingText)
	if strings.TrimSpace(text) == "" {
		return msgBlankPing
	}
	gs.PingMessage = text
	return ""
}

func openPingForm(ic *interaction.Context, store settingsStore) error {
	gs, err := store.GetGuildSettings(ic.Context(), ic.GuildID)
	if err != nil || gs == nil {
		return failedLoad(ic, err)
	}
	box := interaction.TextBox(idPingText, labelPingMessage, discordgo.TextInputParagraph, true)
	input := box.Component.(discordgo.TextInput)
	input.Value, input.MaxLength = pingMessageOf(gs), maxPingMessageLength
	box.Component = input
	return ic.OpenForm(interaction.Form{
		CustomID:   idPingForm,
		Title:      "Edit ping message",
		Components: []discordgo.MessageComponent{box},
	})
}

// apply reads the stored settings, lets change set exactly one of them on a copy, saves, and updates the panel
// in place from freshly read settings. When change refuses or the save fails, the panel is put back to the
// stored values (a picker keeps showing the choice otherwise) and the reason follows privately.
func apply(ic *interaction.Context, store settingsStore, change func(*interaction.Context, *guilds.GuildSettings) string) error {
	ctx := ic.Context()
	stored, err := store.GetGuildSettings(ctx, ic.GuildID)
	if err != nil || stored == nil {
		return failedLoad(ic, err)
	}
	updated := *stored
	if refusal := change(ic, &updated); refusal != "" {
		return restore(ic, stored, refusal)
	}
	if err := store.SetGuildSettings(ctx, ic.GuildID, &updated); err != nil {
		logger.For("settings").Error("settings save failed", "guild_id", ic.GuildID, "custom_id", ic.CustomID, "err", err)
		return restore(ic, stored, msgSaveFailed)
	}
	fresh, err := store.GetGuildSettings(ctx, ic.GuildID)
	if err != nil || fresh == nil {
		// The change is saved; show what was written rather than leaving the panel stale.
		fresh = &updated
	}
	return ic.UpdateMessage(panelResponse(fresh))
}

// restore shows the stored settings on the panel and then the reason, which only the invoker sees.
func restore(ic *interaction.Context, stored *guilds.GuildSettings, reason string) error {
	if err := ic.UpdateMessage(panelResponse(stored)); err != nil {
		return err
	}
	return ic.Refuse(reason)
}
