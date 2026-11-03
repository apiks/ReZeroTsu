package settings

import (
	"context"
	"strings"
	"sync"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/database/guilds"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"

	"github.com/bwmarrin/discordgo"
)

// missingPostPermissions names the permissions an autopost needs in the channel that the bot lacks: View
// Channel, Embed Links, and Send Messages (Send Messages in Threads for a thread). It returns nil when nothing is
// missing, the bot is an Administrator or the permissions cannot be read.
func missingPostPermissions(ic *interaction.Context, channelID string) []string {
	perms, err := ic.BotChannelPermissions(channelID)
	if err != nil {
		return nil
	}
	thread, known := channelIsThread(ic, channelID)
	return discord.MissingNames(perms, []discord.NamedPermission{
		{Bit: discordgo.PermissionViewChannel, Name: "View Channel"},
		discord.SendPermission(perms, thread, known),
		{Bit: discordgo.PermissionEmbedLinks, Name: "Embed Links"},
	})
}

// channelIsThread reports whether the channel is a thread, and whether that is known. A channel the picker just
// resolved carries its type; otherwise the type comes from a lookup, and a failed lookup leaves it unknown so it
// cannot change the result.
func channelIsThread(ic *interaction.Context, channelID string) (thread, known bool) {
	if data, ok := ic.Interaction.Data.(discordgo.MessageComponentInteractionData); ok {
		if ch := data.Resolved.Channels[channelID]; ch != nil {
			return discord.IsThreadType(ch.Type), true
		}
	}
	if ch, err := ic.Channel(channelID); err == nil && ch != nil {
		return discord.IsThreadType(ch.Type), true
	}
	return false, false
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
