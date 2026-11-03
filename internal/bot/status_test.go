package bot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCustomStatusSendsTypeFourWithConfigTextAsState(t *testing.T) {
	usd := customStatus("watching anime")
	if len(usd.Activities) != 1 {
		t.Fatalf("activities = %d, want 1", len(usd.Activities))
	}
	a := usd.Activities[0]
	if a.Type != discordgo.ActivityType(4) {
		t.Errorf("type = %d, want 4 (custom)", a.Type)
	}
	if a.State != "watching anime" {
		t.Errorf("state = %q, want the config text", a.State)
	}
	if usd.Status != "online" {
		t.Errorf("status = %q, want online", usd.Status)
	}
}
