package bot

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// darlingCountByGuild counts pings per guild for "Daaarling~"; guarded by darlingMu.
var (
	darlingCountByGuild = make(map[string]int)
	darlingMu           sync.Mutex
)

func isBarePing(content, botID string) bool {
	norm := strings.TrimSpace(strings.ToLower(content))
	return norm == fmt.Sprintf("<@%s>", botID) || norm == fmt.Sprintf("<@!%s>", botID)
}

func isGoodBot(content, botID string) bool {
	norm := strings.TrimSpace(strings.ToLower(content))
	return norm == fmt.Sprintf("<@%s> good bot", botID) || norm == fmt.Sprintf("<@!%s> good bot", botID)
}

func sendPingReply(ctx context.Context, db *database.Client, s *discordgo.Session, channelID, content, guildID string, settings *guilds.GuildSettings) {
	_, err := s.ChannelMessageSend(channelID, content)
	if err != nil {
		logger.For("bot").Error("ping reply send failed", "channel_id", channelID, "guild_id", guildID, "err", err)
		if settings != nil && settings.BotLogID != nil && settings.BotLogID.ID != "" {
			if _, err2 := s.ChannelMessageSend(settings.BotLogID.ID, fmt.Sprintf("Failed to send ping reply: %v", err)); err2 != nil {
				if discord.IsUnknownChannel(err2) {
					if clearErr := db.Guilds().ClearBotLog(ctx, guildID); clearErr != nil {
						logger.For("bot").Error("ClearBotLog failed", "guild_id", guildID, "err", clearErr)
					} else {
						logger.For("bot").Warn("bot-log channel no longer exists, cleared for guild", "guild_id", guildID)
					}
				} else {
					logger.For("bot").Error("ChannelMessageSend bot-log ping failure", "guild_id", guildID, "err", err2)
				}
			}
		}
	}
}

// pingReplyTable: special replies for bare pings (fixed or random).
type pingReplyTable struct {
	fixed  string   // if non-empty, always send this
	random []string // if non-empty and fixed is empty, pick one at random
}

var pingRepliesByUserID = map[string]pingReplyTable{
	"128312718779219968": {fixed: "Professor!"},
	"66207186417627136": {
		random: []string{"Bug hunter!", "Player!", "Big brain!", "Poster expert!", "Idiot!"},
	},
	"365245718866427904": {
		random: []string{"Begone ethot.", "Humph!", "Wannabe ethot!", "Not even worth my time.", "Okay, maybe you're not that bad."},
	},
}

func OnMessageCreate(ctx context.Context, db *database.Client, ownerID string, prefixes []string, runtime commands.BotRuntime) func(*discordgo.Session, *discordgo.MessageCreate) {
	return func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if commands.HandleMessageCommand(ctx, db, s, m, ownerID, prefixes, runtime) {
			return
		}
		if m.Author == nil || m.Author.ID == s.State.User.ID {
			return
		}
		if m.GuildID == "" {
			return
		}
		if m.Author.Bot {
			return
		}
		for _, u := range m.Mentions {
			if u.ID == s.State.User.ID {
				replyWithPingMessage(ctx, db, s, m)
				return
			}
		}
	}
}

func replyWithPingMessage(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate) {
	botID := s.State.User.ID
	if botID == "" {
		return
	}

	settings, err := db.Guilds().GetGuildSettings(ctx, m.GuildID)
	if err != nil || settings == nil {
		settings = guilds.DefaultGuildSettings()
	}

	// "good bot" → Thank you ❤
	if isGoodBot(m.Content, botID) {
		sendPingReply(ctx, db, s, m.ChannelID, "Thank you ❤", m.GuildID, settings)
		return
	}

	if !isBarePing(m.Content, botID) {
		return
	}

	// Bare ping: user-specific reply
	if tbl, ok := pingRepliesByUserID[m.Author.ID]; ok {
		var msg string
		if tbl.fixed != "" {
			msg = tbl.fixed
		} else if len(tbl.random) > 0 {
			msg = tbl.random[rand.Intn(len(tbl.random))]
		}
		if msg != "" {
			sendPingReply(ctx, db, s, m.ChannelID, msg, m.GuildID, settings)
			return
		}
	}

	// Darling trigger (per-guild): after 6+ pings, "Daaarling~" and reset; else "Baka!" and increment
	darlingMu.Lock()
	count := darlingCountByGuild[m.GuildID]
	if count > 6 {
		darlingCountByGuild[m.GuildID] = 0
		darlingMu.Unlock()
		sendPingReply(ctx, db, s, m.ChannelID, "Daaarling~", m.GuildID, settings)
		return
	}
	darlingCountByGuild[m.GuildID]++
	darlingMu.Unlock()

	// Default: "Baka!"
	sendPingReply(ctx, db, s, m.ChannelID, "Baka!", m.GuildID, settings)
}
