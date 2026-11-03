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
	commands.AddComponent(pickPrefix, handlePick)
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

// pickPrefix starts the custom ID of the day picker: schedpick:<author ID>. The chosen date arrives as the
// selected value, so the custom ID stays short. It does not start with navPrefix.
const pickPrefix = "schedpick:"

// pickerCustomID is the custom ID of the day picker of the message of authorID.
func pickerCustomID(authorID string) string { return pickPrefix + authorID }

// pickerComponent builds the day picker: one option per loaded date, today marked, shown preselected.
func pickerComponent(shown time.Time, authorID string) discordgo.MessageComponent {
	first, last := animeschedule.LoadedRange()
	today := animeschedule.DateKey(animeschedule.Today())
	var options []discordgo.SelectMenuOption
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		key := animeschedule.DateKey(d)
		label := d.Format("Mon 2 Jan")
		if key == today {
			label += " (today)"
		}
		options = append(options, discordgo.SelectMenuOption{Label: label, Value: key, Default: key == animeschedule.DateKey(shown)})
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    pickerCustomID(authorID),
		Placeholder: "Jump to a day",
		Options:     options,
	}}}
}

// pageComponents builds the controls of the page of date: the day picker above the Previous and Next buttons.
func pageComponents(date time.Time, authorID string) []discordgo.MessageComponent {
	return append([]discordgo.MessageComponent{pickerComponent(date, authorID)}, navComponents(date, authorID)...)
}

// handlePick handles a choice in the day picker, by the same press rule as the buttons.
func handlePick(ic *interaction.Context, _ *discordgo.Session) error {
	authorID, ok := strings.CutPrefix(ic.CustomID, pickPrefix)
	if !ok || len(ic.Values) != 1 {
		return ic.Refuse(msgDayNotLoaded)
	}
	date, err := time.Parse("2006-01-02", ic.Values[0])
	if err != nil {
		return ic.Refuse(msgDayNotLoaded)
	}
	return commands.RespondToPress(ic, authorID, renderPage(ic, date, authorID))
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
		Components: pageComponents(date, authorID),
	}}
}
