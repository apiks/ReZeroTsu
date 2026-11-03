package schedule

import (
	"strings"
	"time"

	"ReZeroTsu/internal/animeschedule"
	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

const (
	msgDayNotLoaded = "That day is no longer on the timetable. Run /schedule again."

	// navPrefix starts the custom ID of a schedule navigation button: sched:<UTC date>:<author ID>. A date and
	// a user ID keep it far under Discord's 100-character limit.
	navPrefix = "sched:"
)

func init() {
	commands.AddComponent(navPrefix, handleNav)
}

// navCustomID is the custom ID of a control that opens the schedule page of date for the message of authorID.
func navCustomID(date time.Time, authorID string) string {
	return navPrefix + animeschedule.DateKey(date) + ":" + authorID
}

// parseNavCustomID reads a navigation custom ID; ok is false when it is malformed.
func parseNavCustomID(customID string) (date time.Time, authorID string, ok bool) {
	rest, found := strings.CutPrefix(customID, navPrefix)
	if !found {
		return time.Time{}, "", false
	}
	day, authorID, found := strings.Cut(rest, ":")
	if !found {
		return time.Time{}, "", false
	}
	date, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Time{}, "", false
	}
	return date, authorID, true
}

// navComponents builds the Previous and Next buttons for the page of date. Previous is disabled on the first
// loaded date and Next on the last.
func navComponents(date time.Time, authorID string) []discordgo.MessageComponent {
	first, last := animeschedule.LoadedRange()
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{CustomID: navCustomID(date.AddDate(0, 0, -1), authorID), Label: "Previous", Style: discordgo.SecondaryButton, Disabled: !date.After(first)},
		discordgo.Button{CustomID: navCustomID(date.AddDate(0, 0, 1), authorID), Label: "Next", Style: discordgo.SecondaryButton, Disabled: !date.Before(last)},
	}}}
}

// inLoadedRange reports whether the timetable holds date.
func inLoadedRange(date time.Time) bool {
	first, last := animeschedule.LoadedRange()
	return !date.Before(first) && !date.After(last)
}

// handleNav handles a press on a schedule navigation control: it opens the page of the date the control
// carries, by the press rule shared with the paginated lists.
func handleNav(ic *interaction.Context, _ *discordgo.Session) error {
	date, authorID, ok := parseNavCustomID(ic.CustomID)
	if !ok {
		return ic.Refuse(msgDayNotLoaded)
	}
	return commands.RespondToPress(ic, authorID, renderPage(ic, date, authorID))
}

// renderPage builds the page of date for the message of authorID, or a refusal when the date is not loaded.
func renderPage(ic *interaction.Context, date time.Time, authorID string) commands.Page {
	if !animeschedule.HasData() {
		emb := commands.NewEmbedWithFields(nil, embedTitleUnavailable, msgTryAgainLater, nil)
		return commands.Page{Response: interaction.Response{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: []discordgo.MessageComponent{},
		}}
	}
	if !inLoadedRange(date) {
		return commands.Page{Refusal: msgDayNotLoaded}
	}
	shows := animeschedule.GetDayShows(date, donghuaSetting(ic.Context(), ic.DB, ic.Interaction))
	return commands.Page{Response: interaction.Response{
		Embeds:     []*discordgo.MessageEmbed{BuildScheduleEmbed(date, shows)},
		Components: navComponents(date, authorID),
	}}
}
