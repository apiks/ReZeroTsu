// Package discord provides Discord API constants and error helpers.
package discord

import (
	"errors"

	"github.com/bwmarrin/discordgo"
)

// Discord REST error codes.
const (
	UnknownChannelCode      = 10003 // Channel was deleted; ID is unrecoverable
	UnknownMessageCode      = 10008 // Message was deleted or never existed
	UnknownRoleCode         = 10011 // Role was deleted; ID is unrecoverable
	UnknownUserCode         = 10013 // User does not exist (deleted account)
	MissingAccessCode       = 50001 // Bot not in server or no channel access; recoverable
	CannotDMUserCode        = 50007 // Cannot send messages to this user (DMs disabled or blocked)
	ChannelNotTextCode      = 50008 // Cannot send messages in a non-text channel (e.g. voice)
	MissingPermissionsCode  = 50013 // Bot lacks permission for the action (e.g. Send Messages in channel)
	UnknownWebhookCode      = 10015 // Webhook was deleted
	InvalidWebhookTokenCode = 50027 // Webhook token is no longer valid
)

// IsUserUndeliverable reports whether err means the user can never be DMed: DMs closed or blocked (50007) or
// the user no longer exists (10013). Every other rejection may be transient or bot-side and must not drop data.
func IsUserUndeliverable(err error) bool {
	return IsCannotDMUser(err) || hasCode(err, UnknownUserCode)
}

// restCode returns the Discord error code of the REST error wrapped in err, if any.
func restCode(err error) (int, bool) {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil {
		return 0, false
	}
	return restErr.Message.Code, true
}

// hasCode reports whether err wraps a REST error carrying one of codes.
func hasCode(err error, codes ...int) bool {
	code, ok := restCode(err)
	if !ok {
		return false
	}
	for _, c := range codes {
		if code == c {
			return true
		}
	}
	return false
}

// IsWebhookGone reports whether err says the webhook is unknown (10015) or its token invalid (50027).
func IsWebhookGone(err error) bool { return hasCode(err, UnknownWebhookCode, InvalidWebhookTokenCode) }

// IsUnknownChannel reports whether err is channel-deleted (10003). Use when clearing stored channel IDs.
func IsUnknownChannel(err error) bool { return hasCode(err, UnknownChannelCode) }

// IsUnknownMessage reports whether err is message-deleted or unknown (10008).
func IsUnknownMessage(err error) bool { return hasCode(err, UnknownMessageCode) }

// IsUnknownRole reports whether err is role-deleted (10011).
func IsUnknownRole(err error) bool { return hasCode(err, UnknownRoleCode) }

// IsChannelUnavailable reports whether the channel is unusable (deleted or no access). For clearing stored IDs use IsUnknownChannel.
func IsChannelUnavailable(err error) bool { return hasCode(err, UnknownChannelCode, MissingAccessCode) }

// IsMissingAccess reports whether the bot cannot see the target at all (50001).
func IsMissingAccess(err error) bool { return hasCode(err, MissingAccessCode) }

// IsCannotDMUser reports whether the user cannot receive DMs (50007).
func IsCannotDMUser(err error) bool { return hasCode(err, CannotDMUserCode) }

// IsChannelNotText reports whether the channel is not text (50008). Use when clearing new-episodes autopost.
func IsChannelNotText(err error) bool { return hasCode(err, ChannelNotTextCode) }

// IsMissingPermissions reports whether the bot lacks permission (50013 or Response 403). True even when Message is nil (e.g. webhook 403).
func IsMissingPermissions(err error) bool {
	if hasCode(err, MissingPermissionsCode) {
		return true
	}
	var restErr *discordgo.RESTError
	return errors.As(err, &restErr) && restErr.Response != nil && restErr.Response.StatusCode == 403
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
	MaxEmbedTitleLength       = 256  // Embed title
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
	DefaultInvitePermissions = 1<<6 | // Add Reactions
		1<<10 | // View Channel
		1<<11 | // Send Messages
		1<<13 | // Manage Messages
		1<<14 | // Embed Links
		1<<16 | // Read Message History
		1<<18 | // Use External Emojis
		1<<28 | // Manage Roles
		1<<29 | // Manage Webhooks
		1<<38 | // Send Messages in Threads
		PermissionPinMessages
)

// PermissionPinMessages is Discord's Pin Messages permission (bit 51). Discord's changelog entry "Pin Messages
// permission, required from 2026-02-23" (https://docs.discord.com/developers/change-log) made it a requirement
// for pinning, and the permission list in the docs gives its bit. discordgo v0.29.0 does not define it.
const PermissionPinMessages int64 = 1 << 51

const SupportServerURL = "https://discord.gg/BDT8Twv"

const RepoURL = "https://github.com/apiks/ReZeroTsu"
