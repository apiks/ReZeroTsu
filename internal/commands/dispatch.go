package commands

import (
	"context"
	"runtime/debug"

	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/discord"
	"ReZeroTsu/internal/interaction"
	"ReZeroTsu/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// dispatch runs one slash command interaction. The framework's own refusals (unknown command, wrong context)
// are private replies sent before acknowledging; after that the command is acknowledged by its declared
// response mode and visibility, and its handler runs. A failure after the acknowledgement is shown privately
// (see interaction.Context.Refuse). s is only handed to legacy handlers.
//
// Who may run a mod command is not checked here: Discord enforces the command's default member permissions
// and the server's overrides before the interaction is sent (ADR-0001).
// It returns an error only when the acknowledgement itself failed.
func dispatch(ctx context.Context, db *database.Client, name string, port interaction.Port, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	cmd, ok := registry[name]
	if !ok {
		refuse(interaction.NewContext(port, i.Interaction, db, interaction.Public, interaction.Direct), "Unknown command: "+name)
		return nil
	}
	ic := interaction.NewContext(port, i.Interaction, db, cmd.Visibility, cmd.Response)
	ic.Ctx = ctx

	if msg := contextRefusal(cmd.Context, i.GuildID); msg != "" {
		refuse(ic, msg)
		return nil
	}

	l, found := cmd.resolve(ic.Command.Options)
	if !found {
		refuse(ic, "Unknown command: "+name)
		return nil
	}
	if l.sub != nil {
		ic.Visibility, ic.Mode = l.visibility, l.response
		ic.Group, ic.Subcommand, ic.Command.Options = l.group, l.subcommand, l.options
	}

	if l.response == Deferred {
		if err := ic.Defer(l.visibility); err != nil {
			args := append(discord.RESTAttrs(err), "command", name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
			logger.For("commands").Error("deferred ack failed", args...)
			return err
		}
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				l := logger.For("commands").With("command", name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "panic", r, "stack", string(debug.Stack()))
				l.Error("panic in handler")
				refuse(ic, errMessage)
			}
		}()
		if l.run != nil {
			if err := l.run(ic); err != nil {
				args := append(discord.RESTAttrs(err), "command", name, "guild_id", i.GuildID, "channel_id", i.ChannelID, "err", err)
				logger.For("commands").Error("handler failed", args...)
				refuse(ic, errMessage)
			}
			return
		}
		cmd.Handler(ctx, db, s, i)
	}()
	return nil
}

// refuse shows text only to the invoker and logs when even that fails.
func refuse(ic *interaction.Context, text string) {
	if err := ic.Refuse(text); err != nil {
		args := append(discord.RESTAttrs(err), "guild_id", ic.GuildID, "channel_id", ic.ChannelID, "err", err)
		logger.For("commands").Error("private refusal failed", args...)
	}
}

// OnInteraction dispatches slash commands, buttons, pickers, forms and autocomplete; reports errors privately.
func OnInteraction(ctx context.Context, db *database.Client, s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionMessageComponent || i.Type == discordgo.InteractionModalSubmit {
		dispatchComponent(ctx, db, interaction.NewDiscordgoPort(s, i.Interaction), s, i)
		return
	}
	if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
		handleAutocomplete(ctx, db, s, i)
		return
	}
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	port := interaction.NewDiscordgoPort(s, i.Interaction)
	if err := dispatch(ctx, db, data.Name, port, s, i); err != nil {
		// The acknowledgement failed; it is already logged. A private reply is the last attempt to tell the user.
		_ = port.Reply(interaction.Response{Content: errMessage, Visibility: interaction.Private})
	}
}
