package pick

import (
	"context"
	"math/rand"
	"strings"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

const embedTitlePick = "Pick"

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Direct,
		Name:       "pick",
		Desc:       "Picks a random item from a list of items.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   commands.BothInstalls(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "items",
				Description: "Items to select from, separate using comma (,) or pipe (|). Minimum 2 items required.",
				Required:    true,
			},
		},
		Run: run,
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

func run(ic *interaction.Context) error {
	var input string
	if opt := commands.ParseOption(ic.Command.Options, "items"); opt != nil {
		input = opt.StringValue()
	}
	parsed := parseItems(input)
	switch {
	case len(parsed) == 0:
		return ic.Refuse("Please provide items. Separate using comma (`,`) or pipe (`|`). Minimum 2 items required.")
	case len(parsed) < 2:
		return ic.Refuse("At least 2 items required.")
	}

	msg := "**Picked:** " + parsed[rand.Intn(len(parsed))]
	first := true
	for len(msg) > 0 {
		chunk := msg
		if len(chunk) > discord.MaxContentLength {
			chunk = msg[:discord.MaxContentLength]
		}
		msg = msg[len(chunk):]
		var err error
		if first {
			err = ic.Reply(interaction.Response{Content: chunk})
		} else {
			err = ic.FollowUp(interaction.Response{Content: chunk})
		}
		if err != nil {
			return err
		}
		first = false
	}
	return nil
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
