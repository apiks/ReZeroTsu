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
	emb := &discordgo.MessageEmbed{
		Title:       title,
		Description: discord.Fit(description, discord.MaxEmbedDescriptionLength),
		Color:       discord.EmbedColor,
	}
	if len(fields) > 0 {
		if len(fields) > discord.MaxEmbedFields {
			fields = fields[:discord.MaxEmbedFields]
		}
		emb.Fields = fields
	}
	if len(footer) > 0 && footer[0] != "" {
		emb.Footer = &discordgo.MessageEmbedFooter{Text: discord.Fit(footer[0], discord.MaxEmbedFooterLength)}
	}
	return emb
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

// UserID is the ID of the user whose interaction i is: the member in a server, else the user.
func UserID(i *discordgo.Interaction) string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	return i.User.ID
}

func ParseOption(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.ApplicationCommandInteractionDataOption {
	for _, o := range opts {
		if o.Name == name {
			return o
		}
	}
	return nil
}

// MessageLink is the URL of a message in a server.
func MessageLink(guildID, channelID, messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}

// TruncateRunes cuts s to at most limit runes, ending in an ellipsis when it was cut.
func TruncateRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit < 1 {
		return ""
	}
	return string(r[:limit-1]) + "…"
}
