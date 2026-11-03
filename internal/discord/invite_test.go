package discord

import "testing"

func TestDefaultInvitePermissionsDecodedBits(t *testing.T) {
	want := map[string]int{
		"Add Reactions":            6,
		"View Channel":             10,
		"Send Messages":            11,
		"Manage Messages":          13,
		"Embed Links":              14,
		"Read Message History":     16,
		"Use External Emojis":      18,
		"Manage Roles":             28,
		"Manage Webhooks":          29,
		"Send Messages in Threads": 38,
		"Pin Messages":             51,
	}
	var wantMask int64
	for name, bit := range want {
		wantMask |= 1 << bit
		if DefaultInvitePermissions&(1<<bit) == 0 {
			t.Errorf("%s (bit %d) missing from invite permissions", name, bit)
		}
	}
	if DefaultInvitePermissions != wantMask {
		t.Errorf("invite permissions = %b, want exactly %b", int64(DefaultInvitePermissions), wantMask)
	}
	for name, bit := range map[string]int{"Change Nickname": 26, "Mention Everyone": 17} {
		if DefaultInvitePermissions&(1<<bit) != 0 {
			t.Errorf("%s (bit %d) must not be in invite permissions", name, bit)
		}
	}
}
