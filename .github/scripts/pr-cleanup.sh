#!/usr/bin/env bash

set -euo pipefail

if [[ -z "${GITHUB_EVENT_PATH:-}" ]]; then
  echo "GITHUB_EVENT_PATH is not available; skipping cleanup."
  exit 0
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required but not installed."
  exit 1
fi

merged="$(jq -r '.pull_request.merged // false' "$GITHUB_EVENT_PATH")"
if [[ "$merged" != "true" ]]; then
  echo "PR was not merged; nothing to clean up."
  exit 0
fi

pr_number="$(jq -r '.pull_request.number // ""' "$GITHUB_EVENT_PATH")"
head_ref="$(jq -r '.pull_request.head.ref // ""' "$GITHUB_EVENT_PATH")"
head_repo="$(jq -r '.pull_request.head.repo.full_name // ""' "$GITHUB_EVENT_PATH")"
base_repo="$(jq -r '.repository.full_name // ""' "$GITHUB_EVENT_PATH")"
default_branch="$(jq -r '.repository.default_branch // "main"' "$GITHUB_EVENT_PATH")"

if [[ -z "$head_ref" || -z "$base_repo" ]]; then
  echo "Missing PR branch metadata; skipping cleanup."
  exit 0
fi

if [[ "$head_ref" == "$default_branch" ]]; then
  echo "Head branch matches default branch; refusing to delete."
  exit 0
fi

if [[ "$head_repo" != "$base_repo" ]]; then
  echo "PR branch comes from fork ($head_repo); cannot delete branch in $base_repo."
  exit 0
fi

if gh api "repos/$base_repo/git/ref/heads/$head_ref" >/dev/null 2>&1; then
  encoded_ref="${head_ref//\//%2F}"
  gh api -X DELETE "repos/$base_repo/git/refs/heads/$encoded_ref" >/dev/null
  echo "Deleted merged branch: $head_ref"
else
  echo "Branch already deleted: $head_ref"
fi

if [[ -n "$pr_number" ]]; then
  labels="$(gh pr view "$pr_number" --json labels --jq '.labels[].name' || true)"
  if [[ "$labels" == *"status:in-progress"* ]]; then
    gh pr edit "$pr_number" --remove-label "status:in-progress"
    echo "Removed status:in-progress label from PR #$pr_number"
  fi
fi

echo "Post-merge cleanup completed."
