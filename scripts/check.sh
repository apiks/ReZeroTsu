#!/bin/sh
# The repo's checks, run the same way by the pre-commit hook, CI and agents.
#   scripts/check.sh        gofmt, build, vet, tests, golangci-lint (when installed)
#   scripts/check.sh fast   gofmt and vet only (the pre-commit hook)
#   scripts/check.sh fmt    gofmt only (CI)
# gofmt reads tracked files only, so another worktree's unfinished files under .claude/ never fail this run.

set -e
cd "$(git rev-parse --show-toplevel)"

mode="${1:-full}"

unformatted=$(git ls-files '*.go' | xargs gofmt -l)
if [ -n "$unformatted" ]; then
  echo "gofmt would change:"
  echo "$unformatted"
  exit 1
fi
[ "$mode" = fmt ] && exit 0

go vet ./...
[ "$mode" = fast ] && exit 0

go build ./...
go test -count=1 ./...
if command -v golangci-lint >/dev/null 2>&1; then
  golangci-lint run ./...
else
  echo "golangci-lint not installed; CI runs it"
fi
