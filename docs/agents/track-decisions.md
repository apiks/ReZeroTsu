# Raw, sub and dub tracks: settled decisions

Input for `/to-spec`. This log records the decisions from a grilling session on 2026-11-03, plus the facts behind them. Write one spec per section under "Specs". Use the terms in `GLOSSARY.md` (new this session: Track, Preferred track, Subscription tracks; Subscription was reworded). ADR-0003 records the chain-for-pages, set-for-subscriptions decision. ADR-0002 (route-first matching) still applies. The rules of `docs/agents/ux-overhaul-decisions.md` and `docs/agents/anime-reddit-ux-decisions.md` (visibility, private refusals, plain-language errors, match-anywhere autocomplete, private copies for other members' navigation, one list line per show) still apply.

## Facts found during research

### Code

- The bot sends one `GET /api/v3/timetables` per week with no `airType`, so the API answers `all`: raw, sub and dub rows, each with its track in `airType`.
- `animeschedule.processTimetable` collapses them: dub rows are dropped, raw rows are dropped when the show has any sub row in that fetched week, and the rest is labelled `sub` or `raw`. Every surface (`/schedule`, the daily-schedule autopost, the new-episodes autopost, subscriptions, episode alerts) therefore sees one row per airing, sub else raw, never dub. A delayed sub row still counts as "the show has a sub this week".
- `ShowEntry.AirType` is `"sub"` or `"raw"`. `/schedule` lines carry a `SUB`/`RAW` label (`scheduleLine`). Alerts read "**Ep 5** subbed is out!", "raw is out!", and for movies "**Title** is out, subbed!" (`buildEpisodeEmbed`). `/about` says "Anime Schedule (_subbed_)"; the README says "New Anime Episodes feed (subbed)".
- A subscription (`anime_subs.ShowSub`) is a show: route plus title, with `notified`, `last_notified_air_unix` and `last_notified_episode` (`route|Ep N`, no track). `userShowAlreadyNotified` also treats any air time not later than the last notified one as already sent, which is wrong once two tracks of one show interleave.
- The new-episodes autopost keeps a per-channel document of today's shows keyed by show name under `anime_subs.MatchKey` (`seedGuildNewEpisodesForShard`, `guildShowNamesMatch`, `processGuildNewEpisodes`). Two tracks of one show on one day would collide.
- The `/settings` panel uses all five component rows Discord allows (bot log, new-episodes channel, ping role, daily-schedule channel, the toggle row with Donghua and Edit ping message). A select menu has no free row; the toggle row has room for more buttons.
- The donghua setting is one server setting read by `/schedule`, the daily-schedule autopost and the new-episodes autopost (`GuildSettings.Donghua`, `donghuaSetting`). `/schedule` in a DM, or from a user install in a server the bot is not in, has no settings and shows donghua.
- The schedule navigation custom IDs are `sched:<UTC date>:<author ID>` and `schedpick:<author ID>`; the picker's value is the date. There is room for a track in them.
- The daily cleanup (`buildKeepRoster`, `cleanupFinishedShows`) judges a subscription by its show: kept while the show is on this or next week's timetable, on the upcoming list, or not final last week.
- `/subscription add` resolves the show through `findShowByName`, seeds `notified` from today's aired entry (`airedTodayEntry`) and answers through `findNextEpisode`, which reads every loaded entry and the upcoming list. The upcoming list (`UpcomingShow`) holds one `Premiere` only.

### AnimeSchedule

Verified facts about weeks, time zones, rate limits and media types are in `docs/animeschedule-api.md`. Read from the AnimeSchedule source on 2026-11-03:

- The site calls raw, sub and dub each a **Track**. `/timetables/{airType}` takes `raw`, `sub`, `dub` or `all` (the default). `sub` means "sub, else raw" on the site; `raw` and `dub` are strict. In `all`, a sub or dub tile of a show without its own premiere (a "Shared Slot") whose time equals raw's clock is served as a raw row, so the `all` answer never carries a duplicate of the raw airing. A Shared Slot sub or dub with its own clock is served as a sub or dub row at raw's instant plus the clock difference.
- A sub or dub row carries that track's own delay text and dates.
- A `/anime` item carries `premier`, `subPremier`, `dubPremier`, and the per-track clocks `jpnTime`, `subTime`, `dubTime` (UTC; only hour and minute matter; 00:00 means unset). It does not carry the per-track TBD flag.
- The site's rule for a track existing (`HasSubExists`, `HasDubExists`): the track's premiere is set, or its clock is set (not 00:00), not TBD, and differs from `jpnTime` in UTC hour:minute. A sub or dub premiere is not needed for the track to exist.
- A Shared Slot track's airing instant is raw's instant plus the signed difference `trackClock - jpnTime` in minutes (`clockDeltaFromJpn`, `airInstant`). No wrap-around: an earlier clock gives an earlier instant.
- The site's own episode notifications are per show per track, with per-track "skip when" rules defaulting to "skip raw when sub or dub exists". That model was considered and rejected (ADR-0003).

## Decisions

### Terms

- **Track**: raw, sub or dub. **Preferred track**: Sub, Dub or Raw, for pages and autoposts. **Subscription tracks**: the set a subscription alerts for. There is no "All" value anywhere in the bot. Definitions are in `GLOSSARY.md`.

### Timetable data

- The bot keeps the single `all` request per week and stores every row with its track. No per-track requests.
- A track preference is applied when a surface reads a day, never at fetch time. The Sub chain's raw fallback is computed per show per fetched week, as today.
- A delayed row counts for the chain (it hides lower tracks) and is shown with its delay label; alerts skip delayed rows as today.
- Rows that are identical in show, track, air time and episode are kept once.

### Preferred track, the chain (ADR-0003)

- Every page and autopost shows one row per show per airing, chosen by the preferred track:
  - **Sub** (default): sub, else raw; dub never. This is today's behaviour exactly, so no server or member sees a change at deploy.
  - **Dub**: dub, else sub, else raw. A show with a dub row this week shows only its dub rows that week; a show nobody dubs still shows its sub (or raw) rows.
  - **Raw**: raw only.
- "Has a row of a higher track" is judged per show per week. In the week a dub skips, a Dub server sees that show's sub row.
- Worked example, Frieren this week: raw Ep 5 Friday 15:00, sub Ep 5 Friday 16:00, dub Ep 2 Monday 20:00. Sub shows sub Ep 5 Friday; Dub shows dub Ep 2 Monday only; Raw shows raw Ep 5 Friday.

### Server setting and /settings

- One server setting, **preferred track**, default Sub, read by `/schedule`, the daily-schedule autopost and the new-episodes autopost, like Donghua. Where the bot has no server membership (DMs, user install in a server it has not joined) the preference is Sub.
- The `/settings` panel gets a third button on the toggle row, after Donghua: "Track: Sub" → "Track: Dub" → "Track: Raw", cycling on press. The panel text gains a line "**Preferred track:** Sub".

### /schedule

- An optional `track` option with fixed choices Sub, Dub, Raw. Omitted, it means the server's preferred track. The chosen track overrides the server setting for that message only.
- Previous, Next and the day picker keep the chosen track, so their custom IDs carry it. Another member's press gets a private copy with the same track, as today. A press on a message from before this release, whose custom ID carries no track, reads as the server's preferred track.
- Every line keeps its per-line label, now `RAW`, `SUB` or `DUB`, in every view: a Sub view mixes raw fallbacks and a Dub view mixes sub ones.
- The footer names a non-default preference: "12 shows · Dub preferred · days run on UTC, times show in your time zone" (or "Raw preferred"). For Sub the footer is unchanged.
- The page title, the `day` grammar, the picker and the refusals are unchanged.

### Daily-schedule autopost

- Same page body as `/schedule` under the server's preferred track, including the label and footer rules. The send verification matches the title, which does not change.

### New-episodes autopost

- Posts the rows the server's preferred track chain selects for today, one embed per row. A Dub server posts dub rows, and sub (or raw) rows for shows without a dub that week.
- Its per-channel sent state identifies an airing by show, track and episode, not by show name, so a sub and a dub of one show on one day are two entries. The seeding and "same day, same shows" checks use that identity.
- Role ping, batching, rejection handling and the permission warning are unchanged.

### Alert and page wording

- Episode alerts gain the dub form: "**Ep 5** dubbed is out!", and for movies "**Title** is out, dubbed!". Sub and raw wording is unchanged.
- `/about` reads "Anime Schedule (sub, dub or raw)" instead of "(_subbed_)". The README's "(subbed)" goes the same way. `/help` and the release notes describe the option, the setting and the button.

### Subscription tracks, the set (ADR-0003)

- `/subscription add` gets an optional `track` option with five fixed choices: **Sub** (default), **Dub**, **Raw**, **Sub and Dub**, **Every track**. A subscription stores the set. A stored subscription without one reads as Sub.
- Inside the set, Sub means the sub track, or the raw track in a week the show has no sub row (the same per-week rule as the chain); Dub and Raw mean exactly that track. One DM per airing of each track in the set: Sub and Dub on the Frieren example sends dub Ep 2 on Monday and sub Ep 5 on Friday. A member holding Raw and Sub (via Every track) on a show without a sub track gets the raw row once, not twice.
- Re-running `add` on a show you follow with a different `track` replaces the set and says "**Frieren** now alerts for Sub and Dub." With the same set the reply stays "You are already subscribed to `Frieren`." (with the DMs footer). `remove`, the Remove picker and `clear` act on the whole show, never one track.
- "Already notified" memory is per track inside the subscription: a later-numbered sub never suppresses an earlier-numbered dub and vice versa, whatever order they air or are processed. The episode identity includes the track. An entry's existing memory (`last_notified_air_unix`, `last_notified_episode`) is the sub track's.
- The one-subscription-per-show rule (ADR-0002), the 100-subscription limit and the route-first matching are unchanged.

### Daily cleanup by track

- A subscription is kept while, for any track in its set, a row of that track (Sub counting its raw fallback) is on this or next week's timetable, or the show is on the upcoming list, or last week's latest row of that track was not its final episode.
- In addition, a subscription whose set includes Dub is kept while the show has never had a dub row in the loaded weeks: the dub has not started. Likewise for Raw.
- A plain Sub subscription follows today's rule exactly. Stated consequence: a Dub subscription to a show that never gets dubbed stays until the member removes it.

### Upcoming shows and track existence

- The upcoming list keeps each item's `premier`, `subPremier`, `dubPremier`, `jpnTime`, `subTime` and `dubTime`.
- A sub (dub) track of an upcoming show exists by the site's rule: its premiere is set, or its clock is set (not 00:00) and differs from raw's in UTC hour:minute. The API hides the TBD flag, so a TBD track counts as existing.
- A track's premiere is its own premiere when set; otherwise, when the track exists by its clock, raw's premiere plus the signed clock difference (track clock minus raw clock, in minutes, no day rollover); otherwise unknown. Raw's premiere is `premier`.
- Suggestions label an upcoming show with the premiere of the `track` option's tracks when the option is set (the earliest set one for a set of several), else with raw's as today.

### /subscription wording

- Plain Sub subscriptions read exactly as today in `add` replies, `list` lines and suggestions.
- For any other set, the confirmation names it: "Subscribed to **Frieren** (Sub and Dub)." The next-episode line is the earliest upcoming row across the subscription's tracks, labelled with its track when the set has more than one: "Next episode: Ep 2 (dub), <full date> (<relative>)". Aired, premiere and movie forms carry the same label under the same rule.
- For an upcoming show, the premiere named is the earliest set premiere among the subscription's tracks, labelled with its track when the set has several; a track whose premiere is unknown reads "premiere date TBA" and never implies the track does not exist.
- When a chosen track does not exist by the rule above and has no row in the loaded weeks, the subscription is still saved and the reply says so: "Subscribed to **Frieren** (Dub). No dub is listed yet. You'll get a DM when a dubbed episode airs." (likewise raw). With a set of several tracks, the line names the missing track and the next-episode line covers the rest.
- `list` lines read "3. Frieren · Dub · Ep 2 in 2 days", "· Sub and Dub · Ep 2 (dub) in 2 days", or "· Dub · no dub listed yet", with the media-type label before the tracks as today. The "· subscribed" suggestion label is unchanged.

## Specs

1. **Timetable tracks.** Every row kept with its track; the preferred-track chain and its per-week rule; the server setting, its default and the `/settings` cycling button and panel line; the `/schedule` `track` option carried by the picker and buttons, old-message handling and the footer; the daily-schedule autopost; the new-episodes autopost with sent state by show, track and episode; the `DUB` label and the "dubbed" alert wording; `/about` and README copy.
2. **Subscription tracks.** The five-choice `track` option and the stored set; set semantics with Sub's raw fallback; replace on re-add; per-track notified memory and episode identity; the cleanup by track; the upcoming list's per-track premieres and clocks, the track-existence rule and the shifted premiere; the `add`, `list` and suggestion wording, including the "no dub listed yet" case.

**Order:** 1 first; 2 reads the per-track rows and the upcoming list's new fields, so it follows 1.

Each spec updates `/help`, the README and `docs/release-notes.md` for what it changes.
