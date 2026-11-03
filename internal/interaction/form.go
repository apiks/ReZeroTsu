package interaction

import (
	"encoding/json"

	"github.com/bwmarrin/discordgo"
)

// LabelComponent is Discord's component type for a Label, the wrapper every field of a form sits in.
// discordgo v0.29.0 has no Label, so it is defined here.
const LabelComponent discordgo.ComponentType = 18

// Label wraps one form field (a text box or a picker) with a title and an optional description.
type Label struct {
	Label       string
	Description string
	Component   discordgo.MessageComponent
}

// Type returns the Label component type.
func (Label) Type() discordgo.ComponentType { return LabelComponent }

// MarshalJSON writes the Label in Discord's payload form.
func (l Label) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type        discordgo.ComponentType    `json:"type"`
		Label       string                     `json:"label"`
		Description string                     `json:"description,omitempty"`
		Component   discordgo.MessageComponent `json:"component"`
	}{LabelComponent, l.Label, l.Description, l.Component})
}

// TextBox is a Label-wrapped text box. A paragraph style gives a multi-line box.
func TextBox(customID, label string, style discordgo.TextInputStyle, required bool) *Label {
	return &Label{Label: label, Component: discordgo.TextInput{CustomID: customID, Style: style, Required: required}}
}

// Picker is a Label-wrapped picker: pass a discordgo.SelectMenu of any menu type.
func Picker(label, description string, menu discordgo.SelectMenu) *Label {
	return &Label{Label: label, Description: description, Component: menu}
}

// FormValue is the text typed into the submitted form's text box with the given custom ID, or "" when there
// is none.
func (c *Context) FormValue(customID string) string {
	if c.Submitted == nil {
		return ""
	}
	return findText(c.Submitted.Components, customID)
}

func findText(components []discordgo.MessageComponent, customID string) string {
	for _, comp := range components {
		switch v := comp.(type) {
		case *discordgo.ActionsRow:
			if s := findText(v.Components, customID); s != "" {
				return s
			}
		case *Label:
			if s := findText([]discordgo.MessageComponent{v.Component}, customID); s != "" {
				return s
			}
		case *discordgo.TextInput:
			if v.CustomID == customID {
				return v.Value
			}
		case discordgo.TextInput:
			if v.CustomID == customID {
				return v.Value
			}
		}
	}
	return ""
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
