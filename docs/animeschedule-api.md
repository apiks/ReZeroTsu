# AnimeSchedule API v3: verified facts

Read before changing `internal/animeschedule`. Each fact was read from the API docs or checked with a live call on 2026-11-03 (marked **live**). Re-check a **live** fact before building on it if the API may have changed.

The docs are split across pages, and the main page lacks the endpoint details:

- `https://animeschedule.net/api/v3/documentation`: conventions (UTC, ISO 8601, null values).
- `https://animeschedule.net/api/v3/documentation/anime`: the `/anime` and `/timetables` endpoints and the object fields.
- `https://animeschedule.net/api/v3/documentation/ratelimits`: rate limits. Linked from the main page; the main page does not state them.

Requests send `Authorization: Bearer <key>` and the bot's `User-Agent`.

The API's own source is in the sibling repo `AnimeSchedule`. The response objects are in `backend/web/handlers/api/v3/objects` and the handlers in `backend/web/handlers/api/v3/private/anime`; its `GLOSSARY.md` defines Track, Episode Span and the track-existence rule. Check a field there before assuming the bot decodes all of it, and add what you find to this file.

## Timetables: `GET /api/v3/timetables`

- `airType` is an optional path segment: `raw`, `sub`, `dub` or `all` (default).
- `year` and `week` select a week and must be passed together. Weeks are ISO weeks, Monday to Sunday. **live:** week 45 of 2026 returned 2 to 8 Nov, and `year=2026&week=46` returned the next week. Without them the call returns the current week.
- `tz` is an IANA zone and defaults to Europe/London. Entry times come back as ISO 8601 with their real offset, so the instant is right in any zone. The week boundaries follow `tz`, though: **live:** summer week 30 of 2026 ran from Monday 02:00+01:00 to Sunday 23:00+01:00, so under BST the default week starts and ends at Sunday 23:00 UTC. The bot passes `tz=Etc/UTC` so a week is a UTC week. **live:** `Etc/UTC` is accepted, and `Asia/Tokyo` returns `+09:00` offsets.
- An entry has `title`, `route`, `romaji`, `english`, `native`, `episodeDate`, `episodeNumber`, `subtractedEpisodeNumber` (the first episode of an episode span that ends at `episodeNumber`; the bot reads it as an integer, 0 or absent meaning no span; **not verified:** its type and meaning were not checked against a live call), `episodes` (0 means unknown), `delayedText`, `delayedFrom`, `delayedUntil`, `status`, `airType`, `donghua` (bool), `imageVersionRoute`, `airingStatus` and `mediaTypes`.
- `mediaTypes` is a list of `{name, route}`. **live:** routes seen are `tv`, `tv-short`, `ona`, `ona-chinese`, `ova`, `special` and `movie`. Chinese animation has six routes, all containing "chinese": `tv-chinese`, `tv-chinese-short`, `ona-chinese`, `ova-chinese`, `special-chinese` and `movie-chinese`. A show is donghua when any of its routes contains "chinese"; the entry's `donghua` flag is that rule.
- Rows of one show, track and time stay separate when one is a span (1-12) and the other a single episode (12). **not verified:** whether the API ever sends both; no live call showed it, and the bot does not merge them.
- **live:** a movie appears as episode 1 of 1 on its release date, so a movie can send an episode alert.

## Upcoming shows: `GET /api/v3/anime`

- Query: `airing-statuses=upcoming` (values: `finished`, `ongoing`, `upcoming`), `page` (default 1, 18 per page), `q` text search, `st` sort (`popularity`, `score`, `alphabetic`, `releaseDate`, `createdAt`), plus genre, studio, year, season, media-type and id filters.
- **live:** the response is `{anime, page, totalAmount}`. There were 576 upcoming shows, so 32 pages. An item has `title`, `route`, `names` (romaji, english, native, synonyms), `premier`, `subPremier`, `dubPremier`, `status`, `episodes`, `mediaTypes` and `imageVersionRoute`.
- **live:** most upcoming shows have no premiere date: the date reads `0001-01-01T00:00:00Z`.
- There is no donghua flag on `/anime` items. A show is donghua when any of its `mediaTypes` routes contains "chinese", AnimeSchedule's own rule (it is how the timetable's `donghua` flag is computed). There are six such routes: `tv-chinese`, `tv-chinese-short`, `ona-chinese`, `ova-chinese`, `special-chinese` and `movie-chinese`. The bot decodes only `ona-chinese` as the Donghua media type; the other five decode as TV.
- `q` is loose: it falls back to genres, studios and sources. **live:** "frieren" returned 8 upcoming shows, one of them Frieren. Match names in the bot instead of trusting `q`.
- `GET /api/v3/anime/{route}` returns one show. It has no next-episode field; the timetable carries air times, joined by `route`.

## Conventions

- Datetimes are ISO 8601 and UTC by default. The null datetime is `0001-01-01T00:00:00Z`. Null fields may be left out, so validate.

## Rate limits and caching

- 120 requests per minute, counted per IP and per app. The limit can change.
- Every response carries `X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset`. `Reset` is a Unix time in seconds (**live:** about 60 seconds ahead).
- The docs do not say what happens past the limit; the bot treats a 429 as a wait until `Reset`, then one retry.
- The docs ask to cache API data only as needed and to respect the limits. The bot refreshes the three timetable weeks every 15 minutes and the upcoming list once a day.
