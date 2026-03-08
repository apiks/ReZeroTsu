package prune

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitlePrune    = "Prune"
	maxPrune           = 5000
	maxPruneAgeHours   = 336 // 14 days
	bulkDeleteChunk    = 100
	successDeleteDelay = 3 * time.Second
)

func init() {
	commands.Add(&commands.Command{
		Name:       "prune",
		Desc:       "Prunes the previous x amount of messages. Messages must not be older than 14 days. Max is 5000.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "channel",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "number",
				Description: "Number of messages to delete (1–5000).",
				Required:    true,
			},
			{
				Type:         discordgo.ApplicationCommandOptionChannel,
				Name:         "channel",
				Description:  "The channel in which to delete messages (default: current channel).",
				Required:     false,
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
			},
		},
		Handler: handlePrune,
	})
}

func handlePrune(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	optNum := commands.ParseOption(data.Options, "number")
	if optNum == nil {
		commands.RespondEmbed(s, i, embedTitlePrune, "Please provide a number (1–5000).")
		return
	}
	amount := int(optNum.IntValue())
	if amount < 1 {
		commands.RespondEmbed(s, i, embedTitlePrune, "Invalid amount given. Minimum is 1.")
		return
	}
	if amount > maxPrune {
		commands.RespondEmbed(s, i, embedTitlePrune, "Amount is too large. Maximum is 5000.")
		return
	}
	channelID := commands.TargetChannelID(data, s, i)

	// Immediate feedback for long prunes
	if amount > bulkDeleteChunk {
		msg := "Fetching messages and beginning pruning. This might take a while. It may take up to a minute for the change to be reflected afterwards."
		commands.RespondEmbed(s, i, embedTitlePrune, msg)
	}

	var excludeMessageID string
	if respMsg, respErr := s.InteractionResponse(i.Interaction); respErr == nil && respMsg != nil {
		excludeMessageID = respMsg.ID
	}
	deleted, err := pruneMessages(s, amount, channelID, excludeMessageID)
	if err != nil {
		commands.RespondEmbed(s, i, embedTitlePrune, err.Error())
		return
	}

	resp := fmt.Sprintf("Success! Removed %d valid messages in this channel. Deleting this message in 3 seconds...", deleted)
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &resp}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		if discord.IsUnknownMessage(err) {
			logger.For("commands").Info("prune edit failed (message already gone)", args...)
		} else {
			logger.For("commands").Error("prune edit failed", args...)
		}
		return
	}
	time.Sleep(successDeleteDelay)
	if err := s.InteractionResponseDelete(i.Interaction); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("prune delete response failed", args...)
	}
}

// pruneMessages fetches up to amount (newest-first), skips >14 days and excludeMessageID, bulk-deletes in chunks of 100.
func pruneMessages(s *discordgo.Session, amount int, channelID, excludeMessageID string) (int, error) {
	if amount < 1 {
		return 0, errors.New("Invalid amount given. Minimum is 1.")
	}
	if amount > maxPrune {
		return 0, errors.New("Amount is too large. Maximum is 5000.")
	}

	now := time.Now()
	var deleteMessageIDs []string

	lastMessages, err := s.ChannelMessages(channelID, 1, "", "", "")
	if err != nil {
		return 0, err
	}
	if len(lastMessages) == 0 {
		return 0, errors.New("No valid messages could be found to delete.")
	}
	newest := lastMessages[0]
	beforeID := newest.ID
	if now.Sub(newest.Timestamp).Hours() < maxPruneAgeHours && newest.ID != excludeMessageID {
		deleteMessageIDs = append(deleteMessageIDs, newest.ID)
	}
	remaining := amount - len(deleteMessageIDs)

OuterLoop:
	for remaining > 0 {
		limit := min(remaining, bulkDeleteChunk)
		messages, err := s.ChannelMessages(channelID, limit, beforeID, "", "")
		if err != nil {
			return 0, err
		}
		for _, m := range messages {
			if now.Sub(m.Timestamp).Hours() >= maxPruneAgeHours {
				break OuterLoop
			}
			if m.ID != excludeMessageID {
				deleteMessageIDs = append(deleteMessageIDs, m.ID)
			}
		}
		if len(messages) == 0 {
			break
		}
		beforeID = messages[len(messages)-1].ID // oldest in this batch for next "before"
		remaining = amount - len(deleteMessageIDs)
		if remaining <= 0 || len(messages) < limit {
			break
		}
	}

	if len(deleteMessageIDs) == 0 {
		return 0, errors.New("The messages are either more than 14 days old, I cannot fetch them, or there are no other valid messages to prune.")
	}

	for i := 0; i < len(deleteMessageIDs); {
		end := min(i+bulkDeleteChunk, len(deleteMessageIDs))
		chunk := deleteMessageIDs[i:end]
		if len(chunk) == 1 {
			if err := s.ChannelMessageDelete(channelID, chunk[0]); err != nil {
				return 0, err
			}
		} else {
			if err := s.ChannelMessagesBulkDelete(channelID, chunk); err != nil {
				return 0, err
			}
		}
		i = end
	}
	return len(deleteMessageIDs), nil
}
