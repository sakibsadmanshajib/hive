#!/usr/bin/env bash
# post-pr-visual-proof.sh -- Post local screenshot(s) into a pull request
# comment as images that actually render inline on github.com.
#
# Why this exists, and why it is not a raw.githubusercontent.com link on the
# PR's own branch (.wolf/decisions.md D-042 has the full empirical record)
# ---------------------------------------------------------------------------
# Four candidates were raced on a real scratch PR (#959) and screenshotted
# with a real browser, logged in and out, before this script was written:
#
#   1. raw.githubusercontent.com pinned to the PR's own branch NAME: renders
#      fine right up until the branch is deleted, then 404s immediately
#      (verified with curl showing a cache MISS 404 straight from origin,
#      seconds after `git push origin --delete`). This repo's merge policy is
#      squash-and-delete-branch, so this is exactly the failure PR #867 hit:
#      proof that expires the moment the PR it was meant to prove merges.
#   2. A `blob/` link, embedded as a markdown image or as a bare link: never
#      renders as an image at all (blob URLs serve an HTML page, not raw
#      bytes). Confirmed broken-image icon in the rendered comment.
#   3. A release asset, referenced by its stable releases/download/ URL:
#      renders inline, and is not reachable through any branch at all, so
#      branch deletion cannot touch it. This is what this script uses.
#   4. A data: URI embedded directly in the comment body: GitHub's markdown
#      sanitizer strips it, confirmed broken-image icon, never renders.
#
# A raw link pinned to a commit SHA (rather than a branch name) also
# survives branch deletion in practice, because GitHub retains a merged or
# open PR's head commit via its own internal ref indefinitely. That is a
# real, working alternative, but it depends on an undocumented retention
# behavior and is one easy mistake (pinning to the branch name instead of
# the SHA) away from silently repeating PR #867. The release asset has no
# such trap: there is no branch-vs-SHA distinction to get wrong.
#
# The true drag-and-drop attachment host (the one that produces
# github.com/user-attachments/assets/... URLs) has no REST or GraphQL
# equivalent: confirmed by a 404 on the plausible REST path and no matching
# field on GraphQL's Issue type. It is web-UI only, authenticated by browser
# session cookies and a CSRF token, not by a token this script can hold. Do
# not attempt to drive it with anyone's browser session.
#
# CREDENTIAL MASKING IS THE CALLER'S JOB, NOT THIS SCRIPT'S
# -----------------------------------------------------------
# This script uploads bytes and posts a comment. It cannot look at pixels.
# Per orchestrator.md rule 8: any URL carrying a credential in a query string
# or fragment (invitation accept, password reset, magic link, OAuth
# callback) must be masked in both the text log and the screenshot PIXELS
# before calling this script. `npm run lint:proof-tokens` only catches the
# text half. Masking the image is on whoever captured it. This repository is
# public and permanent once uploaded: review every image before invoking
# this script, not after.
#
# Usage:
#   scripts/post-pr-visual-proof.sh <pr-number> <image1> [image2 ...] \
#     [--caption "one line of context"]
#
# Requires: gh CLI, authenticated, run from inside a checkout of this repo
# (or set GH_REPO=owner/repo).
set -euo pipefail

usage() {
  echo "Usage: $0 <pr-number> <image1> [image2 ...] [--caption \"text\"]" >&2
  exit 1
}

[ $# -ge 2 ] || usage

pr="$1"
shift

caption=""
images=()
while [ $# -gt 0 ]; do
  case "$1" in
    --caption)
      [ $# -ge 2 ] || usage
      caption="$2"
      shift 2
      ;;
    *)
      images+=("$1")
      shift
      ;;
  esac
done
[ "${#images[@]}" -ge 1 ] || usage

for img in "${images[@]}"; do
  [ -f "$img" ] || { echo "::error:: no such file: $img" >&2; exit 1; }
done

repo="${GH_REPO:-$(gh repo view --json nameWithOwner --jq .nameWithOwner)}"
# Created once, then reused forever. Never delete this release: every proof
# image this script has ever posted links into it by name. A single
# credential-bearing asset found later can be pulled with
# `gh release delete-asset visual-proof-assets <name> --repo "$repo" --yes`
# without touching any other proof this release holds.
release_tag="visual-proof-assets"

if ! gh release view "$release_tag" --repo "$repo" >/dev/null 2>&1; then
  gh release create "$release_tag" --repo "$repo" \
    --title "Visual proof assets" \
    --notes "Permanent host for PR visual-proof screenshots (scripts/post-pr-visual-proof.sh). Never delete this release. Remove one leaked asset with \`gh release delete-asset $release_tag <name> --repo $repo --yes\` rather than the whole release." \
    --target main
fi

body_file="$(mktemp)"
stage_dir="$(mktemp -d)"
trap 'rm -f "$body_file"; rm -rf "$stage_dir"' EXIT

{
  echo "## Visual proof"
  echo
  if [ -n "$caption" ]; then
    echo "$caption"
    echo
  fi
} > "$body_file"

for img in "${images[@]}"; do
  base="$(basename "$img")"
  stamp="$(date -u +%Y%m%d%H%M%S)"
  # Namespaced by PR, a UTC timestamp and a short random suffix so two
  # proofs on the same PR, or two agents proving two different PRs at the
  # same second, never collide on this release's one flat asset namespace.
  # `gh release upload` derives the published asset name from the file it
  # is given, not from a `#label` suffix (that only sets a cosmetic display
  # label), so the rename has to happen on disk first.
  name="pr${pr}-${stamp}-${RANDOM}-${base}"
  staged="${stage_dir}/${name}"
  cp "$img" "$staged"
  gh release upload "$release_tag" "$staged" --repo "$repo" >/dev/null
  {
    echo "![${base}](https://github.com/${repo}/releases/download/${release_tag}/${name})"
    echo
  } >> "$body_file"
done

gh pr comment "$pr" --repo "$repo" --body-file "$body_file"
