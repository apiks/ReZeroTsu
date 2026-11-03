# Discord's native command permissions replace the bot's moderator roles

Mod commands default to requiring Manage Server, and server admins grant them to other roles, members or channels in Discord's own command permissions (Server Settings, Integrations). The bot's moderator-role list, its commands and its own permission check are removed. We chose this over keeping bot-managed moderator roles because Discord then hides mod commands from members who cannot run them, and admins manage access in one place. The two cannot be combined: a default permission hides a command from moderator-role members too.

## Consequences

Servers that relied on moderator roles lose mod access for those roles until an admin adds an override. The bot cannot add overrides itself, because that needs a user's OAuth token, not the bot token. Server owners must be warned through a broadcast to bot-log channels before the release.

Discord applies command permissions per top-level command, not per subcommand. A command group must therefore hold either only mod commands or only commands for everyone.

Admin overrides attach to a command's ID, and a renamed command gets a new ID, so its overrides are lost. The switch to native permissions therefore ships only after every mod command has its final name, so admins configure overrides once.
