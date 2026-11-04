# Coding standards

Read during review. Mechanical rules (formatting, unused code, direct Discord sends, show-name matching, text sliced at a Discord length limit) are enforced by checks, not listed here.

## Behaviour parity

A change described as behaviour-preserving keeps every check the old code ran on each call running on each call.

- A cache, memo or hoisted lookup replays the preconditions of the uncached path before it returns an entry. Permission, state and ownership checks are preconditions.
- For each deleted or moved call, name where its guard now runs. A guard with no new home is a finding.
- Moving a call into a callee moves it relative to the callee's guards. List the checks that ran after the call at the old site; each still runs after it at the new one. Trace a late-ending path too: a wait that ends in success after shutdown must still reach the shutdown check before any send.
- The spec and its tickets can disagree with each other ("resolve on a miss" beside "behaviour unchanged"). Review against the behaviour; report the disagreement.

## Glossary vocabulary

Names, comments and test titles use the terms in `GLOSSARY.md`. A term listed under its `_Avoid_` line is a finding, as is a coined synonym for a defined term.

Three archcheck rules cover the mechanical part. `checkAvoidTerms` flags the Avoid terms that mean nothing else (such as "moderator role" and "giveaway") in identifiers and string literals. `checkAvoidPhrases` flags every multi-word Avoid term in `GLOSSARY.md` (such as "post type" and "ONA (Chinese)") in comments and string literals. `checkAvoidNames` reads those same terms as words in identifiers and file names, test files included, so `AirType`, `trackFilter` and `track_setting_test.go` are flagged; it skips the stored `post_type` of an autopost. The same terms are flagged in `README.md` and `docs/release-notes.md`. A line that must quote an upstream or stored name carries `archcheck:allow`. Review the rest: single everyday words like "pick", "roll" and "cache", every test title, and every user-facing sentence.

"Sub" names the Sub track only, and "subscription" is spelled out. A name such as `subTracks` or a test title such as "plain sub removed" is a finding, because a reader cannot tell the track from the subscription.

## Delivery runs and sent state

A change to what a delivery run records as sent is checked against three cases, and each has a test that runs the seeding and then the delivery, the way a restart does:

- A new UTC day: the reset writes the new identity and repeats nothing.
- A restart mid-day: seeding that finds a row missing from today's document records an already-aired, undelayed row as posted, and leaves a row still to air unnotified.
- A setting changed mid-day (the preferred track, donghua): rows the new setting selects get entries the same way, and earlier airings are not posted again.

A test of the delivery run alone passes while the seeding path reposts, so the seeding is part of every such test.

## Mongo writes

Build each filter and update document in a function that takes plain values, and test the document it returns. Test that the bson tags of the fields a write names equal the field names the write uses, in the style of the filter-shape tests beside it. A repository method that only runs a built document needs no real Mongo, and a typo in a field name must fail a test.
