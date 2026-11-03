# Anime and Reddit command UX: settled decisions

Input for `/to-spec`. This log records the decisions from a grilling session on 2026-11-03, plus the facts behind them. Write one spec per section under "Specs". Use the terms in `GLOSSARY.md` (new this session: Reddit feed, Sort, Timetable, Upcoming show, Donghua, Daily-schedule autopost). ADR-0002 records the route-first matching decision. It follows `docs/agents/ux-overhaul-decisions.md`, whose rules (visibility, private refusals, plain-language errors, mentions, match-anywhere autocomplete, private copies for other members' navigation) still apply.

## Facts found during research

### Code
- `/schedule` picks "today" with the host's local clock (`internal/commands/schedule/schedule.go`, `handleSchedule` and `BuildScheduleEmbed`), but the timetable groups entries by UTC weekday (`animeschedule.processTimetable`). On a host not on UTC, "today" can be the wrong page. The daily-schedule autopost already uses UTC.
- The page title date is "this week's" weekday computed from now, Sunday-first, so it can be up to six days off the entries' real dates.
- The over-length cut in `BuildScheduleEmbed` slices bytes mid-line and can break a bold marker or a `<t:…>` token.
- The timetable cache is keyed by weekday only and holds one week.
- The daily cleanup (`RunAnimeSubFinishedCleanupLoop`) deletes every subscription whose show is not on this week's timetable, so a show on a one-week break loses all subscribers.
- Subscriptions (`anime_subs.ShowSub`) store only the display title; matching uses `anime_subs.MatchKey`, enforced by `internal/archcheck`.
- `/reddit add` saves through `$addToSet`, so an identical feed is a silent no-op while the reply says "Added". It checks no channel permissions.
- `/reddit remove` takes channel, subreddit, post-type, author and title, all retyped exactly. The Remove picker identifies a feed by its identity clipped to 100 characters, and two feeds that clip alike make the picker remove nothing.
- Reddit feeds always post through the bot account (`feedPinPolicy` is bot-account-only), never a webhook. Verifying an unknown send reads recent messages, so Read Message History is needed. Auto-pin needs Manage Messages and Pin Messages (`discord.PermissionPinMessages`).
- `commands.handlePagination` holds the rule for navigation presses (the author replaces the message, another member gets a private copy of a copy-safe view). The new schedule day picker must follow the same rule, so that logic needs to be callable from a picker handler too.

### AnimeSchedule API
Verified facts (weeks, time zones, rate limits, media types, the upcoming list) are in `docs/animeschedule-api.md`. One consequence drove this batch: before it, the bot grouped the default week, whose boundaries follow London time, by weekday. Under summer time that lost the last UTC hour of each Sunday and filed the previous Sunday's late shows under the next one.

## Decisions

### Timetable and alerts
- The bot holds three timetable weeks: last week, this week and next week, fetched by explicit ISO week and year computed from the UTC date, with `tz=Etc/UTC` so each week is a UTC week, every 15 minutes as today. Entries are keyed by their UTC date, not weekday.
- The bot holds the whole upcoming-show list in memory, refreshed once a day, its 32-odd requests spread a few seconds apart so they never compete with the timetable refresh. When `X-RateLimit-Remaining` reaches zero, it waits until `X-RateLimit-Reset` before the next request.
- Every media type can be subscribed to and sends an alert when its entry airs, movies included.
- A subscription stores the show's route with its title. The new-episodes notifier and the cleanup match by route first and fall back to the title (match key) when the subscription has no route; a title match backfills the route (ADR-0002).
- The cleanup deletes a subscription only when its show is on neither this week's nor next week's timetable, is not on the upcoming list, and either last week's episode was its final one or it is absent from last week too. This keeps subscribers through a two-week break and keeps subscriptions to upcoming shows.
- Movie alerts read "**Title** is out, subbed!" (or raw) instead of "Ep 1 subbed is out!". Every other type keeps the episode wording.

### /schedule
- Days are UTC days; "today" is computed in UTC. The footer reads like "12 shows · days run on UTC, times show in your time zone". The duplicate "AnimeSchedule.net" leaves the footer; the author line keeps it.
- A page is a date. Previous/Next step one day and cross week boundaries, disabled at both ends of the loaded range (last week's Monday to next week's Sunday).
- A day picker (select menu) lists all 21 loaded days, labelled like "Tue 3 Nov (today)", with the shown day preselected. Previous/Next stay on a second row. Another member's use of the picker or buttons gets a private copy, as with paging today.
- A button or picker on an old message whose day is no longer loaded gets a private "That day is no longer on the timetable. Run /schedule again."
- The `day` option: a weekday name means its next occurrence, today included (on Saturday, "monday" is the coming Monday). It also accepts "today" and "tomorrow". Autocomplete offers Today, Tomorrow, then the next seven days by date ("Wednesday 4 Nov"). Past days are reachable through Previous and the picker.
- Unknown day refusal: "I don't know that day. Pick a suggestion, or type a day like monday, tue or tomorrow."
- The page title takes its date from the entries' UTC dates; an empty day uses that date directly (it is known now that pages are dates).
- Today's page shows each entry's clock time plus a relative time (`<t:…:t>` and `<t:…:R>`); other days show the clock time only. The daily-schedule autopost always shows today, so it gets relative times too.
- An over-long page is cut at the last whole line and ends "… and N more on AnimeSchedule.net", linked to the site. Applies to `/schedule` and the daily-schedule autopost.
- Entries that are not plain TV carry a media-type label: "· TV Short", "· ONA", "· OVA", "· Special", "· Movie", and "· Donghua" for ONA (Chinese). TV is unlabelled. The donghua server setting keeps hiding donghua.
- Everything above that changes the page body (UTC day, dates, relative times on today, line cut, labels, footer) also applies to the daily-schedule autopost; the picker and buttons do not, since the autopost has no components. The autopost's send verification matches the title, so a changed title format matters only on the deploy day.

### /subscription
- Autocomplete and lookup for `add` cover all three loaded weeks and the upcoming list. Timetable shows come first, then upcoming shows. Suggestions read "Title (English)", plus "· premieres 10 Jan" or "· premiere date TBA" for upcoming shows, the media-type label for non-TV, and "· subscribed" for shows the member already follows (sorted last, not hidden). Matching stays anywhere in any of the four names.
- `add` gets an optional `type` option with fixed choices TV, TV Short, ONA, Donghua, OVA, Special, Movie. When set, `anime` suggestions narrow to that type.
- The `add` confirmation names the next episode: "Subscribed to **Frieren**. Next episode: Ep 6, <full date> (<relative>)". The next episode is the show's earliest entry at or after now across the loaded weeks. With none upcoming but one aired: "Ep 5 aired 3 hours ago. You'll get a DM when the next one airs." For an upcoming show: "It premieres <full date> (<relative>). You'll get a DM when episode 1 airs." or "Its premiere date isn't announced yet." Movies say "It releases <full date> (<relative>)".
- Every `add` confirmation has the footer "Episode alerts arrive by DM. Keep DMs from ZeroTsu open."
- `list` lines show status: "1. Frieren · Ep 6 in 2 days", "· Ep 5 aired yesterday", "· premieres in 10 days", "· premiere date TBA", "· releases in 10 days" (movies), or "· no upcoming episode listed", with the media-type label for non-TV. It stays one numbered list matching the Remove picker.
- `clear` asks first: a private "Remove all N subscriptions?" with a red **Remove all** and a grey **Keep them** button, even for one subscription. Confirming updates the message to "All your subscriptions were removed."; Keep them updates it to "Nothing was removed." The buttons carry the author's ID and do not expire; a press after the list changed clears whatever is there then.
- `remove` for a show the member does not follow adds "See /subscription list for the shows you follow." with a clickable command mention.

### /reddit
- A Reddit feed is identified by channel, subreddit, sort, author and title. Auto-pin is a setting, not identity. Re-adding an identical feed replies "That feed is already set up in #chan."; re-adding it with a different pin choice updates the pin setting and says so.
- The `post-type` option is renamed `sort`, with Discord's fixed choices Hot, Rising, New instead of autocomplete. Renaming an option keeps the command ID and its overrides. The stored field is unchanged.
- `/reddit remove` takes one required, autocompleted `feed` option. Suggestions read "r/anime (hot) in #general · u/bob · "Episode"", matched anywhere. Typed text that is a subreddit matching exactly one feed removes that feed; one matching several gets a private refusal listing them and saying to pick a suggestion. Removing all three sorts of a subreddit in one go is dropped.
- The Remove picker and the `feed` suggestions identify a feed by a short code derived from its full identity, so two feeds never collide. The "removed nothing, use /reddit remove" notice goes.
- `/reddit add` checks the bot's permissions in the channel before saving. Missing View Channel, Send Messages (Send Messages in Threads for a thread), Embed Links or Read Message History gets a private refusal naming the channel and the missing permissions. With pin on and Manage Messages or Pin Messages missing, the feed is saved and the reply carries a warning line.
- The `add` reply echoes the whole feed: "Added r/anime (new) to #chan: only posts by u/bob whose title starts with "episode", pinning the latest. New posts start appearing within a few minutes."

### /settings
- The panel shows a warning line, like the existing ping-role warning, when the bot lacks the permissions to post in the new-episodes or daily-schedule channel. It does not block saving.

## Specs

1. **Timetable and alerts.** Three loaded weeks keyed by UTC date, the upcoming-show list, media types on entries, route stored with subscriptions and route-first matching with backfill (ADR-0002), the new cleanup rule, movie alert wording.
2. **/schedule.** UTC days and footer, date pages, cross-week paging, the 21-day picker, the `day` grammar and autocomplete, the refusals, dates from entries, relative times on today, line-boundary cut, media labels, and the same page-body changes in the daily-schedule autopost.
3. **/subscription.** Autocomplete over three weeks and upcoming shows with labels, the `type` filter, next-episode and premiere wording, the DMs footer, list status lines, confirmation before clear, the not-subscribed pointer.
4. **/reddit and /settings.** Feed identity and duplicates, `sort` with fixed choices, the single `feed` option, short codes for the picker and suggestions, the permission refusal and pin warning, the full echo on add, the autopost channel warning in `/settings`.

**Order:** 1 first; 2 and 3 read the three-week timetable and the upcoming list, so they follow it, in either order or in parallel. 4 is independent and can run any time.

Each spec updates `/help`, the README and `docs/release-notes.md` for what it changes.
