package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

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
	msgDayNotOnTimetable  = "That day isn't on the timetable. Pick one of the suggestions."
	msgUnknownDay         = "I don't know that day. Pick a suggestion, or type a day like monday, tue or tomorrow."
)

var dayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// trackOptionChoice is the track option's choice for p: its title and what it falls back to.
func trackOptionChoice(p animeschedule.PreferredTrack) *discordgo.ApplicationCommandOptionChoice {
	return &discordgo.ApplicationCommandOptionChoice{Name: p.Title() + ": " + p.Fallback(), Value: p.String()}
}

func init() {
	commands.Add(&commands.Command{
		Visibility: commands.Public,
		Response:   commands.Deferred,
		Name:       "schedule",
		Desc:       "Anime release times for today, tomorrow or a day, as sub, dub or raw; jump across the loaded weeks.",
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
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "track",
				Description: "Which track to show. Defaults to the server's preferred track, else Sub.",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					trackOptionChoice(animeschedule.PreferSub),
					trackOptionChoice(animeschedule.PreferDub),
					trackOptionChoice(animeschedule.PreferRaw),
				},
			},
		},
		Run:                handleSchedule,
		AutocompleteOption: autocompleteScheduleDay,
	})
}

// autocompleteScheduleDay offers Today and Tomorrow, each labelled with its date, then the five UTC days after
// tomorrow by date. A dated choice carries its date key as the value, so a picked choice resolves to that date.
func autocompleteScheduleDay(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate, optionName, focusedValue string) []*discordgo.ApplicationCommandOptionChoice {
	today := animeschedule.Today()
	label := func(d time.Time) string { return fmt.Sprintf("%s %s", dayNames[d.Weekday()], d.Format("2 Jan")) }
	tomorrow := today.AddDate(0, 0, 1)
	options := []*discordgo.ApplicationCommandOptionChoice{
		{Name: "Today (" + label(today) + ")", Value: "today"},
		{Name: "Tomorrow (" + label(tomorrow) + ")", Value: "tomorrow"},
	}
	for n := 2; n < 7; n++ {
		d := today.AddDate(0, 0, n)
		options = append(options, &discordgo.ApplicationCommandOptionChoice{
			Name:  label(d),
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
	if d, err := animeschedule.ParseDateKey(strings.TrimSpace(input)); err == nil {
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

// scheduleLine renders one entry; onToday adds the live relative time.
func scheduleLine(e animeschedule.ShowEntry, onToday bool) string {
	line := fmt.Sprintf("**%s** — %s %s", e.Name, e.Episode, e.Track.Label())
	if e.Delayed != "" {
		line += " " + e.Delayed
	}
	line += fmt.Sprintf(" · <t:%d:t>", e.AirTimeUnix)
	if onToday {
		line += fmt.Sprintf(" · <t:%d:R>", e.AirTimeUnix)
	}
	if label := e.MediaType.Label(); label != "" {
		line += " · " + label
	}
	return line
}

// scheduleDescription joins the entry lines. When they exceed the embed description limit it keeps the lines
// that fit whole and ends with a linked count of the rest.
func scheduleDescription(lines []string) string {
	full := strings.Join(lines, "\n")
	if utf16Len(full) <= discord.MaxEmbedDescriptionLength {
		return full
	}
	more := func(n int) string {
		return fmt.Sprintf("[… and %d more on AnimeSchedule.net](%s)", n, animeschedule.BaseURL)
	}
	used, kept := 0, 0
	for kept < len(lines) {
		next := used + utf16Len(lines[kept]) + 1 // the line and its newline
		if next+utf16Len(more(len(lines)-kept-1)) > discord.MaxEmbedDescriptionLength {
			break
		}
		used = next
		kept++
	}
	if kept == 0 {
		return more(len(lines))
	}
	return strings.Join(lines[:kept], "\n") + "\n" + more(len(lines)-kept)
}

// utf16Len is the length of s in UTF-16 code units, the unit Discord counts embed text in.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// BuildScheduleEmbed builds the schedule page body for a UTC date and its show list, read under pref. Both
// /schedule and the daily-schedule autopost use it. now is the instant the caller works at: the page of now's UTC
// day adds live relative times. The footer names a Dub or Raw preference.
func BuildScheduleEmbed(date, now time.Time, shows []animeschedule.ShowEntry, pref animeschedule.PreferredTrack) *discordgo.MessageEmbed {
	date = date.UTC()
	titleWithDate := fmt.Sprintf("%s, %s", dayNames[date.Weekday()], date.Format("2 Jan 2006"))

	onToday := animeschedule.DateKey(date) == animeschedule.DateKey(now)

	description := "No anime scheduled for this day. Run " + commands.CommandMention("schedule") + " to check other days."
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
	if pref != animeschedule.PreferSub {
		footerText += " · " + pref.Title() + " preferred"
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

// guildSettings reads a server's settings; tests replace it.
var guildSettings = commands.GetSettings

// trackOption reads the track option; nil when it was omitted, so the server's preferred track applies.
func trackOption(ic *interaction.Context) *animeschedule.PreferredTrack {
	if opt := commands.ParseOption(ic.Command.Options, "track"); opt != nil {
		if pref, ok := animeschedule.ParsePreferredTrack(opt.StringValue()); ok {
			return &pref
		}
	}
	return nil
}

// dayShows reads the shows of date under the track chosen, or the server's preferred track when none was chosen,
// and returns the track used.
func dayShows(ic *interaction.Context, date time.Time, chosen *animeschedule.PreferredTrack) ([]animeschedule.ShowEntry, animeschedule.PreferredTrack) {
	donghua, pref := commands.ServerShowSettings(ic.Context(), ic.DB, ic.Interaction, guildSettings)
	if chosen != nil {
		pref = *chosen
	}
	return animeschedule.GetDayShows(date, donghua, pref), pref
}

// handleSchedule answers /schedule with one day's schedule and Previous/Next buttons. Refusals are private.
func handleSchedule(ic *interaction.Context) error {
	if !animeschedule.APIKeyConfigured() {
		return ic.Refuse(msgNotConfigured)
	}
	if !animeschedule.HasData() {
		return ic.Refuse(msgTryAgainMinutes)
	}

	v := currentView()
	date := animeschedule.DayOf(v.now)
	if opt := commands.ParseOption(ic.Command.Options, "day"); opt != nil && opt.StringValue() != "" {
		resolved, ok := resolveDay(opt.StringValue(), date)
		if !ok {
			return ic.Refuse(msgUnknownDay)
		}
		if !v.holds(resolved) {
			return ic.Refuse(msgDayNotOnTimetable)
		}
		date = resolved
	}

	shows, pref := dayShows(ic, date, trackOption(ic))
	return ic.EditReply(interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{BuildScheduleEmbed(date, v.now, shows, pref)},
		Components: pageComponents(v, date, ic.Invoker.ID, pref),
	})
}
