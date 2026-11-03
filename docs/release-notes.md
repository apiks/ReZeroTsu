# Release notes and release steps

Newest first. Each release lists the steps the bot owner does around the deploy, then what changed.

## Timetable alerts (spec #77)

- Movie alerts (#84): the episode DM and the new-episodes autopost read "**Title** is out, subbed!" (or raw) for a movie instead of "Ep 1 subbed is out!". Other media types keep the episode wording.
- Daily cleanup (#87): subscriptions are kept through breaks and before premieres. A subscription is removed only when its show is on neither this week's nor next week's timetable, is not on the upcoming list, and either aired its final episode last week or was not on last week's timetable either. The cleanup waits until the upcoming list has loaded.

## Reddit settings UX (spec #80, tickets #96 to #100)

- `/reddit list` and `/reddit remove` (#96): every feed has a short code. Pick a feed from the `/reddit remove` suggestions, or from the list's menu, instead of typing its filters.
- `/reddit remove` (#97) now takes one `feed` option. The `channel`, `subreddit`, `post-type`, `author` and `title` options are gone. Moderators pick a feed from the suggestions, or type a subreddit when it has exactly one feed; with several, the bot lists them and asks to pick one. Removing a subreddit used to remove all three sorts at once; now remove each sort's feed separately. A feed's delivery history is kept while a sibling feed in the same channel still uses it.
- `/reddit add` (#99): the bot checks its permissions in the channel first. If it cannot post there, the feed is refused and the missing permissions are named; grant them and run `/reddit add` again. With `pin` on, a missing Manage Messages or Pin Messages permission saves the feed and adds a warning that nothing will be pinned.
- `/settings` (#100): the panel warns when the bot cannot post in the new-episodes or daily-schedule channel and names the missing permissions. Grant them, or pick another channel.
- `/reddit add` (#98): the `post-type` option is renamed `sort`, with fixed choices Hot, Rising and New (default Hot). Existing feeds keep working.
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
