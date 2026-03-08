package say

import (
	"context"
	"strconv"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleSay  = "Say"
	embedTitleEdit = "Edit"
	successSay     = "Success! Message sent."
	successEmbed   = "Success! Embed message sent."
	successEdit    = "Success! Target message has been edited."
	successEditEmb = "Success! Target embed message has been edited."
)

func init() {
	channelOpt := &discordgo.ApplicationCommandOption{
		Type:         discordgo.ApplicationCommandOptionChannel,
		Name:         "channel",
		Description:  "The channel to send the message to (default: current channel).",
		Required:     false,
		ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
	}

	commands.Add(&commands.Command{
		Name:       "say",
		Desc:       "Sends a message from the bot in the target channel.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "misc",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "The message you want to send.",
				Required:    true,
			},
			channelOpt,
		},
		Handler: handleSay,
	})

	commands.Add(&commands.Command{
		Name:       "embed",
		Desc:       "Sends an embed message from the bot in the target channel.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "misc",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "The message you want to send in the embed.",
				Required:    true,
			},
			channelOpt,
		},
		Handler: handleEmbed,
	})

	commands.Add(&commands.Command{
		Name:       "edit",
		Desc:       "Edits a message sent by the bot.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "misc",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionChannel,
				Name:         "channel",
				Description:  "The channel in which the message is.",
				Required:     true,
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message-id",
				Description: "The ID of the message to edit.",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "The new message content.",
				Required:    true,
			},
		},
		Handler: handleEdit,
	})

	commands.Add(&commands.Command{
		Name:       "edit-embed",
		Desc:       "Edits an embed message sent by the bot.",
		Permission: commands.PermMod,
		Context:    commands.ContextGuildOnly,
		Module:     "misc",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionChannel,
				Name:         "channel",
				Description:  "The channel in which the message is.",
				Required:     true,
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message-id",
				Description: "The ID of the message to edit.",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "The new embed description.",
				Required:    true,
			},
		},
		Handler: handleEditEmbed,
	})
}

func handleSay(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	optMsg := commands.ParseOption(data.Options, "message")
	if optMsg == nil || optMsg.StringValue() == "" {
		commands.RespondEmbed(s, i, embedTitleSay, "Please provide a message.")
		return
	}
	channelID := commands.TargetChannelID(data, s, i)
	if _, err := s.ChannelMessageSend(channelID, optMsg.StringValue()); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", channelID, "err", err)
		logger.For("commands").Error("say send failed", args...)
		commands.RespondEmbed(s, i, embedTitleSay, "Could not send message: "+err.Error())
		return
	}
	editResponseContent(s, i, successSay)
}

func handleEmbed(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	optMsg := commands.ParseOption(data.Options, "message")
	if optMsg == nil || optMsg.StringValue() == "" {
		commands.RespondEmbed(s, i, embedTitleSay, "Please provide a message.")
		return
	}
	channelID := commands.TargetChannelID(data, s, i)
	if err := commands.SendEmbed(s, channelID, "", optMsg.StringValue()); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", channelID, "err", err)
		logger.For("commands").Error("embed send failed", args...)
		commands.RespondEmbed(s, i, embedTitleSay, "Could not send embed: "+err.Error())
		return
	}
	editResponseContent(s, i, successEmbed)
}

func handleEdit(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	optCh := commands.ParseOption(data.Options, "channel")
	optID := commands.ParseOption(data.Options, "message-id")
	optMsg := commands.ParseOption(data.Options, "message")
	if optCh == nil || optID == nil || optMsg == nil {
		commands.RespondEmbed(s, i, embedTitleEdit, "Please provide channel, message-id, and message.")
		return
	}
	channelID := optCh.ChannelValue(s).ID
	messageID := optID.StringValue()
	if _, err := strconv.ParseInt(messageID, 10, 64); err != nil || len(messageID) < 17 {
		commands.RespondEmbed(s, i, embedTitleEdit, "Invalid message ID.")
		return
	}
	if _, err := s.ChannelMessageEdit(channelID, messageID, optMsg.StringValue()); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", channelID, "err", err)
		logger.For("commands").Error("edit failed", args...)
		commands.RespondEmbed(s, i, embedTitleEdit, "Could not edit message: "+err.Error())
		return
	}
	editResponseContent(s, i, successEdit)
}

func handleEditEmbed(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	optCh := commands.ParseOption(data.Options, "channel")
	optID := commands.ParseOption(data.Options, "message-id")
	optMsg := commands.ParseOption(data.Options, "message")
	if optCh == nil || optID == nil || optMsg == nil {
		commands.RespondEmbed(s, i, embedTitleEdit, "Please provide channel, message-id, and message.")
		return
	}
	channelID := optCh.ChannelValue(s).ID
	messageID := optID.StringValue()
	if _, err := strconv.ParseInt(messageID, 10, 64); err != nil || len(messageID) < 17 {
		commands.RespondEmbed(s, i, embedTitleEdit, "Invalid message ID.")
		return
	}
	emb := commands.NewEmbed(s, "", optMsg.StringValue())
	if _, err := s.ChannelMessageEditEmbed(channelID, messageID, emb); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", channelID, "err", err)
		logger.For("commands").Error("edit-embed failed", args...)
		commands.RespondEmbed(s, i, embedTitleEdit, "Could not edit embed: "+err.Error())
		return
	}
	editResponseContent(s, i, successEditEmb)
}

func editResponseContent(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("say/edit edit response failed", args...)
	}
}
