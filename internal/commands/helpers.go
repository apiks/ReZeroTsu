package commands

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

var (
	reTimeCompact = regexp.MustCompile(`(\d+)([wdhmWDHM])`)
	reTimeWeeks   = regexp.MustCompile(`(?i)(\d+)\s*(?:weeks?|wks?)\b`)
	reTimeDays    = regexp.MustCompile(`(?i)(\d+)\s*(?:days?)\b`)
	reTimeHours   = regexp.MustCompile(`(?i)(\d+)\s*(?:hours?|hrs?)\b`)
	reTimeMinutes = regexp.MustCompile(`(?i)(\d+)\s*(?:minutes?|mins?)\b`)
)

func NewEmbed(s *discordgo.Session, title, description string, footer ...string) *discordgo.MessageEmbed {
	return NewEmbedWithFields(s, title, description, nil, footer...)
}

func NewEmbedWithFields(s *discordgo.Session, title, description string, fields []*discordgo.MessageEmbedField, footer ...string) *discordgo.MessageEmbed {
	if len(description) > discord.MaxEmbedDescriptionLength {
		description = description[:discord.MaxEmbedDescriptionLength]
	}
	emb := &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       discord.EmbedColor,
	}
	if len(fields) > 0 {
		if len(fields) > discord.MaxEmbedFields {
			fields = fields[:discord.MaxEmbedFields]
		}
		emb.Fields = fields
	}
	if len(footer) > 0 && footer[0] != "" {
		text := footer[0]
		if len(text) > discord.MaxEmbedFooterLength {
			text = text[:discord.MaxEmbedFooterLength]
		}
		emb.Footer = &discordgo.MessageEmbedFooter{Text: text}
	}
	return emb
}

func chunkDescription(description string, maxLen int) []string {
	if maxLen <= 0 || len(description) <= maxLen {
		return []string{description}
	}
	var chunks []string
	for len(description) > 0 {
		if len(description) <= maxLen {
			chunks = append(chunks, description)
			break
		}
		chunks = append(chunks, description[:maxLen])
		description = description[maxLen:]
	}
	return chunks
}

func RespondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, title, description string, footer ...string) error {
	return RespondEmbedWithFields(s, i, title, description, nil, footer...)
}

// RespondEmbedWithFields edits the deferred response with an embed; long descriptions are chunked with follow-ups.
func RespondEmbedWithFields(s *discordgo.Session, i *discordgo.InteractionCreate, title, description string, fields []*discordgo.MessageEmbedField, footer ...string) error {
	maxDesc := discord.MaxEmbedDescriptionLength
	chunks := chunkDescription(description, maxDesc)
	if len(chunks) == 1 {
		emb := NewEmbedWithFields(s, title, chunks[0], fields, footer...)
		_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}})
		if err != nil {
			args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("RespondEmbed edit failed", args...)
			return err
		}
		return nil
	}
	emb := NewEmbedWithFields(s, title, chunks[0], fields, footer...)
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("RespondEmbed edit failed", args...)
		return err
	}
	continuationChunks := chunks[1:]
	maxPerMsg := discord.MaxEmbedsPerMessage
	for start := 0; start < len(continuationChunks); start += maxPerMsg {
		end := min(start+maxPerMsg, len(continuationChunks))
		batch := make([]*discordgo.MessageEmbed, 0, end-start)
		for _, chunk := range continuationChunks[start:end] {
			batch = append(batch, NewEmbedWithFields(s, "", chunk, nil))
		}
		if _, err := s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{Embeds: batch}); err != nil {
			args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("RespondEmbed follow-up failed", args...)
			return err
		}
	}
	return nil
}

// SendEmbed sends embeds to a channel; long descriptions are chunked (MaxEmbedsPerMessage per message).
func SendEmbed(s *discordgo.Session, channelID, title, description string, footer ...string) error {
	maxDesc := discord.MaxEmbedDescriptionLength
	chunks := chunkDescription(description, maxDesc)
	if len(chunks) == 1 {
		emb := NewEmbed(s, title, chunks[0], footer...)
		_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{emb}})
		if err != nil {
			args := append(discord.RESTAttrs(err), "channel_id", channelID, "err", err)
			logger.For("commands").Error("SendEmbed failed", args...)
			return err
		}
		return nil
	}
	embeds := make([]*discordgo.MessageEmbed, 0, len(chunks))
	embeds = append(embeds, NewEmbed(s, title, chunks[0], footer...))
	for _, chunk := range chunks[1:] {
		embeds = append(embeds, NewEmbedWithFields(s, "", chunk, nil))
	}
	maxPerMsg := discord.MaxEmbedsPerMessage
	for i := 0; i < len(embeds); i += maxPerMsg {
		end := min(i+maxPerMsg, len(embeds))
		batch := embeds[i:end]
		if _, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Embeds: batch}); err != nil {
			args := append(discord.RESTAttrs(err), "channel_id", channelID, "err", err)
			logger.For("commands").Error("SendEmbed batch failed", args...)
			return err
		}
	}
	return nil
}

func normalizeTimeString(given string) string {
	s := reTimeWeeks.ReplaceAllString(given, "${1}w")
	s = reTimeDays.ReplaceAllString(s, "${1}d")
	s = reTimeHours.ReplaceAllString(s, "${1}h")
	s = reTimeMinutes.ReplaceAllString(s, "${1}m")
	return s
}

// ResolveTimeFromString parses "2w1d12h30m" and returns time.Now() + duration. perma true if zero duration.
func ResolveTimeFromString(given string) (ret time.Time, perma bool, err error) {
	ret = time.Now()
	comp := ret
	normalized := normalizeTimeString(given)
	groups := reTimeCompact.FindAllStringSubmatch(normalized, -1)
	if len(groups) == 0 {
		err = fmt.Errorf("invalid time format: %s", given)
		return
	}
	for _, match := range groups {
		if len(match) < 3 {
			continue
		}
		val, convErr := strconv.Atoi(match[1])
		if convErr != nil {
			continue
		}
		switch strings.ToLower(match[2]) {
		case "w":
			ret = ret.AddDate(0, 0, val*7)
		case "d":
			ret = ret.AddDate(0, 0, val)
		case "h":
			ret = ret.Add(time.Hour * time.Duration(val))
		case "m":
			ret = ret.Add(time.Minute * time.Duration(val))
		default:
			err = fmt.Errorf("unrecognized time unit: %s", match[2])
			return
		}
	}
	if ret.Equal(comp) {
		perma = true
	}
	return
}

// LogToBotLog sends errMsg to the guild's bot log channel when set.
func LogToBotLog(ctx context.Context, db *database.Client, s *discordgo.Session, guildID, errMsg string) {
	if guildID == "" {
		return
	}
	settings, err := db.Guilds().GetGuildSettings(ctx, guildID)
	if err != nil || settings == nil || settings.BotLogID == nil || settings.BotLogID.ID == "" {
		return
	}
	if _, err := s.ChannelMessageSend(settings.BotLogID.ID, errMsg); err != nil {
		if discord.IsUnknownChannel(err) {
			if clearErr := db.Guilds().ClearBotLog(ctx, guildID); clearErr != nil {
				logger.For("commands").Error("LogToBotLog ClearBotLog failed", "guild_id", guildID, "err", clearErr)
			} else {
				logger.For("commands").Warn("bot-log channel no longer exists, cleared for guild", "guild_id", guildID)
			}
		} else {
			args := append(discord.RESTAttrs(err), "guild_id", guildID, "err", err)
			logger.For("commands").Error("LogToBotLog ChannelMessageSend failed", args...)
		}
	}
}

func GetSettings(ctx context.Context, db *database.Client, guildID string) (*guilds.GuildSettings, error) {
	return db.Guilds().GetGuildSettings(ctx, guildID)
}

func UserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	return i.User.ID
}

func InteractionUser(i *discordgo.InteractionCreate) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

func ParseOption(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.ApplicationCommandInteractionDataOption {
	for _, o := range opts {
		if o.Name == name {
			return o
		}
	}
	return nil
}

// TargetChannelID returns the "channel" option's ID, or the interaction's channel ID.
func TargetChannelID(data discordgo.ApplicationCommandInteractionData, s *discordgo.Session, i *discordgo.InteractionCreate) string {
	opt := ParseOption(data.Options, "channel")
	if opt != nil {
		return opt.ChannelValue(s).ID
	}
	return i.ChannelID
}
