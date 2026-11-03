package interactiontest

import (
	"fmt"
	"unicode/utf8"

	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// Limits Discord enforces on a payload and the fake did not, so a handler that overran one passed its tests and
// failed in production.
const (
	maxEmbedTotal     = 6000 // every text field of every embed in one message
	maxCustomIDLength = 100
	maxButtonLabel    = 80
	maxOptionText     = 100 // select option label, value and description
	maxOptions        = 25
	maxChoices        = 25
	maxChoiceText     = 100
	maxFormTitle      = 45
	maxFormLabel      = 45
	maxFormTextBox    = 4000
)

// violations lists each Discord limit the response breaks, in plain words. An empty result means Discord would
// accept it.
func violations(r interaction.Response) []string {
	var out []string
	over := func(what string, got, limit int) {
		if got > limit {
			out = append(out, fmt.Sprintf("%s is %d characters, Discord's limit is %d", what, got, limit))
		}
	}
	over("message content", utf8.RuneCountInString(r.Content), discord.MaxContentLength)
	if len(r.Embeds) > discord.MaxEmbedsPerMessage {
		out = append(out, fmt.Sprintf("%d embeds, Discord's limit is %d", len(r.Embeds), discord.MaxEmbedsPerMessage))
	}
	total := 0
	for _, e := range r.Embeds {
		if e == nil {
			continue
		}
		count := func(what string, s string, limit int) {
			n := utf8.RuneCountInString(s)
			total += n
			over(what, n, limit)
		}
		count("embed title", e.Title, discord.MaxEmbedTitleLength)
		count("embed description", e.Description, discord.MaxEmbedDescriptionLength)
		if e.Footer != nil {
			count("embed footer", e.Footer.Text, discord.MaxEmbedFooterLength)
		}
		if e.Author != nil {
			count("embed author name", e.Author.Name, discord.MaxEmbedAuthorNameLength)
		}
		if len(e.Fields) > discord.MaxEmbedFields {
			out = append(out, fmt.Sprintf("%d embed fields, Discord's limit is %d", len(e.Fields), discord.MaxEmbedFields))
		}
		for _, f := range e.Fields {
			if f == nil {
				continue
			}
			count("embed field name", f.Name, discord.MaxEmbedFieldNameLength)
			count("embed field value", f.Value, discord.MaxEmbedFieldValueLength)
		}
	}
	over("all embed text together", total, maxEmbedTotal)
	out = append(out, componentViolations(r.Components)...)
	return out
}

func componentViolations(components []discordgo.MessageComponent) []string {
	var out []string
	over := func(what string, got, limit int) {
		if got > limit {
			out = append(out, fmt.Sprintf("%s is %d characters, Discord's limit is %d", what, got, limit))
		}
	}
	for _, c := range components {
		switch v := c.(type) {
		case discordgo.ActionsRow:
			out = append(out, componentViolations(v.Components)...)
		case *discordgo.ActionsRow:
			out = append(out, componentViolations(v.Components)...)
		case discordgo.Button:
			over("button custom ID", utf8.RuneCountInString(v.CustomID), maxCustomIDLength)
			over("button label", utf8.RuneCountInString(v.Label), maxButtonLabel)
		case *discordgo.Button:
			out = append(out, componentViolations([]discordgo.MessageComponent{*v})...)
		case discordgo.SelectMenu:
			over("select menu custom ID", utf8.RuneCountInString(v.CustomID), maxCustomIDLength)
			if len(v.Options) > maxOptions {
				out = append(out, fmt.Sprintf("%d select options, Discord's limit is %d", len(v.Options), maxOptions))
			}
			for _, o := range v.Options {
				over("select option label", utf8.RuneCountInString(o.Label), maxOptionText)
				over("select option value", utf8.RuneCountInString(o.Value), maxOptionText)
				over("select option description", utf8.RuneCountInString(o.Description), maxOptionText)
			}
		case *discordgo.SelectMenu:
			out = append(out, componentViolations([]discordgo.MessageComponent{*v})...)
		}
	}
	return out
}

// formViolations lists each Discord limit the form breaks.
func formViolations(f interaction.Form) []string {
	var out []string
	over := func(what string, got, limit int) {
		if got > limit {
			out = append(out, fmt.Sprintf("%s is %d characters, Discord's limit is %d", what, got, limit))
		}
	}
	over("form custom ID", utf8.RuneCountInString(f.CustomID), maxCustomIDLength)
	over("form title", utf8.RuneCountInString(f.Title), maxFormTitle)
	for _, c := range f.Components {
		var label *discordgo.Label
		switch v := c.(type) {
		case discordgo.Label:
			label = &v
		case *discordgo.Label:
			label = v
		}
		if label == nil {
			continue
		}
		over("form label", utf8.RuneCountInString(label.Label), maxFormLabel)
		var input *discordgo.TextInput
		switch v := label.Component.(type) {
		case discordgo.TextInput:
			input = &v
		case *discordgo.TextInput:
			input = v
		case discordgo.SelectMenu, *discordgo.SelectMenu:
			out = append(out, componentViolations([]discordgo.MessageComponent{v})...)
		}
		if input != nil {
			over("form text box custom ID", utf8.RuneCountInString(input.CustomID), maxCustomIDLength)
			over("form text box limit", input.MaxLength, maxFormTextBox)
			over("form text box prefilled text", utf8.RuneCountInString(input.Value), maxFormTextBox)
		}
	}
	return out
}

// choiceViolations lists each Discord limit the autocomplete choices break.
func choiceViolations(choices []*discordgo.ApplicationCommandOptionChoice) []string {
	var out []string
	if len(choices) > maxChoices {
		out = append(out, fmt.Sprintf("%d autocomplete choices, Discord's limit is %d", len(choices), maxChoices))
	}
	for _, c := range choices {
		if c == nil {
			continue
		}
		if n := utf8.RuneCountInString(c.Name); n > maxChoiceText {
			out = append(out, fmt.Sprintf("autocomplete choice name is %d characters, Discord's limit is %d", n, maxChoiceText))
		}
		if s, ok := c.Value.(string); ok {
			if n := utf8.RuneCountInString(s); n > maxChoiceText {
				out = append(out, fmt.Sprintf("autocomplete choice value is %d characters, Discord's limit is %d", n, maxChoiceText))
			}
		}
	}
	return out
}
