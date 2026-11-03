# Release notes and release steps

Newest first. Each release lists the steps the bot owner does around the deploy, then what changed.

## Reddit settings UX (spec #80, ticket #98)

- `/reddit add`: the `post-type` option is renamed `sort`, with fixed choices Hot, Rising and New (default Hot). Existing feeds keep working.
- Re-adding an identical feed saves nothing and says it is already set up; re-adding it with a different `pin` only updates the pin setting.
- The reply describes the whole saved feed: subreddit, sort, channel, author, title and pin.

## Native moderator permissions (spec #37, ticket #65)

Implements `docs/adr/0001-native-command-permissions-replace-moderator-roles.md`.

> **Danger:** servers that used roles added with `/add-moderator-role` lose mod access for those roles at this release, until an admin adds overrides. The bot owner must send the broadcast below before deploying.

### Release steps

1. Make sure every mod command has its final name (ADR-0001: a renamed command loses its overrides).
2. Send the broadcast to every server's bot-log channel: DM the bot `<prefix>messagelogs` followed by the text below.
3. Deploy. Registration bulk-overwrites the commands with default member permissions of Manage Server on every mod command.
4. After deploy, confirm in a test server that a member without Manage Server does not see `/settings`, and that an admin override in Server Settings, Integrations shows the command again.

### Broadcast text

```text
**ZeroTsu update: moderator access is changing.** In the next update, roles added with `/add-moderator-role` stop granting access to ZeroTsu's mod commands. Mod commands will require **Manage Server** by default. To let another role use them, open **Server Settings → Integrations → ZeroTsu**, choose a command (or the app), and add the role. Members with Manage Server or Administrator are not affected.
```

### What changed

- Mod commands register with Manage Server as the default member permission; Discord enforces it and the server's overrides.
- The bot no longer checks moderators itself: no moderator-role lookup, no owner bypass, no re-check on `/settings` buttons, pickers and forms.
- Removed `/add-moderator-role`, `/remove-moderator-role`, `/moderator-roles`, `/mod-only` and `/react-module`. The stored moderator-role, mod-only and reacts-module settings are no longer read or written.
