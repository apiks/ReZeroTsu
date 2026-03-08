package joke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const jokeURL = "https://v2.jokeapi.dev/joke/Any?blacklistFlags=nsfw,religious,political,racist,sexist,explicit"

const embedTitleJoke = "Joke"

type jokeAPIResponse struct {
	ID       int    `json:"id"`
	Type     string `json:"type"`
	Joke     string `json:"joke"`
	Setup    string `json:"setup"`
	Delivery string `json:"delivery"`
}

func init() {
	commands.Add(&commands.Command{
		Name:       "joke",
		Desc:       "Display a random joke.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Handler:    handleJoke,
	})
	commands.AddMessageCommand(&commands.MessageCommand{
		Name:       "joke",
		Permission: commands.PermEveryone,
		Context:    commands.ContextDMOnly,
		MessageHandler: func(ctx context.Context, db *database.Client, s *discordgo.Session, m *discordgo.MessageCreate, args string, runtime commands.BotRuntime) {
			jokeStr, err := fetchJoke(ctx)
			if err != nil {
				logger.For("commands").Error("joke fetch failed", "channel_id", m.ChannelID, "err", err)
				emb := commands.NewEmbed(s, embedTitleJoke, "Joke website is not working properly. Please notify apiks about it.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			if jokeStr == "" {
				emb := commands.NewEmbed(s, embedTitleJoke, "Could not get a joke this time.")
				_, _ = s.ChannelMessageSendEmbed(m.ChannelID, emb)
				return
			}
			_, _ = s.ChannelMessageSend(m.ChannelID, jokeStr)
		},
	})
}

func handleJoke(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	jokeStr, err := fetchJoke(ctx)
	if err != nil {
		logger.For("commands").Error("joke fetch failed", "guild_id", i.GuildID, "err", err)
		commands.RespondEmbed(s, i, embedTitleJoke, "Joke website is not working properly. Please notify apiks about it.")
		return
	}
	if jokeStr == "" {
		commands.RespondEmbed(s, i, embedTitleJoke, "Could not get a joke this time.")
		return
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &jokeStr}); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
		logger.For("commands").Error("joke edit failed", args...)
	}
}

func fetchJoke(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jokeURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpclient.Default().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var j jokeAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		return "", err
	}

	switch j.Type {
	case "single":
		return j.Joke, nil
	case "twopart":
		return fmt.Sprintf("%s\n\n%s", j.Setup, j.Delivery), nil
	default:
		return "", nil
	}
}
