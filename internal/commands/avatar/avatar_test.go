package avatar

import (
	"testing"

	"ReZeroTsu/internal/commands"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/interaction/interactiontest"

	"github.com/bwmarrin/discordgo"
)

func avatarOf(target *discordgo.User) []interactiontest.Call {
	data := discordgo.ApplicationCommandInteractionData{Name: "avatar"}
	if target != nil {
		data.Options = []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: target.ID},
		}
		data.Resolved = &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{target.ID: target}}
	}
	fake := &interactiontest.Fake{}
	if err := run(interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, data)); err != nil {
		panic(err)
	}
	return fake.Calls()
}

func TestAvatarShowsTheChosenUserPubliclyInOneReply(t *testing.T) {
	calls := avatarOf(&discordgo.User{ID: "42", Username: "ram", Avatar: "abc"})
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want exactly one public Reply", calls)
	}
	emb := calls[0].Response.Embeds[0]
	if emb.Title != "ram's avatar" || emb.Image == nil || emb.Image.URL == "" {
		t.Errorf("embed = %+v", emb)
	}
}

func TestAvatarDefaultsToTheInvoker(t *testing.T) {
	// The test invoker has no avatar hash, so the default avatar URL still resolves and is shown.
	calls := avatarOf(nil)
	if len(calls) != 1 || calls[0].Visibility != interaction.Public || calls[0].Response.Embeds[0].Title != "invoker's avatar" {
		t.Fatalf("calls = %+v", calls)
	}
}

func showAvatarOn(target *discordgo.User) []interactiontest.Call {
	data := discordgo.ApplicationCommandInteractionData{Name: "Show avatar", CommandType: discordgo.UserApplicationCommand, TargetID: target.ID,
		Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{target.ID: target}}}
	fake := &interactiontest.Fake{}
	if err := runShowAvatar(interactiontest.NewCommandContext(fake, interaction.Public, interaction.Direct, data)); err != nil {
		panic(err)
	}
	return fake.Calls()
}

func TestShowAvatarPostsTheTargetsAvatarPublicly(t *testing.T) {
	calls := showAvatarOn(&discordgo.User{ID: "42", Username: "ram", Avatar: "abc"})
	if len(calls) != 1 || calls[0].Op != "Reply" || calls[0].Visibility != interaction.Public {
		t.Fatalf("calls = %+v, want exactly one public Reply", calls)
	}
	emb := calls[0].Response.Embeds[0]
	if emb.Title != "ram's avatar" || emb.Image == nil || emb.Image.URL == "" {
		t.Errorf("embed = %+v", emb)
	}
}

func TestShowAvatarIsRegisteredAsAPublicDirectUserCommand(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "Show avatar" {
			if c.Kind != commands.KindUser || c.Run == nil || c.Visibility != commands.Public || c.Response != commands.Direct {
				t.Errorf("Show avatar registration = %+v", c)
			}
			return
		}
	}
	t.Fatal("Show avatar is not registered")
}

func TestAvatarIsRegisteredAsAPublicDirectCommand(t *testing.T) {
	for _, c := range commands.AllCommands() {
		if c.Name == "avatar" {
			if c.Run == nil || c.Handler != nil || c.Visibility != commands.Public || c.Response != commands.Direct {
				t.Errorf("avatar registration = %+v", c)
			}
			return
		}
	}
	t.Fatal("avatar is not registered")
}
