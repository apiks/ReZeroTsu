# Implementer contract

Read by an implementer subagent that builds one ticket. The prompt names the ticket and the integration branch; everything else is here.

## Build

- Reset onto the integration branch, then `git switch -c ticket/<number>-<slug>` and commit there. A worktree starts on a branch the harness names, and the orchestrator merges the branch you report.
- Read `AGENTS.md`, `GLOSSARY.md` and `CODING_STANDARDS.md`, then the ticket and its parent spec.
- Load the `tdd` skill before the first line of code. Write each test first and run it red on an assertion: stub every new symbol first, so the failure line names the behaviour and is not a compile error ("undefined: X"). Test through the existing seams and fakes.
- Name things with `GLOSSARY.md` terms.
- A change described as behaviour-preserving follows the "Behaviour parity" standard: every deleted or moved guard gets a named new home.
- Facts about AnimeSchedule's API (weeks, time zones, rate limits, response shapes) are in `docs/animeschedule-api.md`.

## Done

Done means all of these hold on the merged tree:

- `scripts/check.sh` passes. It runs gofmt over tracked files, build, vet, `go test -count=1 ./...` and golangci-lint when installed (CI runs it).
- The ticket's user-visible change has its line in `docs/release-notes.md`, under its spec's section, saying what members or moderators do differently. `/help` text and the README say the same wherever they describe that command. A later ticket does not pick these up.

## Finish

- Commit on your branch. End the message with the attribution line from the session's commit-attribution reminder.
- Merge the integration branch tip into your branch, then rerun the checks.
- Report: the output of `git rev-parse --abbrev-ref HEAD`, the final commit hash, one line per acceptance criterion, surprises, and for a behaviour-preserving change the guard-to-new-home mapping.
- Report the red run for each acceptance criterion: the test name and its failure line from before the code existed. A criterion without one was not built test-first; say so.
- Leave pushing, pull requests and issues to the orchestrator.
