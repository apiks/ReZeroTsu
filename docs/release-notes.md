# Release notes and release steps

Newest first. Each release lists the steps the bot owner does around the deploy, then what changed.

## Raw, sub and dub tracks (spec #108)

- `/schedule` track (#110): `/schedule` takes an optional `track` option whose choices read "Sub: sub, else raw", "Dub: dub, else sub, else raw" and "Raw: raw only", and whose description says it defaults to the server's preferred track, else Sub. Sub shows sub, else raw (what the bot always showed). Dub shows a show's dub in a week it airs one, else its sub, else raw. Raw shows raw only. Previous, Next and the day picker keep the track you chose. Every line is labelled `RAW`, `SUB` or `DUB`, and the footer says "Dub preferred" or "Raw preferred" for those two. Subscriptions are unchanged. What a server's preferred track, the autoposts and schedule messages from before this release do is described in the bullets below. A dub episode alert reads "**Ep 5** dubbed is out!" (a dubbed movie, "**Title** is out, dubbed!").
- Paging keeps the track (#111): Previous, Next and the day picker keep the track a `/schedule` message shows, so a member who opened a Dub page and presses Next sees the next day's Dub page. When another member presses them, their private copy shows the same track. A button or picker on a message from before this release opens the server's preferred track, and a day no longer on the timetable still gets the same private refusal.
- Server preferred track (#112): moderators set the server's preferred track from `/settings` with the new "Preferred track: Sub" button after Donghua. Each press cycles Sub, Dub, Raw, then Sub again and saves only that setting; the panel text gains "**Preferred track:** Sub (sub, else raw). Used by /schedule and both autoposts." (Dub reads "(dub, else sub, else raw)", Raw "(raw only)"). `/schedule` without its `track` option, and the buttons of older schedule messages, use the server's preference; the `track` option still overrides it for one message. In DMs, and in a server the bot has not joined, `/schedule` uses Sub. The daily-schedule autopost builds its page under the server's preference with the same line labels and footer; its title is unchanged. A server that changes nothing stays on Sub and sees no difference.
- New-episodes autopost track (#113): the new-episodes autopost follows the server's preferred track. A Dub server gets dubbed episodes as they air ("**Ep 5** dubbed is out!") and the sub (or raw) episodes of shows without a dub that week; a Raw server gets raw episodes; a Sub server sees no change. A show's sub and dub on one day are both posted, once each. Episodes the channel already got on the deploy day are not posted again. A moderator who changes the preferred track during the day is not sent that day's earlier airings again, and the new track's later airings post as they air. Episode DMs still follow Sub. `/about` and the README now say the bot covers sub, dub or raw.

## Subscription UX (spec #79)

- Next episode (#92): `/subscription add` says when the show airs next, or when the latest episode aired and that you get a DM for the next one. The reply ends with a reminder that alerts arrive by DM and DMs from ZeroTsu must stay open, also when you are already subscribed. `/subscription list` shows each subscription's next episode, or "no upcoming episode listed".
- Upcoming shows (#93): you can subscribe to a show that is on the upcoming list but not yet on the timetable. The suggestions and the typed lookup cover the three loaded weeks (last week's shows count unless their final episode aired there) and the upcoming list. The suggestion's value is the show's route, so a renamed show is still the same subscription. A subscription with a route matches only by route, and a legacy one without a route matches by title. The reply and the list say when the show premieres ("premieres ...", "premiered ...", or "premiere date TBA").
- Movies (#93): a movie has a release, not episodes. The reply says "It releases ..." or "It released ...", and for an undated movie "Its release date isn't announced yet. You'll get a DM when it is released." The list says "releases ...", "released ..." or "release date TBA".
- `/subscription add` suggestions (#94) carry the media type of non-TV shows ("· TV Short", "· ONA", "· Donghua", "· OVA", "· Special", "· Movie") and "· subscribed" for shows you are already subscribed to, which are listed last. Timetable shows come before upcoming shows. A new optional `type` option (TV, TV Short, ONA, Donghua, OVA, Special, Movie) narrows the suggestions and the typed lookup to that type.
- `/subscription clear` (#95) asks first, with Remove all and Keep them buttons, and only the member whose list it is can press them. `/subscription remove` with a show you are not subscribed to points at `/subscription list`.

## Schedule UX (spec #78, tickets #88 to #91)

- `/schedule` shows a day picker above Previous and Next. Pick any day of last week, this week or next week, or step with Previous and Next across week boundaries. Use the picker or buttons instead of re-running `/schedule` for another weekday.
- The `day` option takes `today`, `tomorrow`, or a weekday name, which now means its next occurrence (today included). A weekday name no longer opens a day that has already passed; to see a past day, use the picker or pick it from the suggestions. The suggestions are Today and Tomorrow with their dates, then the next five days. A date outside the loaded weeks is refused with a private note.
- The title carries the real date ("Wednesday, 4 Nov 2026"), and the footer says days run on UTC and times show in your time zone.
- Today's page adds a relative time ("in 2 hours") next to each time. Other days show the time only.
- Non-TV entries carry a label: TV Short, ONA, Donghua, OVA, Special or Movie.
- A day with too many shows is cut at a whole line and ends with a link to AnimeSchedule.net with the count of the rest.
- Buttons on schedule messages posted before this release now answer "That day is no longer on the timetable. Run /schedule again." Members must run `/schedule` again to get a working message.
- The daily schedule autopost uses the same page body; an empty day tells members to run `/schedule`.

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
