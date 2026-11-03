package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

const (
	embedTitleUnavailable = "Anime Schedule"
	msgNotConfigured      = "Schedule unavailable. API key is not configured."
	msgTryAgainMinutes    = "Schedule temporarily unavailable. Try again in a few minutes."
	msgTryAgainLater      = "Schedule temporarily unavailable. Try again later."
	msgUnknownDay         = "I don't know that day. Pick a suggestion, or type a day like monday, tue or tomorrow."
)

var dayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Deferred,
		Name:       "schedule",
		Desc:       "Shows anime release times for today, tomorrow or a given day (subbed where possible).",
		Context:    commands.ContextBoth,
		Module:     "schedule",
		Installs:   commands.BothInstalls(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionString,
				Name:         "day",
				Description:  "A day: today, tomorrow, or a weekday like monday or tue (its next occurrence). Omit for today.",
				Required:     false,
				Autocomplete: true,
			},
		},
		Run:                handleSchedule,
		AutocompleteOption: autocompleteScheduleDay,
	})
	commands.RegisterCopySafePaginationRenderer("schedule", RenderSchedulePage)

}

// autocompleteScheduleDay offers Today, Tomorrow, then the next seven UTC days by date. A dated choice carries
// its date key as the value, so a picked choice resolves to that date.
func autocompleteScheduleDay(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	today := animeschedule.Today()
	options := []*discordgo.ApplicationCommandOptionChoice{
		{Name: "Today", Value: "today"},
		{Name: "Tomorrow", Value: "tomorrow"},
	}
	for n := range 7 {
		d := today.AddDate(0, 0, n)
		options = append(options, &discordgo.ApplicationCommandOptionChoice{
			Name:  fmt.Sprintf("%s %s", dayNames[d.Weekday()], d.Format("2 Jan")),
			Value: animeschedule.DateKey(d),
		})
	}
	matched := commands.MatchAnywhere(options, focusedValue, func(c *discordgo.ApplicationCommandOptionChoice) []string { return []string{c.Name} })
	if len(matched) == 0 {
		return options
	}
	return matched
}

// resolveDay maps the day option to a UTC date, as midnight UTC. It accepts today, tomorrow, a weekday name
// (its next occurrence, today included) and a date key such as an autocomplete choice carries.
func resolveDay(input string, today time.Time) (time.Time, bool) {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "today":
		return today, true
	case "tomorrow":
		return today.AddDate(0, 0, 1), true
	}
	if weekday := parseDay(input); weekday >= 0 {
		return today.AddDate(0, 0, (weekday-int(today.Weekday())+7)%7), true
	}
	if d, err := time.Parse("2006-01-02", strings.TrimSpace(input)); err == nil {
		return d, true
	}
	return time.Time{}, false
}

// parseDay maps input to weekday 0=Sunday..6=Saturday; -1 if invalid.
func parseDay(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "sunday", "sundays", "sun":
		return 0
	case "monday", "mondays", "mon":
		return 1
	case "tuesday", "tuesdays", "tue", "tues":
		return 2
	case "wednesday", "wednesdays", "wed":
		return 3
	case "thursday", "thursdays", "thu", "thurs", "thur":
		return 4
	case "friday", "fridays", "fri":
		return 5
	case "saturday", "saturdays", "sat":
		return 6
	default:
		return -1
	}
}

// BuildScheduleEmbed builds the schedule embed for a UTC date and its show list.
func BuildScheduleEmbed(date time.Time, shows []animeschedule.ShowEntry) *discordgo.MessageEmbed {
	date = date.UTC()
	titleWithDate := fmt.Sprintf("%s, %s", dayNames[date.Weekday()], date.Format("2 Jan 2006"))

	var desc strings.Builder
	if len(shows) == 0 {
		desc.WriteString("No anime scheduled for this day. Use the buttons below to check other days.")
	} else {
		for _, e := range shows {
			airLabel := "SUB"
			if e.AirType == "raw" {
				airLabel = "RAW"
			}
			line := fmt.Sprintf("**%s** — %s %s", e.Name, e.Episode, airLabel)
			if e.Delayed != "" {
				line += " " + e.Delayed
			}
			line += fmt.Sprintf(" · <t:%d:t>\n", e.AirTimeUnix)
			desc.WriteString(line)
		}
	}
	description := desc.String()
	if len(description) > discord.MaxEmbedDescriptionLength {
		suffix := "\n\n… and more."
		maxLen := discord.MaxEmbedDescriptionLength - len(suffix)
		if maxLen < 200 {
			maxLen = discord.MaxEmbedDescriptionLength - 80
		}
		description = description[:maxLen] + suffix
		if len(description) > discord.MaxEmbedDescriptionLength {
			description = description[:discord.MaxEmbedDescriptionLength-1] + "…"
		}
	}

	footerText := "AnimeSchedule.net"
	if len(shows) == 0 {
		footerText += " · 0 shows"
	} else if len(shows) == 1 {
		footerText += " · 1 show"
	} else {
		footerText += fmt.Sprintf(" · %d shows", len(shows))
	}
	emb := &discordgo.MessageEmbed{
		Title:       titleWithDate,
		URL:         animeschedule.BaseURL,
		Description: description,
		Color:       discord.EmbedColor,
		Author: &discordgo.MessageEmbedAuthor{
			Name: "AnimeSchedule.net",
			URL:  animeschedule.BaseURL,
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: footerText,
		},
	}
	return emb
}

// donghuaSetting reports whether the server shows donghua. Where the bot has no server membership (DMs, and a
// user install in a server it has not joined) there are no settings, and donghua is shown.
func donghuaSetting(ctx context.Context, db *database.Client, i *discordgo.Interaction) bool {
	if !interaction.BotInGuild(i) {
		return true
	}
	settings, err := commands.GetSettings(ctx, db, i.GuildID)
	if err == nil && settings != nil {
		return settings.Donghua
	}
	return true
}

// handleSchedule answers /schedule with one day's schedule and Previous/Next buttons. Refusals are private.
func handleSchedule(ic *interaction.Context) error {
	if !animeschedule.APIKeyConfigured() {
		return ic.Refuse(msgNotConfigured)
	}
	if !animeschedule.HasData() {
		return ic.Refuse(msgTryAgainMinutes)
	}

	date := animeschedule.Today()
	if opt := commands.ParseOption(ic.Command.Options, "day"); opt != nil && opt.StringValue() != "" {
		resolved, ok := resolveDay(opt.StringValue(), date)
		if !ok {
			return ic.Refuse(msgUnknownDay)
		}
		date = resolved
	}

	shows := animeschedule.GetDayShows(date, donghuaSetting(ic.Context(), ic.DB, ic.Interaction))
	return ic.EditReply(interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{BuildScheduleEmbed(date, shows)},
		Components: commands.BuildSchedulePaginationComponents(int(date.Weekday()), ic.Invoker.ID),
	})
}

// RenderSchedulePage renders one day's schedule for pagination.
func RenderSchedulePage(ctx context.Context, db *database.Client, _ *discordgo.Session, i *discordgo.InteractionCreate, page, total int, authorID string) commands.Page {
	if !animeschedule.HasData() {
		emb := commands.NewEmbedWithFields(nil, embedTitleUnavailable, msgTryAgainLater, nil)
		return commands.Page{Response: interaction.Response{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: []discordgo.MessageComponent{},
		}}
	}

	weekday := min(max(page, 0), 6)
	date := animeschedule.DateInWeek(time.Weekday(weekday))
	shows := animeschedule.GetDayShows(date, donghuaSetting(ctx, db, i.Interaction))
	return commands.Page{Response: interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{BuildScheduleEmbed(date, shows)},
		Components: commands.BuildSchedulePaginationComponents(weekday, authorID),
	}}
}
