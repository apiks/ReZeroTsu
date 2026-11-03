package settings

import (
	"context"
	"fmt"
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
	embedTitleVoiceRoles = "Voice Roles"

	// voicePageKey names the voice role list's page renderer.
	voicePageKey = "voicerole"
	// voicePickerPrefix starts the custom ID of the Remove picker: voicerole-rm:<authorID>:<page>.
	voicePickerPrefix = "voicerole-rm:"
	voiceMaxChoice    = 100

	msgVoiceNone         = "No voice channel roles are set."
	msgVoiceLoadFailed   = "Could not load the voice channel roles."
	msgVoiceOnlyOwner    = "Only the moderator who ran /voicerole list can remove from it."
	msgVoiceStalePicker  = "Could not read that choice. Run /voicerole list again."
	msgVoiceBothRequired = "Please provide both channel and role."
	msgVoiceNotFound     = "Channel or role not found."
)

// voiceStore is the part of the guild repository the voice role commands use.
type voiceStore interface {
	GetGuildSettings(ctx context.Context, guildID string) (*guilds.GuildSettings, error)
	// SetVoiceChas writes the voice channel list alone; other settings, stored or not, stay as they are.
	SetVoiceChas(ctx context.Context, guildID string, chas []guilds.VoiceCha) error
}

// voiceStoreOf picks the store of a database client; tests replace it.
var voiceStoreOf = func(db *database.Client) voiceStore { return db.Guilds() }

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Private,
		Response:   commands.Deferred,
		Name:       "voicerole",
		Desc:       "Give a role to members while they are in a voice channel.",
		// Guard home: the moderator check is Discord's default member permissions (Manage Server) and server overrides.
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "settings",
		Subcommands: []*commands.Subcommand{
			{
				Name: "add",
				Desc: "Set a voice channel to give a role when users join and remove it when they leave.",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionChannel,
						Name:         "channel",
						Description:  "The voice channel.",
						Required:     true,
						ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice},
					},
					{
						Type:        discordgo.ApplicationCommandOptionRole,
						Name:        "role",
						Description: "The role to give when joining the channel.",
						Required:    true,
					},
				},
				Run: runVoiceAdd,
			},
			{
				Name: "remove",
				Desc: "Remove a voice channel role, or remove a specific role from a channel.",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionChannel,
						Name:         "channel",
						Description:  "The voice channel to remove.",
						Required:     true,
						ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice},
					},
					{
						Type:        discordgo.ApplicationCommandOptionRole,
						Name:        "role",
						Description: "If provided, remove only this role from the channel.",
						Required:    false,
					},
				},
				Run: runVoiceRemove,
			},
			{
				Name: "list",
				Desc: "List all voice channels and their associated roles.",
				Run:  runVoiceList,
			},
		},
	})
	// Restricted: the list and its picker belong to the moderator who ran it.
	commands.RegisterPaginationRenderer(voicePageKey, RenderVoiceRolePage)
	commands.AddComponent(voicePickerPrefix, handleVoiceRemovePicker)
}

func voiceFail(ic *interaction.Context, err error) error {
	return ic.Fail(err, interaction.ErrorContext{})
}

func voiceReply(ic *interaction.Context, desc string, hint ...string) error {
	return ic.EditReply(interaction.Response{Embeds: []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleVoiceRoles, desc, hint...)}})
}

// voiceIDOption is the snowflake the member chose for an option, or "" when it is missing.
func voiceIDOption(ic *interaction.Context, name string) string {
	o := commands.ParseOption(ic.Command.Options, name)
	if o == nil {
		return ""
	}
	id, _ := o.Value.(string)
	return id
}

func voiceHintRemove() string {
	return "Use " + commands.CommandMention("voicerole", "remove") + " to remove."
}

func voiceHintListAdd() string {
	return "Use " + commands.CommandMention("voicerole", "list") + " to list all, " + commands.CommandMention("voicerole", "add") + " to add."
}

// runVoiceAdd is /voicerole add (was /add-voice). Guard homes: the moderator check and the server-only check are the command's
// PermMod and ContextGuildOnly; both options present, resolvable and a voice channel, the duplicate-pair refusal,
// the merge into an existing channel entry and the load and save failures are below. New: commands.CanHandOutRole
// refuses a role at or above the moderator's own highest role.
func runVoiceAdd(ic *interaction.Context) error {
	chID, roleID := voiceIDOption(ic, "channel"), voiceIDOption(ic, "role")
	if chID == "" || roleID == "" {
		return ic.Refuse(msgVoiceBothRequired)
	}
	var ch *discordgo.Channel
	var role *discordgo.Role
	if res := ic.Command.Resolved; res != nil {
		ch, role = res.Channels[chID], res.Roles[roleID]
	}
	if ch == nil || role == nil {
		return ic.Refuse(msgVoiceNotFound)
	}
	if ch.Type != discordgo.ChannelTypeGuildVoice {
		return ic.Refuse("That channel is not a voice channel.")
	}
	roles, err := ic.Roles(ic.GuildID)
	if err != nil {
		return voiceFail(ic, err)
	}
	if !commands.CanHandOutRole(ic, roles, role) {
		return ic.Refuse(commands.RoleAboveInvoker(role.ID, "a voice role"))
	}
	store := voiceStoreOf(ic.DB)
	settings, err := store.GetGuildSettings(ic.Context(), ic.GuildID)
	if err != nil || settings == nil {
		return voiceFail(ic, voiceLoadErr(err))
	}

	newRole := guilds.Role{Name: role.Name, ID: role.ID, Position: role.Position}
	merged := false
	for j, vc := range settings.VoiceChas {
		if vc.ID != ch.ID {
			continue
		}
		for _, r := range vc.Roles {
			if r.ID == role.ID {
				return ic.Refuse(fmt.Sprintf("%s is already set for %s.", commands.RoleMention(role.ID), commands.ChannelMention(ch.ID)))
			}
		}
		settings.VoiceChas[j].Roles = append(settings.VoiceChas[j].Roles, newRole)
		merged = true
		break
	}
	if !merged {
		settings.VoiceChas = append(settings.VoiceChas, guilds.VoiceCha{Name: ch.Name, ID: ch.ID, Roles: []guilds.Role{newRole}})
	}
	if err := store.SetVoiceChas(ic.Context(), ic.GuildID, settings.VoiceChas); err != nil {
		return voiceFail(ic, err)
	}
	return voiceReply(ic, fmt.Sprintf("**Channel:** %s\n**Role:** %s\n\nUsers will get this role when they join the channel.",
		commands.ChannelMention(ch.ID), commands.RoleMention(role.ID)), voiceHintRemove())
}

func voiceLoadErr(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("guild settings missing")
}

// runVoiceRemove is /voicerole remove (was /remove-voice). Guard homes: the moderator and server-only checks are the command's PermMod
// and ContextGuildOnly; the channel-given check, the not-in-the-list refusal, the role-not-set refusal, dropping
// a channel that has no roles left and the load and save failures are below. Without the role option the whole
// channel entry goes, as before. A role chosen but not resolvable no longer removes the whole channel as the old
// command did: the ID the member chose is what is looked up.
func runVoiceRemove(ic *interaction.Context) error {
	chID, roleID := voiceIDOption(ic, "channel"), voiceIDOption(ic, "role")
	if chID == "" {
		return ic.Refuse("Please provide a channel.")
	}
	store := voiceStoreOf(ic.DB)
	settings, err := store.GetGuildSettings(ic.Context(), ic.GuildID)
	if err != nil || settings == nil {
		return voiceFail(ic, voiceLoadErr(err))
	}
	channelIdx := -1
	for j, vc := range settings.VoiceChas {
		if vc.ID == chID {
			channelIdx = j
			break
		}
	}
	if channelIdx < 0 {
		return ic.Refuse(fmt.Sprintf("%s is not in the voice roles list.", commands.ChannelMention(chID)))
	}
	if roleID != "" {
		roles := settings.VoiceChas[channelIdx].Roles
		roleIdx := -1
		for k, r := range roles {
			if r.ID == roleID {
				roleIdx = k
				break
			}
		}
		if roleIdx < 0 {
			return ic.Refuse("That role is not set for this channel.")
		}
		settings.VoiceChas[channelIdx].Roles = append(roles[:roleIdx], roles[roleIdx+1:]...)
		if len(settings.VoiceChas[channelIdx].Roles) == 0 {
			settings.VoiceChas = append(settings.VoiceChas[:channelIdx], settings.VoiceChas[channelIdx+1:]...)
		}
	} else {
		settings.VoiceChas = append(settings.VoiceChas[:channelIdx], settings.VoiceChas[channelIdx+1:]...)
	}
	if err := store.SetVoiceChas(ic.Context(), ic.GuildID, settings.VoiceChas); err != nil {
		return voiceFail(ic, err)
	}
	if roleID != "" {
		return voiceReply(ic, fmt.Sprintf("Role removed from %s.", commands.ChannelMention(chID)), voiceHintListAdd())
	}
	return voiceReply(ic, fmt.Sprintf("All roles removed from %s.", commands.ChannelMention(chID)), voiceHintListAdd())
}

// runVoiceList is /voicerole list (was /voice-roles). Guard homes: the moderator and server-only checks are the command's PermMod and
// ContextGuildOnly; the empty notice and the load failure are below.
func runVoiceList(ic *interaction.Context) error {
	settings, err := voiceStoreOf(ic.DB).GetGuildSettings(ic.Context(), ic.GuildID)
	if err != nil || settings == nil {
		return voiceFail(ic, voiceLoadErr(err))
	}
	entries := voiceEntries(settings)
	if len(entries) == 0 {
		return voiceReply(ic, msgVoiceNone, "Use "+commands.CommandMention("voicerole", "add")+" to assign a role to a channel.")
	}
	return ic.EditReply(voicePage(entries, 0, ic.Invoker.ID))
}

// voiceEntry is one line of the list: a voice channel and one of its roles, or a channel with none.
type voiceEntry struct {
	channel guilds.VoiceCha
	role    *guilds.Role
}

func voiceEntries(settings *guilds.GuildSettings) []voiceEntry {
	var out []voiceEntry
	for _, vc := range settings.VoiceChas {
		if len(vc.Roles) == 0 {
			out = append(out, voiceEntry{channel: vc})
			continue
		}
		for i := range vc.Roles {
			out = append(out, voiceEntry{channel: vc, role: &vc.Roles[i]})
		}
	}
	return out
}

func voiceClip(s string) string {
	r := []rune(s)
	if len(r) <= voiceMaxChoice {
		return s
	}
	return string(r[:voiceMaxChoice])
}

// voicePairValue is a picker value: the channel and role IDs.
func voicePairValue(e voiceEntry) string { return e.channel.ID + ":" + e.role.ID }

func voiceMentionOr(mention, name string) string {
	if mention != "" {
		return mention
	}
	return "**" + name + "**"
}

func (e voiceEntry) line() string {
	ch := voiceMentionOr(commands.ChannelMention(e.channel.ID), e.channel.Name)
	if e.role == nil {
		return ch + ": no roles"
	}
	return ch + " gives " + voiceMentionOr(commands.RoleMention(e.role.ID), e.role.Name)
}

// voicePage renders one page (0-based, clamped) of a non-empty list: the embed with mentions, the paging buttons
// when there is more than one page, and the Remove picker for the page's channel and role pairs.
func voicePage(entries []voiceEntry, page int, authorID string) interaction.Response {
	n := len(entries)
	totalPages := max((n+discord.MaxEmbedFields-1)/discord.MaxEmbedFields, 1)
	page = min(max(page, 0), totalPages-1)
	start := page * discord.MaxEmbedFields
	end := min(start+discord.MaxEmbedFields, n)

	var lines []string
	var options []discordgo.SelectMenuOption
	for _, e := range entries[start:end] {
		lines = append(lines, e.line())
		if e.role == nil || e.channel.ID == "" || e.role.ID == "" {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: voiceClip(e.channel.Name + ": " + e.role.Name),
			Value: voiceClip(voicePairValue(e)),
		})
	}
	desc := "Channels and the roles given while members are in them.\n\n" + strings.Join(lines, "\n")
	var components []discordgo.MessageComponent
	if n > discord.MaxEmbedFields {
		desc = fmt.Sprintf("Page %d of %d. Showing %d-%d of %d.\n\n", page+1, totalPages, start+1, end, n) + desc
		components = append(components, commands.PaginationComponents(voicePageKey, page, n, authorID)...)
	}
	if len(options) > 0 {
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    voicePickerPrefix + authorID + ":" + strconv.Itoa(page),
			Placeholder: "Remove a channel and role",
			Options:     options,
		}}})
	}
	emb := commands.NewEmbed(nil, embedTitleVoiceRoles, desc, "Pick a channel and role below to remove it.")
	return interaction.Response{Embeds: []*discordgo.MessageEmbed{emb}, Components: components}
}

// RenderVoiceRolePage renders one page of the voice role list for pagination. Guard homes: the load failure and
// empty notices are below; the moderator-only reach is the restricted renderer (only the author pages it).
func RenderVoiceRolePage(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, page, _ int, authorID string) commands.Page {
	if i.GuildID == "" {
		return commands.Page{Refusal: msgVoiceLoadFailed}
	}
	settings, err := voiceStoreOf(db).GetGuildSettings(ctx, i.GuildID)
	if err != nil || settings == nil {
		logger.For("settings").Error("voice role page failed", "guild_id", i.GuildID, "err", err)
		return commands.Page{Refusal: msgVoiceLoadFailed}
	}
	entries := voiceEntries(settings)
	if len(entries) == 0 {
		return commands.Page{Refusal: msgVoiceNone + " Run /voicerole list again."}
	}
	return commands.Page{Response: voicePage(entries, page, authorID)}
}

// handleVoiceRemovePicker removes the chosen channel and role pair, as /voicerole remove with both options does, and
// re-renders the page. Guard home: only the moderator whose list it is may remove from it, checked before the
// store is read. A choice already gone just re-renders; a choice that more than one pair clips to removes nothing.
func handleVoiceRemovePicker(ic *interaction.Context, _ *discordgo.Session) error {
	parts := strings.SplitN(strings.TrimPrefix(ic.CustomID, voicePickerPrefix), ":", 2)
	if len(parts) != 2 || len(ic.Values) != 1 || ic.GuildID == "" || ic.Invoker == nil {
		return ic.Refuse(msgVoiceStalePicker)
	}
	authorID := parts[0]
	page, err := strconv.Atoi(parts[1])
	if err != nil {
		return ic.Refuse(msgVoiceStalePicker)
	}
	if ic.Invoker.ID != authorID {
		return ic.Refuse(msgVoiceOnlyOwner)
	}

	store := voiceStoreOf(ic.DB)
	settings, err := store.GetGuildSettings(ic.Context(), ic.GuildID)
	if err != nil || settings == nil {
		return voicePickerFailed(ic, msgVoiceLoadFailed, voiceLoadErr(err))
	}
	type pos struct{ ch, role int }
	var matches []pos
	for ci, vc := range settings.VoiceChas {
		for ri, r := range vc.Roles {
			if voiceClip(vc.ID+":"+r.ID) == ic.Values[0] {
				matches = append(matches, pos{ci, ri})
			}
		}
	}
	if len(matches) > 1 {
		return ic.Refuse("More than one channel and role pair has that choice as far as the list can show it, so I removed nothing. Use " +
			commands.CommandMention("voicerole", "remove") + " with the channel and role instead.")
	}
	if len(matches) == 1 {
		m := matches[0]
		roles := settings.VoiceChas[m.ch].Roles
		settings.VoiceChas[m.ch].Roles = append(roles[:m.role], roles[m.role+1:]...)
		if len(settings.VoiceChas[m.ch].Roles) == 0 {
			settings.VoiceChas = append(settings.VoiceChas[:m.ch], settings.VoiceChas[m.ch+1:]...)
		}
		if err := store.SetVoiceChas(ic.Context(), ic.GuildID, settings.VoiceChas); err != nil {
			return voicePickerFailed(ic, "Could not save settings.", err)
		}
	}
	entries := voiceEntries(settings)
	if len(entries) == 0 {
		// Explicit empty components: an omitted list would leave the old picker on the message.
		return ic.UpdateMessage(interaction.Response{
			Embeds:     []*discordgo.MessageEmbed{commands.NewEmbed(nil, embedTitleVoiceRoles, msgVoiceNone)},
			Components: []discordgo.MessageComponent{},
		})
	}
	return ic.UpdateMessage(voicePage(entries, page, authorID))
}

func voicePickerFailed(ic *interaction.Context, text string, err error) error {
	logger.For("settings").Error("voice role picker failed", "guild_id", ic.GuildID, "err", err)
	return ic.Refuse(text)
}
