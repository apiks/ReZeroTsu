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
	msgCannotParseDay     = "Cannot parse that day."
)

var dayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Deferred,
		Name:       "schedule",
		Desc:       "Shows today's (or a given day's) anime release times (subbed where possible).",
		Context:    commands.ContextBoth,
		Module:     "schedule",
		Installs:   commands.BothInstalls(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionString,
				Name:         "day",
				Description:  "The target day (e.g. monday, tue, sat). Omit for today.",
				Required:     false,
				Autocomplete: true,
			},
		},
		Run:                handleSchedule,
		AutocompleteOption: autocompleteScheduleDay,
	})
}

func autocompleteScheduleDay(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	names := commands.MatchAnywhere(dayNames, focusedValue, func(n string) []string { return []string{n} })
	if len(names) == 0 {
		names = dayNames
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(names))
	for _, name := range names {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: name})
	}
	return choices
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

// mediaTypeLabels names the non-TV media types; TV carries no label.
var mediaTypeLabels = map[animeschedule.MediaType]string{
	animeschedule.MediaTVShort: "TV Short",
	animeschedule.MediaONA:     "ONA",
	animeschedule.MediaDonghua: "Donghua",
	animeschedule.MediaOVA:     "OVA",
	animeschedule.MediaSpecial: "Special",
	animeschedule.MediaMovie:   "Movie",
}

// scheduleLine renders one entry; onToday adds the live relative time.
func scheduleLine(e animeschedule.ShowEntry, onToday bool) string {
	airLabel := "SUB"
	if e.AirType == "raw" {
		airLabel = "RAW"
	}
	line := fmt.Sprintf("**%s** — %s %s", e.Name, e.Episode, airLabel)
	if e.Delayed != "" {
		line += " " + e.Delayed
	}
	line += fmt.Sprintf(" · <t:%d:t>", e.AirTimeUnix)
	if onToday {
		line += fmt.Sprintf(" · <t:%d:R>", e.AirTimeUnix)
	}
	if label := mediaTypeLabels[e.MediaType]; label != "" {
		line += " · " + label
	}
	return line
}

// scheduleDescription joins the entry lines. When they exceed the embed description limit it keeps the lines
// that fit whole and ends with a linked count of the rest.
func scheduleDescription(lines []string) string {
	full := strings.Join(lines, "\n")
	if len(full) <= discord.MaxEmbedDescriptionLength {
		return full
	}
	more := func(n int) string {
		return fmt.Sprintf("[… and %d more on AnimeSchedule.net](%s)", n, animeschedule.BaseURL)
	}
	used, kept := 0, 0
	for kept < len(lines) {
		next := used + len(lines[kept]) + 1 // the line and its newline
		if next+len(more(len(lines)-kept-1)) > discord.MaxEmbedDescriptionLength {
			break
		}
		used = next
		kept++
	}
	return strings.Join(lines[:kept], "\n") + "\n" + more(len(lines)-kept)
}

// BuildScheduleEmbed builds the schedule page body for a UTC date and its show list. Both /schedule and the
// daily-schedule autopost use it. Today's page adds live relative times.
func BuildScheduleEmbed(date time.Time, shows []animeschedule.ShowEntry) *discordgo.MessageEmbed {
	date = date.UTC()
	titleWithDate := fmt.Sprintf("%s, %s", dayNames[date.Weekday()], date.Format("2 Jan 2006"))

	onToday := animeschedule.DateKey(date) == animeschedule.DateKey(animeschedule.Now())

	description := "No anime scheduled for this day. Use the buttons below to check other days."
	if len(shows) > 0 {
		lines := make([]string, len(shows))
		for i, e := range shows {
			lines[i] = scheduleLine(e, onToday)
		}
		description = scheduleDescription(lines)
	}

	footerText := fmt.Sprintf("%d shows", len(shows))
	if len(shows) == 1 {
		footerText = "1 show"
	}
	footerText += " · days run on UTC, times show in your time zone"
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
		weekday := parseDay(opt.StringValue())
		if weekday < 0 || weekday > 6 {
			return ic.Refuse(msgCannotParseDay)
		}
		date = animeschedule.DateInWeek(time.Weekday(weekday))
	}

	shows := animeschedule.GetDayShows(date, donghuaSetting(ic.Context(), ic.DB, ic.Interaction))
	return ic.EditReply(interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{BuildScheduleEmbed(date, shows)},
		Components: navComponents(date, ic.Invoker.ID),
	})
}
