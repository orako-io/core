#!/usr/bin/env bash
# Strip the retired `orako`/`relay` CLI binaries (and stray graphify-out blobs)
# from the ENTIRE git history — audit finding H1. This shrinks .git from ~79 MB
# to ~7 MB before the repo is made public. VALIDATED on a mirror clone
# (2026-07-17): 78M -> 7.0M, 0 relay blobs left, all 248 commits + source intact.
#
# ⚠️ DESTRUCTIVE, IRREVERSIBLE, HISTORY-REWRITING.
#   - Rewrites EVERY commit hash. Run this as the VERY LAST step before going
#     public, AFTER every other branch (security, editions, …) is merged/pushed.
#   - Requires a FORCE-PUSH to origin and re-clones for all collaborators.
#   - Do NOT run mid-development: it orphans existing feature branches.
#
# Prereq: git-filter-repo (`brew install git-filter-repo`).
#
# Usage (deliberate, not automated):
#   1. Merge/push everything you intend to keep.
#   2. Run this script from a FRESH clone of the repo.
#   3. Inspect the result (git log, du -sh .git, a build).
#   4. Only then: git push --force --all && git push --force --tags
set -euo pipefail

if ! command -v git-filter-repo >/dev/null 2>&1; then
  echo "git-filter-repo not found — brew install git-filter-repo" >&2
  exit 1
fi

read -r -p "This REWRITES ALL HISTORY. Type 'rewrite' to proceed: " confirm
[ "$confirm" = "rewrite" ] || { echo "aborted"; exit 1; }

echo "before: $(du -sh .git | cut -f1)"

# Remove the retired CLI binary dirs and the historical graphify-out blobs.
git filter-repo --force \
  --path npm/platforms \
  --path graphify-out \
  --invert-paths

git reflog expire --expire=now --all
git gc --prune=now --aggressive

echo "after:  $(du -sh .git | cut -f1)"
echo "remaining relay blobs: $(git rev-list --objects --all | grep -c 'npm/platforms' || true)"
echo
echo "Inspect, build, then force-push manually:"
echo "  git remote add origin <url>   # filter-repo drops the remote by design"
echo "  git push --force --all && git push --force --tags"
