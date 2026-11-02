package discord

// ChannelTerminalReason says why a send error means the target channel cannot be posted to, or that it does not.
type ChannelTerminalReason int

const (
	// ChannelNotTerminal means the error says nothing final about the channel.
	ChannelNotTerminal ChannelTerminalReason = iota
	// ChannelDeleted is an unknown channel (10003): the stored ID is unrecoverable.
	ChannelDeleted
	// ChannelInaccessible is missing access (50001): the bot cannot see the channel.
	ChannelInaccessible
	// ChannelNotText is a channel that does not take messages (50008), e.g. a voice channel.
	ChannelNotText
	// ChannelMissingPermissions is a missing permission (50013, or any other 403 response).
	ChannelMissingPermissions
)

func (r ChannelTerminalReason) String() string {
	switch r {
	case ChannelDeleted:
		return "channel deleted (10003)"
	case ChannelInaccessible:
		return "channel inaccessible (50001)"
	case ChannelNotText:
		return "channel is non-text (50008)"
	case ChannelMissingPermissions:
		return "missing permissions (50013)"
	default:
		return "not terminal"
	}
}

// ClassifyChannelTerminal maps a send error to the terminal-channel reason it carries. Missing permissions is
// checked last because IsMissingPermissions also matches any 403 response, whatever its code.
func ClassifyChannelTerminal(err error) ChannelTerminalReason {
	switch {
	case err == nil:
		return ChannelNotTerminal
	case IsUnknownChannel(err):
		return ChannelDeleted
	case IsChannelNotText(err):
		return ChannelNotText
	case IsChannelUnavailable(err):
		return ChannelInaccessible
	case IsMissingPermissions(err):
		return ChannelMissingPermissions
	default:
		return ChannelNotTerminal
	}
}
