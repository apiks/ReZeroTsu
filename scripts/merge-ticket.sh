#!/bin/sh
# Merge one finished ticket branch into the branch checked out here, committing only when every check passes.
#   scripts/merge-ticket.sh <ticket-branch-or-commit> [message]
# Run it in the integration worktree with a clean tree. It pushes nothing.
#   - A branch that is already part of HEAD (an empty branch, a wrong name, a repeat merge) is refused.
#   - The merge is staged, not committed. A failing check leaves the merge staged and exits 1: fix forward and run
#     `git commit --no-edit`, or run `git merge --abort`. The integration branch never holds a red merge commit.

set -e
cd "$(git rev-parse --show-toplevel)"

branch="${1:?usage: scripts/merge-ticket.sh <ticket-branch-or-commit> [message]}"
message="${2:-Merge $branch}"

if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "working tree has uncommitted changes; commit or stash them first"
  exit 1
fi

if git merge-base --is-ancestor "$branch" HEAD; then
  echo "nothing to merge: $branch ($(git rev-parse --short "$branch")) is already part of $(git rev-parse --abbrev-ref HEAD)."
  echo "Check the branch name against the implementer's reported branch and commit."
  exit 1
fi

git merge --no-ff --no-commit "$branch"

if ! scripts/check.sh; then
  echo
  echo "checks failed; the merge of $branch is staged and not committed."
  echo "Fix forward then run: git commit --no-edit    Or give up with: git merge --abort"
  exit 1
fi

git commit -q -m "$message"
git log --oneline -1
