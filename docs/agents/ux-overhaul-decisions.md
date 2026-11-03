# UX overhaul: settled decisions

Input for `/to-spec`. This log records the decisions from a grilling session on 2026-11-03, plus the facts behind them. Write one spec per section under "Specs". Use the terms in `GLOSSARY.md`. ADR-0001 records the moderator decision.

## Facts found during research

- The invite permission integer `335883328` grants Add Reactions, View Channel, Send Messages, Manage Messages, Read Message History, Use External Emojis, Change Nickname and Manage Roles. It lacks Embed Links, Manage Webhooks and Send Messages in Threads, which the bot needs.
- Since 2026-02-23 Discord requires Pin Messages (`1 << 51`) to pin. discordgo v0.29.0 has no constant for it. Feed auto-pin checks Manage Messages and ignores pin errors, so it probably fails silently.
- `default_member_permissions` and admin overrides apply per top-level command, not per subcommand.
- Admin overrides attach to command IDs. Bulk overwrite keeps the ID of a command whose name is unchanged. A renamed command gets a new ID and loses its overrides.
- A bot token cannot set command permission overrides. That needs a user OAuth token.
- discordgo v0.29.0 supports modals, Label, select menus in modals, Components V2, integration types and interaction contexts.
- Context menu commands: up to 15 per type since 2026-03-03.
- Modal text inputs hold at most 4000 characters. An embed description holds 4096.
- AnimeSchedule timetable entries carry `title`, `romaji`, `english` and `native`. The bot decodes only `title`.
- The raffle record stores no channel ID. Reaction-role entries are keyed by message ID only, with no channel ID.
- `/mod-only` and `/react-module` store flags that nothing reads. `/ping-message` is shown only by `/ping`, not on a bare @mention.
- Send verification for unconfirmed deliveries matches embeds by URL and description. Components V2 messages have no embeds.
- Channel obfuscation: from 2026-11-16 Discord hides the names of channels the bot cannot view.

## Decisions

### Moderators (ADR-0001)
- Mod commands default to Manage Server. Admins grant them to others in Server Settings, Integrations.
- Remove the moderator-role commands, the bot-side moderator check, `/mod-only` and `/react-module`.
- Before release, broadcast to bot-log channels with the owner `messagelogs` command. **Danger:** servers that rely on moderator roles lose mod access for those roles until an admin adds overrides.

### Visibility and responses
- **Public:** results meant for the channel. That covers `/roll`, `/pick`, `/joke`, `/avatar`, `/schedule`, `/ping`, `/about`, `/help`, raffle creation and draws.
- **Private:** refusals, errors, personal data and its confirmations, and every mod configuration view and confirmation.
- An error found before acknowledging gets an immediate private reply. A later error on a public command deletes the placeholder and sends a private follow-up.
- Instant commands answer directly, with no "thinking" step.
- When someone other than the author presses navigation on a public message, they get a private copy at the requested page.
- Errors explain the problem and the fix, for example naming the missing permission and channel. Raw Discord errors are never shown. Unexpected errors show "Something went wrong" and are logged.
- Every displayed channel, role and user is a mention. Legacy reaction-role entries without a channel show a bare message ID.
- Every autocomplete matches anywhere in the text, case-insensitively.

### Command tree (final)
```
Everyone (server + user install)
  /schedule [day]
  /subscription add|remove|list|clear
  /reminder set|list|remove
  /roll  /pick  /joke  /avatar  /help  /about  /invite  /ping
  User right-click: Show avatar

Moderators (server install only, default Manage Server)
  /settings
  /voicerole add|remove|list
  /raffle create|draw|remove|list|post
  /reddit add|remove|list
  /reactrole list
  /say [channel] [embed]
  /prune
  Message right-click: Add reaction role, Remove reaction roles, Edit message

Owner (DM prefix only): servers, shards, uptime, messagelogs
```
- Each command moves to its final name inside its own area spec. No interim names.
- Every list view gets a Remove picker with at most 25 entries per page. Slash `remove` subcommands stay, with autocomplete. Reaction roles have no slash remove.

### Invite
- Add Embed Links, Manage Webhooks, Send Messages in Threads and Pin Messages. Drop Change Nickname.
- Leave Mention Everyone out. The new-episodes role picker warns when the role is not mentionable.

### Personal commands
- User install for the "Everyone" set and Show avatar.
- `/subscription add` autocompletes this week's airing shows, matching title, romaji, English or native name. Choices show as "Title (English)". `/subscription remove` suggests the user's own subscriptions. Subscriptions stay keyed by the display title.
- Reminders are delivered only by DM, with no channel fallback. The DM shows "Set in #channel".
- One `/help` with a category picker and clickable command mentions. Moderator categories are shown to everyone, labelled "Moderators only". Under a user install it lists only commands available there.
- Retire the user prefix commands. For about three months, a DM starting with the prefix that is not an owner command gets one hint: "Prefix commands were replaced by slash commands. Type / to see them."

### Server configuration
- A private `/settings` panel shows current values in five rows: bot-log channel, new-episodes channel, new-episodes ping role, daily-schedule channel, and a donghua toggle plus an "Edit ping message" button that opens a form.
- Clearing a channel picker disables that feature.
- The ping message stays the text shown by `/ping`, and its label says so. Bare @mention replies stay as they are, including the per-user ones.

### Raffles
- New raffles post one message showing the name, entrant count and latest winner, with Enter and Leave buttons.
- Button clicks get private confirmations, and the message updates on every entry and draw.
- A draw does not close the raffle, and re-draws are allowed.
- `/raffle post` puts a button message up for any raffle, which migrates raffles created without one. Legacy 🎰 reaction raffles keep working.
- `/raffle remove` edits the message to "This raffle has ended", keeps the last winner, and disables the buttons.

### Posting and reaction roles
- `/say [channel] [embed]` opens a form with a multi-line text box.
- Edit message, on the bot's own messages only, opens the same form prefilled. Text over 4000 characters gets a clear refusal.
- Add reaction role opens a form with an emoji box and a role picker. The bot reacts on the target message wherever it is.

### Other
- The bot status becomes a custom status. The config rotation stays.

## Specs

1. **Quick fixes.** Invite permissions, Pin Messages for feed auto-pin, custom status.
2. **Interaction foundation.** Command groups, the visibility rule, direct answers, private late errors, default permissions, user-install support, routing for buttons, forms and right-click commands, private navigation copies, command mentions, mention display, match-anywhere autocomplete and plain-language errors. Applies the new rules to commands that keep their names: `/roll`, `/pick`, `/joke`, `/avatar`, `/ping`, `/about`, `/invite`, `/prune` and `/schedule`.
3. **Personal commands and user install.** `/subscription`, `/reminder`, `/help`, user install, Show avatar, prefix retirement with the hint.
4. **Server configuration.** `/settings` panel, `/voicerole`, `/reddit`.
5. **Button raffles.**
6. **Posting and reaction roles.** `/say`, Edit message, reaction-role right-click commands, `/reactrole list`.
7. **Native moderators.** ADR-0001, with the broadcast flagged as a danger.

**Order:** 1, then 2, then 3 to 6 in any order or in parallel, then 7. Specs 4 to 6 keep the current bot-side moderator check until spec 7 flips to native permissions. Admins then configure overrides once, on final command IDs.

**Out of this batch:** Components V2 notifications. That needs a new way to verify unconfirmed deliveries, because the current check matches embeds.
