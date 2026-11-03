package settings

import (
	"context"
	"time"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/anime_subs"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	idNewEpisodes     = panelPrefix + "new-episodes"
	idNewEpisodesRole = panelPrefix + "new-episodes-role"
	idDailySchedule   = panelPrefix + "daily-schedule"

	postTypeNewEpisodes   = "newepisodes"
	postTypeDailySchedule = "dailyschedule"

	msgSetChannelFirst = "Set a channel first."
	msgNotTextChannel  = "Please choose a text or thread channel."
	msgNoRole          = "That role was not found. Pick it again."

	// warningMarker starts the panel line that says the chosen ping role cannot be mentioned.
	warningMarker = "Warning:"
	warningText   = warningMarker + " the new-episodes ping role is not mentionable and the bot cannot mention everyone there, so pings will not arrive. Make the role mentionable or give the bot Mention Everyone."

	// warningLookupTimeout bounds the guild read behind the mentionable warning, which must not hold up a panel.
	warningLookupTimeout = time.Second
)

// dbStore is the settings store backed by the database: the guild repository plus the repository that holds the
// new-episodes sent-state record for a channel.
type dbStore struct {
	*guilds.Repo
	subs *anime_subs.Repo
}

func newDBStore(db *database.Client) dbStore { return dbStore{Repo: db.Guilds(), subs: db.AnimeSubs()} }

// DeleteSentState deletes the new-episodes sent-state record for a channel (stored by the older anime_subs package).
func (s dbStore) DeleteSentState(ctx context.Context, id string) error {
	return s.subs.DeleteByID(ctx, id)
}

// panelData is everything the panel shows: the guild settings and the two autoposts, nil while off.
type panelData struct {
	gs            *guilds.GuildSettings
	newEpisodes   *guilds.Autopost
	dailySchedule *guilds.Autopost
}

// readPanel reads the stored settings and autoposts with one read of the guild document. Each is a copy the
// caller may change. A missing guild or missing settings show the defaults.
func readPanel(ctx context.Context, store settingsStore, guildID string) (*panelData, error) {
	g, err := store.GetGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	gs := guilds.DefaultGuildSettings()
	d := &panelData{gs: gs}
	if g == nil {
		return d, nil
	}
	if g.GuildSettings != nil {
		c := *g.GuildSettings
		d.gs = &c
	}
	for i := range g.Autoposts {
		switch g.Autoposts[i].PostType {
		case postTypeNewEpisodes:
			d.newEpisodes = activeAutopost(&g.Autoposts[i])
		case postTypeDailySchedule:
			d.dailySchedule = activeAutopost(&g.Autoposts[i])
		}
	}
	return d, nil
}

// activeAutopost is the autopost, or nil when it is absent or has no channel.
func activeAutopost(ap *guilds.Autopost) *guilds.Autopost {
	if ap == nil || ap.ID == "" {
		return nil
	}
	c := *ap
	return &c
}

// copyOf is an independent copy of the panel data for a change to edit.
func (d *panelData) copyOf() *panelData {
	gs := *d.gs
	out := &panelData{gs: &gs}
	out.newEpisodes = activeAutopost(d.newEpisodes)
	out.dailySchedule = activeAutopost(d.dailySchedule)
	return out
}

func autopostChannelID(ap *guilds.Autopost) string {
	if ap == nil {
		return ""
	}
	return ap.ID
}

func autopostRoleID(ap *guilds.Autopost) string {
	if ap == nil {
		return ""
	}
	return ap.RoleID
}

var autopostChannelTypes = []discordgo.ChannelType{
	discordgo.ChannelTypeGuildText,
	discordgo.ChannelTypeGuildPublicThread,
	discordgo.ChannelTypeGuildPrivateThread,
}

func isAutopostChannelType(t discordgo.ChannelType) bool {
	for _, ok := range autopostChannelTypes {
		if t == ok {
			return true
		}
	}
	return false
}

func channelPicker(id, placeholder, current string) discordgo.MessageComponent {
	none := 0
	menu := discordgo.SelectMenu{
		MenuType:     discordgo.ChannelSelectMenu,
		CustomID:     id,
		Placeholder:  placeholder,
		MinValues:    &none,
		MaxValues:    1,
		ChannelTypes: autopostChannelTypes,
	}
	if current != "" {
		menu.DefaultValues = []discordgo.SelectMenuDefaultValue{{ID: current, Type: discordgo.SelectMenuDefaultValueChannel}}
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}
}

// newEpisodesRow is row 2, the new-episodes channel picker.
func newEpisodesRow(d *panelData) discordgo.MessageComponent {
	return channelPicker(idNewEpisodes, "New episodes channel (clear it to turn the autopost off)", autopostChannelID(d.newEpisodes))
}

// newEpisodesRoleRow is row 3, the new-episodes ping role picker.
func newEpisodesRoleRow(d *panelData) discordgo.MessageComponent {
	none := 0
	menu := discordgo.SelectMenu{
		MenuType:    discordgo.RoleSelectMenu,
		CustomID:    idNewEpisodesRole,
		Placeholder: "New episodes ping role (clear it to stop pinging)",
		MinValues:   &none,
		MaxValues:   1,
	}
	if id := autopostRoleID(d.newEpisodes); id != "" {
		menu.DefaultValues = []discordgo.SelectMenuDefaultValue{{ID: id, Type: discordgo.SelectMenuDefaultValueRole}}
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}
}

// dailyScheduleRow is row 4, the daily-schedule channel picker.
func dailyScheduleRow(d *panelData) discordgo.MessageComponent {
	return channelPicker(idDailySchedule, "Daily schedule channel (clear it to turn it off)", autopostChannelID(d.dailySchedule))
}

// mentionWarning is the panel line shown while the chosen ping role is not mentionable, the bot lacks Mention
// Everyone in the new-episodes channel and the role is not @everyone. It is "" when the condition does not hold
// or cannot be checked.
func mentionWarning(ic *interaction.Context, ap *guilds.Autopost) string {
	if ap == nil || ap.RoleID == "" || ap.RoleID == ic.GuildID {
		return ""
	}
	ctx, cancel := context.WithTimeout(ic.Context(), warningLookupTimeout)
	defer cancel()
	guild, err := ic.Guild(ctx, ic.GuildID)
	if err != nil || guild == nil {
		return ""
	}
	var role *discordgo.Role
	for _, r := range guild.Roles {
		if r.ID == ap.RoleID {
			role = r
			break
		}
	}
	if role == nil || role.Mentionable {
		return ""
	}
	perms, err := ic.BotChannelPermissions(ap.ID)
	if err != nil || perms&(discordgo.PermissionMentionEveryone|discordgo.PermissionAdministrator) != 0 {
		return ""
	}
	return warningText
}

// choose resolves the one channel a picker submission carries. It returns nil for a cleared picker, and ok is
// false with the refusal text when the choice cannot be used.
func chosenChannel(ic *interaction.Context) (ch *discordgo.Channel, refusal string) {
	if len(ic.Values) == 0 {
		return nil, ""
	}
	if data, ok := ic.Interaction.Data.(discordgo.MessageComponentInteractionData); ok {
		ch = data.Resolved.Channels[ic.Values[0]]
	}
	if ch == nil {
		return nil, msgNoChannel
	}
	if !isAutopostChannelType(ch.Type) {
		return nil, msgNotTextChannel
	}
	return ch, ""
}

// chooseNewEpisodes sets the new-episodes channel, or clears the autopost when the picker was cleared. Both a
// change and a clear drop the previous channel's sent state, as /new-episodes did.
func chooseNewEpisodes(ic *interaction.Context, d *draft) string {
	ch, refusal := chosenChannel(ic)
	if refusal != "" {
		return refusal
	}
	prev := d.newEpisodes
	if ch == nil {
		if prev != nil {
			d.staleChannels = append(d.staleChannels, prev.ID)
		}
		d.newEpisodes = nil
		return ""
	}
	ap := prev
	if ap == nil {
		ap = &guilds.Autopost{PostType: postTypeNewEpisodes}
	}
	if ap.ID != "" && ap.ID != ch.ID {
		d.staleChannels = append(d.staleChannels, ap.ID)
	}
	ap.Name, ap.ID = ch.Name, ch.ID
	d.newEpisodes = ap
	return ""
}

// chooseNewEpisodesRole sets the ping role of the new-episodes autopost, or clears it. A role needs a channel.
func chooseNewEpisodesRole(ic *interaction.Context, d *draft) string {
	if len(ic.Values) == 0 {
		if d.newEpisodes != nil {
			d.newEpisodes.RoleID = ""
		}
		return ""
	}
	if d.newEpisodes == nil {
		return msgSetChannelFirst
	}
	id := ic.Values[0]
	if data, ok := ic.Interaction.Data.(discordgo.MessageComponentInteractionData); !ok || data.Resolved.Roles[id] == nil {
		return msgNoRole
	}
	d.newEpisodes.RoleID = id
	return ""
}

// chooseDailySchedule sets the daily-schedule channel, or turns the autopost off when the picker was cleared.
func chooseDailySchedule(ic *interaction.Context, d *draft) string {
	ch, refusal := chosenChannel(ic)
	if refusal != "" {
		return refusal
	}
	if ch == nil {
		d.dailySchedule = nil
		return ""
	}
	d.dailySchedule = &guilds.Autopost{PostType: postTypeDailySchedule, Name: ch.Name, ID: ch.ID}
	return ""
}

// dropSentState deletes the sent-state documents of channels the new-episodes autopost left. A failure is
// logged and does not stop the change, as before.
func dropSentState(ctx context.Context, store settingsStore, guildID string, ids []string) {
	for _, id := range ids {
		if err := store.DeleteSentState(ctx, id); err != nil {
			logger.For("settings").Error("delete new-episodes sent state failed", "guild_id", guildID, "channel_id", id, "err", err)
		}
	}
}
