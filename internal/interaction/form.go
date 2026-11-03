package interaction

import (
	"github.com/bwmarrin/discordgo"
)

// TextBox is a Label-wrapped text box. A paragraph style gives a multi-line box.
func TextBox(customID, label string, style discordgo.TextInputStyle, required bool) *discordgo.Label {
	return &discordgo.Label{Label: label, Component: discordgo.TextInput{CustomID: customID, Style: style, Required: &required}}
}

// Picker is a Label-wrapped picker: pass a discordgo.SelectMenu of any menu type.
func Picker(label, description string, menu discordgo.SelectMenu) *discordgo.Label {
	return &discordgo.Label{Label: label, Description: description, Component: menu}
}

// FormValue is the text typed into the submitted form's text box with the given custom ID, or "" when there
// is none.
func (c *Context) FormValue(customID string) string {
	if c.Submitted == nil {
		return ""
	}
	if t, ok := findField(c.Submitted.Components, customID).(discordgo.TextInput); ok {
		return t.Value
	}
	return ""
}

// FormValues is the options chosen in the submitted form's picker with the given custom ID, or nil when there
// is none.
func (c *Context) FormValues(customID string) []string {
	if c.Submitted == nil {
		return nil
	}
	if m, ok := findField(c.Submitted.Components, customID).(discordgo.SelectMenu); ok {
		return m.Values
	}
	return nil
}

// findField returns the text box (as a discordgo.TextInput) or picker (as a discordgo.SelectMenu) with the
// given custom ID, looking inside action rows and labels, or nil.
func findField(components []discordgo.MessageComponent, customID string) discordgo.MessageComponent {
	for _, comp := range components {
		var found discordgo.MessageComponent
		switch v := comp.(type) {
		case *discordgo.ActionsRow:
			found = findField(v.Components, customID)
		case discordgo.ActionsRow:
			found = findField(v.Components, customID)
		case *discordgo.Label:
			found = findField([]discordgo.MessageComponent{v.Component}, customID)
		case discordgo.Label:
			found = findField([]discordgo.MessageComponent{v.Component}, customID)
		case *discordgo.TextInput:
			if v.CustomID == customID {
				found = *v
			}
		case discordgo.TextInput:
			if v.CustomID == customID {
				found = v
			}
		case *discordgo.SelectMenu:
			if v.CustomID == customID {
				found = *v
			}
		case discordgo.SelectMenu:
			if v.CustomID == customID {
				found = v
			}
		}
		if found != nil {
			return found
		}
	}
	return nil
}

// TargetUser is the user a user right-click command was run on, or nil.
func (c *Context) TargetUser() *discordgo.User {
	if c.Command.Resolved == nil {
		return nil
	}
	return c.Command.Resolved.Users[c.Command.TargetID]
}

// TargetMessage is the message a message right-click command was run on, or nil.
func (c *Context) TargetMessage() *discordgo.Message {
	if c.Command.Resolved == nil {
		return nil
	}
	return c.Command.Resolved.Messages[c.Command.TargetID]
}
