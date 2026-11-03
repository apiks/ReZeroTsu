package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestDefaultInvitePermissionsAreExactlyTheBotsNeeds(t *testing.T) {
	want := int64(discordgo.PermissionAddReactions |
		discordgo.PermissionViewChannel |
		discordgo.PermissionSendMessages |
		discordgo.PermissionManageMessages |
		discordgo.PermissionEmbedLinks |
		discordgo.PermissionReadMessageHistory |
		discordgo.PermissionUseExternalEmojis |
		discordgo.PermissionManageRoles |
		discordgo.PermissionManageWebhooks |
		discordgo.PermissionSendMessagesInThreads |
		PermissionPinMessages)
	if DefaultInvitePermissions != want {
		t.Errorf("invite permissions = %b, want exactly %b", int64(DefaultInvitePermissions), want)
	}
}
