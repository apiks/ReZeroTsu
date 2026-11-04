# Coding standards

Read during review. Mechanical rules (formatting, unused code, unreachable functions, direct Discord sends, show-name matching, text sliced at a Discord length limit, raw Discord mentions in docs) are enforced by checks, not listed here.

## Behaviour parity

A change described as behaviour-preserving keeps every check the old code ran on each call running on each call.

- A cache, memo or hoisted lookup replays the preconditions of the uncached path before it returns an entry. Permission, state and ownership checks are preconditions.
- For each deleted or moved call, name where its guard now runs. A guard with no new home is a finding.
- Moving a call into a callee moves it relative to the callee's guards. List the checks that ran after the call at the old site; each still runs after it at the new one. Trace a late-ending path too: a wait that ends in success after shutdown must still reach the shutdown check before any send.
- The spec and its tickets can disagree with each other ("resolve on a miss" beside "behaviour unchanged"). Review against the behaviour; report the disagreement.

## Glossary vocabulary

Names, comments and test titles use the terms in `GLOSSARY.md`. A term listed under its `_Avoid_` line is a finding, as is a coined synonym for a defined term.

Three archcheck rules cover the mechanical part. `checkAvoidTerms` flags the Avoid terms that mean nothing else (such as "moderator role" and "giveaway") in identifiers and string literals. `checkAvoidPhrases` flags every multi-word Avoid term in `GLOSSARY.md` (such as "post type" and "ONA (Chinese)") in comments and string literals. `checkAvoidNames` reads those same terms as words in identifiers and file names, test files included, so `AirType`, `trackFilter` and `track_setting_test.go` are flagged; it skips the stored `post_type` of an autopost. The same terms are flagged in `README.md` and `docs/release-notes.md`. A line that must quote an upstream or stored name carries `archcheck:allow`. Review the rest: single everyday words like "pick", "roll" and "cache", every test title, and every user-facing sentence.

"Sub" names the Sub track only, and "subscription" is spelled out. A name such as `subTracks` or a test title such as "plain sub removed" is a finding, because a reader cannot tell the track from the subscription. `checkSubscriptionNames` flags the plural "subs" as a word in any identifier, test files included, because no track is called that; write "subscriptions", or "subcommands" for a command's children. The stored `anime_subs` and `AnimeSubs` are exempt. The singular is left to review, since `TrackSub` and `subCore` both use it.

## Delivery runs and sent state

A change to what a delivery run records as sent is checked against three cases, and each has a test that runs the seeding and then the delivery, the way a restart does:

- A new UTC day: the reset writes the new identity and repeats nothing.
- A restart mid-day: seeding that finds a row missing from today's document records an already-aired, undelayed row as posted, and leaves a row still to air unnotified.
- A setting changed mid-day (the preferred track, donghua): rows the new setting selects get entries the same way, and earlier airings are not posted again.

A test of the delivery run alone passes while the seeding path reposts, so the seeding is part of every such test.

## Test stubs

A package-wide stub set in `TestMain` lives in the package's `main_test.go`, and at least one test asserts how often the stubbed call runs. Without a count, a lost or doubled call passes every test that merely needs the stub to exist.

## Mongo writes

Build each filter and update document in a function that takes plain values, and test the document it returns. A repository method that only runs a built document needs no real Mongo.

`checkBsonFieldNames` fails a stored name that is no bson tag of its package. It reads the string keys of each top-level `bson.M` filter, and of the operand of an update operator such as `$set`, segment by segment along a dotted path. It leaves alone documents nested in another document, `bson.D`, pipelines and keys built at run time. An aggregation expression or an array filter carries `archcheck:allow` on its first line. The per-write test therefore covers the shape and values of the document, and a write that builds a key at run time keeps a test that its prefix and its field are bson tags, in the style of the filter-shape tests beside it.
