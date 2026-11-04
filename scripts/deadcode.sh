#!/bin/sh
# Report functions that nothing reaches from main or from a test. The linter's unused check skips exported
# names, so a removed feature leaves its exported helpers behind; this catches them.
#   scripts/deadcode.sh
# A function that must stay unreachable for now goes in the list below with the reason, as .golangci.yml does.

set -e
cd "$(git rev-parse --show-toplevel)"

# Known leftovers (decide whether to delete, then drop the line):
#   removeFeedsForSubredditAndPostType  predates this check
known='removeFeedsForSubredditAndPostType'

if ! out=$(go run golang.org/x/tools/cmd/deadcode@v0.38.0 -test ./... 2>&1); then
  echo "deadcode could not run:"
  echo "$out"
  exit 1
fi

found=$(printf '%s\n' "$out" | grep -v -E "$known" || true)
if [ -n "$found" ]; then
  echo "unreachable functions (delete them, or list them in scripts/deadcode.sh with a reason):"
  echo "$found"
  exit 1
fi
