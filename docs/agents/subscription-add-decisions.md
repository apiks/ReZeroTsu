# /subscription add: drop the type option, respect hidden donghua

Input for `/to-spec`. This log records the decisions from a review of `/subscription add` on 2026-11-04. Write one spec. Use the terms in `GLOSSARY.md` (subscription, donghua, upcoming show, timetable). ADR-0002 and ADR-0003 still apply.

## Facts found during research

### Code (main at f15a364)

- `/subscription add` has a required `anime` option (autocompleted), then an optional `type` (TV, TV Short, ONA, Donghua, OVA, Special, Movie), then an optional `track`. Discord lists required options before optional ones, so `type` can only be set after `anime` has been filled. Its one effect is narrowing the `anime` suggestions and the typed lookup to one media type; it is not stored and changes no alert.
- When the lookup finds nothing with `type` set, the reply adds "The type filter may be hiding it; try again without it."
- The suggestions already label non-TV shows ("· TV Short", "· ONA", "· Donghua", "· OVA", "· Special", "· Movie").
- `/schedule`, the daily-schedule autopost and the new-episodes autopost hide donghua when the server's donghua setting is off. `/subscription add` ignores the setting: its suggestions and typed lookup offer donghua in every server.
- The server-settings read for `/schedule` treats a DM, and a user install in a server the bot has not joined, as having no settings: donghua is shown there.
- Timetable entries carry the API's `donghua` flag. Upcoming shows carry no such flag; the bot decodes only their first recognised media type, and recognises only `ona-chinese` as Chinese.

### AnimeSchedule (sibling repo `AnimeSchedule`)

- Chinese animation has six media-type routes: `tv-chinese`, `tv-chinese-short`, `ona-chinese`, `ova-chinese`, `special-chinese` and `movie-chinese` (`backend/timetable/filters.go`).
- The timetable's `donghua` flag is true when any of the show's media-type routes contains "chinese" (`backend/anime/asConverters.go`).
- `docs/animeschedule-api.md` lists only `ona-chinese` among the routes seen live, and says to use it for donghua on `/anime` items. That is incomplete.

## Decisions

### Drop the `type` option

- `/subscription add` loses its `type` option. The suggestions keep their media-type labels, and suggestion order and matching are unchanged.
- The "type filter may be hiding it" sentence goes with it.
- Removing an option keeps the command and its ID; registration updates it.

### Hidden donghua in `/subscription add`

- A show is donghua when any of its media-type routes contains "chinese", AnimeSchedule's own rule. Timetable entries keep using the API's flag. Upcoming shows compute it from their media types, so the upcoming list keeps that fact.
- In a server where the bot is a member and donghua is hidden, `/subscription add` neither suggests donghua nor resolves a typed donghua name. This covers timetable shows and upcoming shows, and shows the member already follows.
- In a DM, and from a user install in a server the bot has not joined, donghua is offered as today, the same rule `/schedule` uses.
- A typed name that matches only a hidden donghua gets the not-found reply plus: "This server hides donghua. Run /subscription add in a DM with ZeroTsu to follow it." The command mention is clickable.
- A typed name is resolved against every show first, with the usual precedence (route, title, title cut to 100 characters, romaji, English, native). If it resolves to a hidden donghua, the reply is the not-found message plus the pointer and nothing is subscribed, even when a lower-precedence match exists among the shown shows. This stops a collision from silently subscribing the member to a different show.
- If that hidden donghua is one the member already follows, the reply is "You already follow `<title>`. This server hides donghua, so run /subscription add in a DM with ZeroTsu to change its tracks." and nothing changes, because the not-found message would be untrue.
- The settings panel line reads "Donghua in the schedule and new subscriptions".
- Existing subscriptions to donghua are untouched: `list`, `remove`, the Remove picker, `clear`, episode DMs and the cleanup all ignore the setting.

### Docs

- `/help` follows the command shape. The README's `/subscription` line drops `type` and says hidden donghua is not offered. `docs/release-notes.md` gets a section for this spec.
- `docs/animeschedule-api.md` lists the six Chinese routes and states the "contains chinese" rule for donghua, replacing the `ona-chinese`-only advice.

## Chinese media types carry both their type and Donghua (added after the review)

Found while researching: the bot recognised only `ona-chinese`, so `tv-chinese`, `tv-chinese-short`, `ova-chinese`, `special-chinese` and `movie-chinese` decoded as TV. A Chinese movie got episode wording ("Ep 1 subbed is out!") and no label. Decision: a Chinese release is both its media type and Donghua.

- The six Chinese routes decode to their base type (TV, TV Short, ONA, OVA, Special, Movie) plus a Donghua fact, not to a type of their own. The Donghua fact is the same one that hides donghua, so the label and the hiding cannot disagree.
- A Chinese movie gets movie wording everywhere a Japanese one does: "**Title** is out, subbed!" (or dubbed, or raw) for alerts, and "It releases …", "It released …", "release date TBA" in `/subscription add` replies and list lines.
- The label carries both: the type's label, then "Donghua", as in "· Movie · Donghua", "· TV Short · Donghua", "· OVA · Donghua" and "· Special · Donghua". A Chinese TV series is unlabelled as a type, so it reads "· Donghua". A Chinese ONA reads "· ONA · Donghua" (it read "· Donghua" before).
- Hiding donghua, the schedule's donghua filter and the `donghua` fact on timetable entries are unchanged.
- Ticket #128 in spec #125 carries this.

## Delivery

- One spec, built on a new integration branch from `main`.
