# Track polish: settled decisions

Input for `/to-spec`. This log records the decisions from a grilling session on 2026-11-04, after reviewing the implementation of spec #108 (PR #119, branch `spec/108-tracks`). Write one spec. Use the terms in `GLOSSARY.md` (new this session: Episode span). ADR-0003 and `docs/agents/track-decisions.md` still apply.

## Facts found during research

### Code (branch `spec/108-tracks`)

- A timetable entry's episode label is built from `episodeNumber` alone: "Ep N", or "Ep NF" when N equals the show's known episode total. The bot does not decode the API's `subtractedEpisodeNumber`, so a release of episodes 1 to 12 reads "Ep 12F".
- The label is shown in `/schedule` lines (and the daily-schedule autopost), the episode alert ("**Ep 5** subbed is out!"), the `/subscription add` confirmation ("Next episode: Ep 6, …", "Ep 5 aired …") and `/subscription list` lines ("· Ep 6 in 2 days", "· Ep 5 aired yesterday").
- The label is also part of three identities: the timetable's duplicate-row key, the episode DM's "already sent" key (route plus label), and the new-episodes autopost's sent-state identity (show, track, label).
- Changing the label is safe on the deploy day. An episode DM whose key changed is still suppressed by the existing rule that an air time not later than the last-notified one was already sent. A new-episodes row whose identity changed after it aired is recorded as already posted by the mid-day recording of missing rows, so it is not posted again. A row still to air is posted once.
- The final marker is right for a span: the span holds the show's last episode, so the run did finish. The cleanup's use of "final" needs no change.
- The `/settings` panel's preferred-track button reads "Track: Sub"; the panel line reads "**Preferred track:** Sub" and says nothing about the fallback or which surfaces use it.
- The `/schedule` `track` option's description ends "Omit for the server's", which is wrong in a DM and in a server the bot has not joined, where the default is Sub. Its choices are named "Sub", "Dub" and "Raw".
- `docs/release-notes.md`, spec #108 section, first bullet, misspells "autoposts" as "autposts".

### AnimeSchedule

- The timetable row carries `subtractedEpisodeNumber`: the lowest episode number, used only when several episodes air together.
- The site's timetable shows "Ep {from}-{to}" when `subtractedEpisodeNumber` is set, with "F" appended when `to` equals the show's total; otherwise "Ep {to}", with the same "F" rule.
- AnimeSchedule's glossary calls this an Episode Span: from and to where from is above zero and below to.

## Decisions

### Episode span label

- The timetable decodes `subtractedEpisodeNumber`. A row is an episode span when it is above zero and below the episode number.
- A span's label is "Ep {first}-{last}", with a plain hyphen, and "F" appended when the last equals the show's known total: "Ep 1-12F". A row that is not a span keeps today's label: "Ep 5", "Ep 12F".
- The one label is used everywhere the episode label appears today: schedule lines (`/schedule` and the daily-schedule autopost), episode alerts, `/subscription add` confirmations and `/subscription list` lines.
- Alerts keep their sentence with the span label: "**Ep 1-12F** dubbed is out!" (likewise subbed and raw).
- A span's final marker is unchanged in meaning: it marks the show's last episode.
- One row is one DM and one new-episodes embed, span or not.
- No migration: the identity change is safe on the deploy day (see the facts).

### /settings panel

- The preferred-track button's label is "Preferred track: Sub" (or Dub, or Raw). It still cycles Sub, Dub, Raw, Sub.
- The panel line says what the value means and where it applies: "**Preferred track:** Sub (sub, else raw). Used by /schedule and both autoposts." Dub reads "(dub, else sub, else raw)"; Raw reads "(raw only)".

### /schedule `track` option

- Each choice's name carries its meaning: "Sub: sub, else raw", "Dub: dub, else sub, else raw", "Raw: raw only". The stored values stay sub, dub and raw.
- The option description is "Which track to show. Defaults to the server's preferred track, else Sub."

### Release notes and docs

- Fix "autposts" to "autoposts" in the spec #108 section.
- The span label gets its own bullet in the spec #108 section. The button, panel line and option wording are folded into the existing #112 and #110 bullets.
- The README's mention of "the Track button" follows the new label.

## Delivery

- Built on `spec/108-tracks`, so PR #119 ships it in the same release. One spec; its tickets are small and independent.
