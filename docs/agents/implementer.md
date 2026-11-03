# Implementer contract

Read by an implementer subagent that builds one ticket. The prompt names the ticket and the integration branch; everything else is here.

## Build

- Read `AGENTS.md`, `GLOSSARY.md` and `CODING_STANDARDS.md`, then the ticket and its parent spec.
- Load the `tdd` skill before the first line of code. Write each test first and run it red: a failing run that names the behaviour. Test through the existing seams and fakes.
- Name things with `GLOSSARY.md` terms.
- A change described as behaviour-preserving follows the "Behaviour parity" standard: every deleted or moved guard gets a named new home.

## Done

Done means `scripts/check.sh` passes on the merged tree. It runs gofmt over tracked files, build, vet, `go test -count=1 ./...` and golangci-lint when installed (CI runs it).

## Finish

- Commit on your branch. End the message with the attribution line from the session's commit-attribution reminder.
- Merge the integration branch tip into your branch, then rerun the checks.
- Report: branch name, final commit hash, one line per acceptance criterion, surprises, and for a behaviour-preserving change the guard-to-new-home mapping.
- Report the red run for each acceptance criterion: the test name and its failure line from before the code existed. A criterion without one was not built test-first; say so.
- Leave pushing, pull requests and issues to the orchestrator.
