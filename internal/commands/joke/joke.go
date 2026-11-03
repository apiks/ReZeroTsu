package joke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/httpclient"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"
)

const jokeURL = "https://v2.jokeapi.dev/joke/Any?blacklistFlags=nsfw,religious,political,racist,sexist,explicit"

type jokeAPIResponse struct {
	ID       int    `json:"id"`
	Type     string `json:"type"`
	Joke     string `json:"joke"`
	Setup    string `json:"setup"`
	Delivery string `json:"delivery"`
}

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Direct,
		Name:       "joke",
		Desc:       "Display a random joke.",
		Permission: commands.PermEveryone,
		Module:     "general",
		Installs:   commands.BothInstalls(),
		Run:        func(ic *interaction.Context) error { return runJoke(ic, fetchJoke) },
	})
}

// fetchTimeout keeps the fetch inside Discord's three seconds for a first response, since /joke answers
// directly instead of deferring.
const fetchTimeout = 2500 * time.Millisecond

func runJoke(ic *interaction.Context, fetch func(context.Context) (string, error)) error {
	ctx, cancel := context.WithTimeout(ic.Context(), fetchTimeout)
	defer cancel()
	jokeStr, err := fetch(ctx)
	if err != nil {
		logger.For("commands").Error("joke fetch failed", "guild_id", ic.GuildID, "err", err)
		return ic.Refuse("The joke website is not working properly right now. Please try again later.")
	}
	if jokeStr == "" {
		return ic.Refuse("Could not get a joke this time. Please try again.")
	}
	return ic.Reply(interaction.Response{Content: jokeStr})
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
