package prune

import (
	"errors"
	"fmt"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	maxPrune         = 5000
	maxPruneAgeHours = 336 // 14 days
	bulkDeleteChunk  = 100

	// pruneNeeds is what the bot needs in the pruned channel: to see it, read its history and delete messages.
	pruneNeeds = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionManageMessages
)

// refusal is a problem with the request itself, worded for the person who made it.
type refusal string

func (r refusal) Error() string { return string(r) }

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Private,
		Response:   commands.Deferred,
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
		Run: handlePrune,
	})
}

// handlePrune answers privately through the placeholder the dispatcher already showed. Every refusal and error
// edits that placeholder (via ic.Refuse and ic.Fail); the confirmation stays and is never deleted.
func handlePrune(ic *interaction.Context) error {
	data := ic.Command
	channelID := ic.ChannelID
	if opt := commands.ParseOption(data.Options, "channel"); opt != nil {
		channelID = opt.ChannelValue(nil).ID
	}
	at := interaction.ErrorContext{ChannelID: channelID, Needs: pruneNeeds}

	optNum := commands.ParseOption(data.Options, "number")
	if optNum == nil {
		return ic.Refuse("Please provide a number (1–5000).")
	}
	amount := int(optNum.IntValue())

	// Immediate feedback for long prunes.
	if amount > bulkDeleteChunk && amount <= maxPrune {
		msg := "Fetching messages and beginning pruning. This might take a while. It may take up to a minute for the change to be reflected afterwards."
		if err := ic.EditReply(interaction.Response{Content: msg}); err != nil {
			logEditFailed(ic, err)
		}
	}

	deleted, err := pruneMessages(ic, amount, channelID)
	if err != nil {
		var r refusal
		if errors.As(err, &r) {
			return ic.Refuse(string(r))
		}
		return ic.Fail(err, at)
	}

	resp := fmt.Sprintf("Success! Removed %d valid messages in <#%s>.", deleted, channelID)
	if err := ic.EditReply(interaction.Response{Content: resp}); err != nil {
		logEditFailed(ic, err)
	}
	return nil
}

func logEditFailed(ic *interaction.Context, err error) {
	args := append(discord.RESTAttrs(err), "guild_id", ic.GuildID, "channel_id", ic.ChannelID, "err", err)
	if discord.IsUnknownMessage(err) {
		logger.For("commands").Info("prune edit failed (message already gone)", args...)
		return
	}
	logger.For("commands").Error("prune edit failed", args...)
}

// pruneMessages fetches up to amount (newest-first), skips messages over 14 days old, and deletes them in
// chunks of 100. The private reply is not a channel message, so there is nothing of the bot's own to skip.
func pruneMessages(port interaction.Port, amount int, channelID string) (int, error) {
	if amount < 1 {
		return 0, refusal("Invalid amount given. Minimum is 1.")
	}
	if amount > maxPrune {
		return 0, refusal("Amount is too large. Maximum is 5000.")
	}

	now := time.Now()
	var deleteMessageIDs []string

	lastMessages, err := port.ListMessages(channelID, 1, "")
	if err != nil {
		return 0, err
	}
	if len(lastMessages) == 0 {
		return 0, refusal("No valid messages could be found to delete.")
	}
	newest := lastMessages[0]
	beforeID := newest.ID
	if now.Sub(newest.Timestamp).Hours() < maxPruneAgeHours {
		deleteMessageIDs = append(deleteMessageIDs, newest.ID)
	}
	remaining := amount - len(deleteMessageIDs)

OuterLoop:
	for remaining > 0 {
		limit := min(remaining, bulkDeleteChunk)
		messages, err := port.ListMessages(channelID, limit, beforeID)
		if err != nil {
			return 0, err
		}
		for _, m := range messages {
			if now.Sub(m.Timestamp).Hours() >= maxPruneAgeHours {
				break OuterLoop
			}
			deleteMessageIDs = append(deleteMessageIDs, m.ID)
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
		return 0, refusal("The messages are either more than 14 days old, I cannot fetch them, or there are no other valid messages to prune.")
	}

	for i := 0; i < len(deleteMessageIDs); {
		end := min(i+bulkDeleteChunk, len(deleteMessageIDs))
		chunk := deleteMessageIDs[i:end]
		if len(chunk) == 1 {
			if err := port.DeleteMessage(channelID, chunk[0]); err != nil {
				return 0, err
			}
		} else {
			if err := port.DeleteMessages(channelID, chunk); err != nil {
				return 0, err
			}
		}
		i = end
	}
	return len(deleteMessageIDs), nil
}
