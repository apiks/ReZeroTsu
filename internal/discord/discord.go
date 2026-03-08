// Package discord provides Discord API constants and error helpers.
package discord

import (
	"errors"

	"github.com/bwmarrin/discordgo"
)

// Discord REST error codes.
const (
	UnknownChannelCode     = 10003 // Channel was deleted; ID is unrecoverable
	UnknownMessageCode     = 10008 // Message was deleted or never existed
	UnknownRoleCode        = 10011 // Role was deleted; ID is unrecoverable
	MissingAccessCode      = 50001 // Bot not in server or no channel access; recoverable
	CannotDMUserCode       = 50007 // Cannot send messages to this user (DMs disabled or blocked)
	ChannelNotTextCode     = 50008 // Cannot send messages in a non-text channel (e.g. voice)
	MissingPermissionsCode = 50013 // Bot lacks permission for the action (e.g. Send Messages in channel)
)

// IsUnknownChannel reports whether err is channel-deleted (10003). Use when clearing stored channel IDs.
func IsUnknownChannel(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil {
		return false
	}
	return restErr.Message.Code == UnknownChannelCode
}

// IsUnknownMessage reports whether err is message-deleted or unknown (10008).
func IsUnknownMessage(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil {
		return false
	}
	return restErr.Message.Code == UnknownMessageCode
}

// IsUnknownRole reports whether err is role-deleted (10011).
func IsUnknownRole(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil {
		return false
	}
	return restErr.Message.Code == UnknownRoleCode
}

// IsChannelUnavailable reports whether the channel is unusable (deleted or no access). For clearing stored IDs use IsUnknownChannel.
func IsChannelUnavailable(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil {
		return false
	}
	code := restErr.Message.Code
	return code == UnknownChannelCode || code == MissingAccessCode
}

// IsCannotDMUser reports whether the user cannot receive DMs (50007).
func IsCannotDMUser(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil {
		return false
	}
	return restErr.Message.Code == CannotDMUserCode
}

// IsChannelNotText reports whether the channel is not text (50008). Use when clearing new-episodes autopost.
func IsChannelNotText(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil {
		return false
	}
	return restErr.Message.Code == ChannelNotTextCode
}

// IsMissingPermissions reports whether the bot lacks permission (50013 or Response 403). True even when Message is nil (e.g. webhook 403).
func IsMissingPermissions(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) {
		return false
	}
	if restErr.Message != nil && restErr.Message.Code == MissingPermissionsCode {
		return true
	}
	if restErr.Response != nil && restErr.Response.StatusCode == 403 {
		return true
	}
	return false
}

// RESTAttrs returns slog-style key-value pairs (code, message) for a REST error, or nil.
func RESTAttrs(err error) []any {
	var restErr *discordgo.RESTError
	if err == nil || !errors.As(err, &restErr) || restErr.Message == nil {
		return nil
	}
	return []any{"code", restErr.Message.Code, "message", restErr.Message.Message}
}

// Embed and message limits (Discord API).
const (
	MaxContentLength          = 2000 // Message content
	MaxEmbedDescriptionLength = 4096 // Embed description
	MaxEmbedAuthorNameLength  = 256  // Embed author name
	MaxEmbedFooterLength      = 2048 // Embed footer text
	MaxEmbedFieldNameLength   = 256  // Embed field name
	MaxEmbedFieldValueLength  = 1024 // Embed field value
	MaxEmbedFields            = 25   // Fields per embed
	MaxEmbedsPerMessage       = 10   // Embeds per message
)

// EmbedColor is the default embed accent (light pink).
const EmbedColor = 0xF8B4D9

const (
	InviteURLFormat          = "https://discord.com/oauth2/authorize?client_id=%s&scope=bot%%20applications.commands&permissions=%d"
	DefaultInvitePermissions = 335883328
)

const SupportServerURL = "https://discord.gg/BDT8Twv"
