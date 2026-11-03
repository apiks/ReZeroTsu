package interaction

import (
	"github.com/bwmarrin/discordgo"
)

// DiscordgoPort is the Port backed by a discordgo session, bound to one interaction. This file is the only
// place outside the not-yet-migrated command files that may call the session's interaction-response methods;
// the archcheck guardrail enforces it.
type DiscordgoPort struct {
	s *discordgo.Session
	i *discordgo.Interaction
}

// NewDiscordgoPort binds a session to one interaction.
func NewDiscordgoPort(s *discordgo.Session, i *discordgo.Interaction) *DiscordgoPort {
	return &DiscordgoPort{s: s, i: i}
}

func flags(v Visibility) discordgo.MessageFlags {
	if v == Private {
		return discordgo.MessageFlagsEphemeral
	}
	return 0
}

func (p *DiscordgoPort) Reply(r Response) error {
	return p.s.InteractionRespond(p.i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: r.Content, Embeds: r.Embeds, Components: r.Components, Flags: flags(r.Visibility),
		},
	})
}

func (p *DiscordgoPort) Defer(v Visibility) error {
	return p.s.InteractionRespond(p.i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: flags(v)},
	})
}

func (p *DiscordgoPort) EditReply(r Response) error {
	embeds := r.Embeds
	if embeds == nil {
		embeds = []*discordgo.MessageEmbed{}
	}
	components := r.Components
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	_, err := p.s.InteractionResponseEdit(p.i, &discordgo.WebhookEdit{Content: &r.Content, Embeds: &embeds, Components: &components})
	return err
}

func (p *DiscordgoPort) FollowUp(r Response) error {
	_, err := p.s.FollowupMessageCreate(p.i, true, &discordgo.WebhookParams{
		Content: r.Content, Embeds: r.Embeds, Components: r.Components, Flags: flags(r.Visibility),
	})
	return err
}

func (p *DiscordgoPort) DeleteReply() error {
	return p.s.InteractionResponseDelete(p.i)
}

func (p *DiscordgoPort) OpenForm(f Form) error {
	return p.s.InteractionRespond(p.i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{CustomID: f.CustomID, Title: f.Title, Components: f.Components},
	})
}

func (p *DiscordgoPort) UpdateMessage(r Response) error {
	return p.s.InteractionRespond(p.i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: r.Content, Embeds: r.Embeds, Components: r.Components},
	})
}

func (p *DiscordgoPort) PostMessage(channelID string, r Response) (*discordgo.Message, error) {
	return p.s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: r.Content, Embeds: r.Embeds, Components: r.Components})
}

func (p *DiscordgoPort) EditMessage(channelID, messageID string, r Response) error {
	embeds := r.Embeds
	if embeds == nil {
		embeds = []*discordgo.MessageEmbed{}
	}
	_, err := p.s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: channelID, ID: messageID, Content: &r.Content, Embeds: &embeds, Components: &r.Components,
	})
	return err
}

func (p *DiscordgoPort) ReadMessage(channelID, messageID string) (*discordgo.Message, error) {
	return p.s.ChannelMessage(channelID, messageID)
}

func (p *DiscordgoPort) AddReaction(channelID, messageID, emoji string) error {
	return p.s.MessageReactionAdd(channelID, messageID, emoji)
}

// Guild reads the cached guild and falls back to REST when the cache has no entry.
func (p *DiscordgoPort) Guild(guildID string) (*discordgo.Guild, error) {
	if g, err := p.s.State.Guild(guildID); err == nil {
		return g, nil
	}
	return p.s.Guild(guildID)
}

func (p *DiscordgoPort) Roles(guildID string) ([]*discordgo.Role, error) {
	return p.s.GuildRoles(guildID)
}

// Channel reads the cached channel and falls back to REST when the cache has no entry.
func (p *DiscordgoPort) Channel(channelID string) (*discordgo.Channel, error) {
	if c, err := p.s.State.Channel(channelID); err == nil {
		return c, nil
	}
	return p.s.Channel(channelID)
}
