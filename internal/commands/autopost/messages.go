package autopost

import "ReZeroTsu/internal/commands"

// newEpisodesMessage words the new-episodes autopost state with the channel
// and the optional ping role as mentions.
func newEpisodesMessage(lead, channelID, roleID string) string {
	msg := lead + " " + commands.ChannelMention(channelID) + "."
	if roleID != "" {
		msg += " Role ping: " + commands.RoleMention(roleID) + "."
	}
	return msg
}

// dailyMessage words the daily-schedule autopost state with the channel as a mention.
func dailyMessage(lead, channelID string) string {
	return lead + " " + commands.ChannelMention(channelID) + "."
}
