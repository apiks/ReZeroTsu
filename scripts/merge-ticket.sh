#!/bin/sh
# Merge one finished ticket branch into the branch checked out here, then run every check.
#   scripts/merge-ticket.sh <ticket-branch> [message]
# Run it in the integration worktree with a clean tree. It stops at the first failure and pushes nothing.

set -e
cd "$(git rev-parse --show-toplevel)"

branch="${1:?usage: scripts/merge-ticket.sh <ticket-branch> [message]}"
message="${2:-Merge $branch}"

if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "working tree has uncommitted changes; commit or stash them first"
  exit 1
fi

git merge --no-ff "$branch" -m "$message"
scripts/check.sh
