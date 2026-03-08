package pick

import (
	"context"
	"math/rand"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const embedTitlePick = "Pick"

func init() {
	commands.Add(&commands.Command{
		Name:       "pick",
		Desc:       "Picks a random item from a list of items.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "items",
				Description: "Items to select from, separate using comma (,) or pipe (|). Minimum 2 items required.",
				Required:    true,
			},
		},
		Handler: handlePick,
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "pick",
		Aliases:    []string{"pic", "pik", "p"},
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			parsed := parseItems(args)
			if len(parsed) < 2 {
				emb := commands.NewEmbed(s, embedTitlePick, "Please provide items. Separate using comma (`,`) or pipe (`|`). Minimum 2 items required.")
				if len(parsed) == 1 {
					emb.Description = "At least 2 items required."
				}
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			picked := parsed[rand.Intn(len(parsed))]
			msg := "**Picked:** " + picked
			for len(msg) > 0 {
				chunk := msg
				if len(chunk) > discord.MaxContentLength {
					chunk = msg[:discord.MaxContentLength]
					msg = msg[discord.MaxContentLength:]
				} else {
					msg = ""
				}
				_, _ = s.ChannelMessageSend(m.ChannelID, chunk)
			}
		},
	})
}

func handlePick(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	opt := commands.ParseOption(data.Options, "items")
	if opt == nil || opt.StringValue() == "" {
		commands.RespondEmbed(s, i, embedTitlePick, "Please provide items. Separate using comma (`,`) or pipe (`|`). Minimum 2 items required.")
		return
	}

	parsed := parseItems(opt.StringValue())
	if len(parsed) < 2 {
		commands.RespondEmbed(s, i, embedTitlePick, "At least 2 items required.")
		return
	}

	picked := parsed[rand.Intn(len(parsed))]
	msg := "**Picked:** " + picked

	if len(msg) <= discord.MaxContentLength {
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg}); err != nil {
			args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("pick edit failed", args...)
		}
		return
	}

	first := msg[:discord.MaxContentLength]
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &first}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("pick edit failed", args...)
		return
	}
	rest := msg[discord.MaxContentLength:]
	for len(rest) > 0 {
		chunk := rest
		if len(chunk) > discord.MaxContentLength {
			chunk = rest[:discord.MaxContentLength]
			rest = rest[discord.MaxContentLength:]
		} else {
			rest = ""
		}
		if _, err := s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{Content: chunk}); err != nil {
			args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("pick followup failed", args...)
			return
		}
	}
}

// parseItems splits input by |, comma, or ‚; trims and drops empty.
func parseItems(input string) []string {
	sep := "|"
	if !strings.Contains(input, sep) {
		sep = ","
		if !strings.Contains(input, sep) {
			sep = "‚" // U+201A single low-9 quotation mark
		}
	}
	parts := strings.Split(input, sep)
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
