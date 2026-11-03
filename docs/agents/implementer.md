# Implementer contract

Read by an implementer subagent that builds one ticket. The prompt names the ticket and the integration branch; everything else is here.

## Build

- Read `AGENTS.md`, `GLOSSARY.md` and `CODING_STANDARDS.md`, then the ticket and its parent spec.
- Build the ticket test-first with the `tdd` skill. Test through the existing seams and fakes.
- Name things with `GLOSSARY.md` terms.
- A change described as behaviour-preserving follows the "Behaviour parity" standard: every deleted or moved guard gets a named new home.

## Done

Done means all of these pass on the merged tree:

```text
gofmt -l .            # prints nothing
go build ./...
go vet ./...
go test -count=1 ./...
golangci-lint run ./...   # when installed; CI runs it
```

## Finish

- Commit on your branch. End the message with the attribution line from the session's commit-attribution reminder.
- Merge the integration branch tip into your branch, then rerun the checks.
- Report: branch name, final commit hash, one line per acceptance criterion, surprises, and for a behaviour-preserving change the guard-to-new-home mapping.
- Leave pushing, pull requests and issues to the orchestrator.
