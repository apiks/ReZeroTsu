# Release notes and release steps

Newest first. Each release lists the steps the bot owner does around the deploy, then what changed.

## Native moderator permissions (spec #37, ticket #65)

Implements `docs/adr/0001-native-command-permissions-replace-moderator-roles.md`.

> **Danger:** servers that relied on moderator roles lose mod access for those roles at this release, until an admin adds overrides. The bot owner must send the broadcast below before deploying.

### Release steps

1. Make sure every mod command has its final name (ADR-0001: a renamed command loses its overrides).
2. Send the broadcast to every server's bot-log channel: DM the bot `<prefix>messagelogs` followed by the text below.
3. Deploy. Registration bulk-overwrites the commands with default member permissions of Manage Server on every mod command.
4. After deploy, confirm in a test server that a member without Manage Server does not see `/settings`, and that an admin override in Server Settings, Integrations shows the command again.

### Broadcast text

```text
Moderator roles are going away. After the next update, the bot's mod commands (/settings, /voicerole, /raffle, /reddit, /reactrole, /say, /prune and the message right-click commands Add reaction role, Remove reaction roles and Edit message) can be used by members with the Manage Server permission, and by nobody else until you say so.

If you gave other roles access with /add-moderator-role, that access ends with the update. To give it back: Server Settings > Integrations > this bot, pick a command, and add the roles, members or channels you want. Discord also hides these commands from members who cannot use them.

The commands /add-moderator-role, /remove-moderator-role, /moderator-roles, /mod-only and /react-module are removed.
```

### What changed

- Mod commands register with Manage Server as the default member permission; Discord enforces it and the server's overrides.
- The bot no longer checks moderators itself: no moderator-role lookup, no owner bypass, no re-check on `/settings` buttons, pickers and forms.
- Removed `/add-moderator-role`, `/remove-moderator-role`, `/moderator-roles`, `/mod-only` and `/react-module`. The stored moderator-role, mod-only and reacts-module settings are no longer read or written.
