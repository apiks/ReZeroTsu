package settings

import (
	"context"
	"strings"
	"sync"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// missingPostPermissions names the permissions an autopost needs in the channel that the bot lacks: View
// Channel, Embed Links, and Send Messages (Send Messages in Threads for a thread). It returns nil when nothing is
// missing, the bot is an Administrator or the permissions cannot be read.
func missingPostPermissions(ic *interaction.Context, channelID string) []string {
	perms, err := ic.BotChannelPermissions(channelID)
	if err != nil || perms&discordgo.PermissionAdministrator != 0 {
		return nil
	}
	send, sendName := int64(discordgo.PermissionSendMessages), "Send Messages"
	if ch, err := ic.Channel(channelID); err == nil && ch != nil &&
		(ch.Type == discordgo.ChannelTypeGuildPublicThread || ch.Type == discordgo.ChannelTypeGuildPrivateThread) {
		send, sendName = discordgo.PermissionSendMessagesInThreads, "Send Messages in Threads"
	}
	var missing []string
	for _, p := range []struct {
		bit  int64
		name string
	}{
		{discordgo.PermissionViewChannel, "View Channel"},
		{send, sendName},
		{discordgo.PermissionEmbedLinks, "Embed Links"},
	} {
		if perms&p.bit == 0 {
			missing = append(missing, p.name)
		}
	}
	return missing
}

// channelWarnings is the panel lines for autopost channels the bot cannot post in, in the order given. The
// lookups run together under one bounded wait, and an unreadable or slow lookup gives no line.
func channelWarnings(ic *interaction.Context, aps ...*guilds.Autopost) []string {
	lines := make([]string, len(aps))
	var wg sync.WaitGroup
	for i, ap := range aps {
		if ap == nil || ap.ID == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if missing := missingPostPermissions(ic, ap.ID); len(missing) > 0 {
				lines[i] = warningMarker + " the bot cannot post in " + commands.ChannelMention(ap.ID) + ", it is missing " + strings.Join(missing, ", ") + "."
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	ctx, cancel := context.WithTimeout(ic.Context(), warningLookupTimeout)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
		return nil
	}
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
