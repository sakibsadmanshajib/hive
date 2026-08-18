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
# text half, and it only ever scans committed files under docs/proof/.
#
# Nothing scans what this script uploads. A release asset is blob storage
# attached to a tag, not a git object, so GitHub secret scanning, push
# protection and GitGuardian never see it, and neither does our own linter.
# Under the old committed-file mechanism a leaked credential had two chances
# to be caught after the fact; here it has none. Masking the image is on
# whoever captured it, and it is the only control. This repository is public
# and an upload is permanent: review every image before invoking this script,
# not after.
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

# GitHub rewrites a release asset's name on upload: characters outside a safe
# set are substituted, so an asset uploaded as `name space test.png` is
# published as `name.space.test.png` (verified against this repo's own release,
# not assumed). This script builds the markdown URL from the name it chose
# locally, so any name GitHub would rewrite yields a URL that points at
# nothing: a 404 image, posted by a script that exited 0. Markdown compounds
# it, terminating a URL at the first space and treating `#` as a fragment.
# Folding the name into the safe set first makes the published name and the URL
# identical by construction. Names already inside the set upload verbatim
# (again, verified against existing assets).
sanitize_asset_name() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '_'
}

# Preflight self-test, mirroring the MUST_CATCH/MUST_ALLOW pattern the repo's
# tools/lint-*.mjs guards use. If the sanitizer ever stops holding, this fails
# here rather than silently posting a proof that does not render.
[ "$(sanitize_asset_name 'name space test.png')" = "name_space_test.png" ] &&
  [ "$(sanitize_asset_name 'a#b?c&d%e.png')" = "a_b_c_d_e.png" ] &&
  [ "$(sanitize_asset_name 'safe-name_1.0.png')" = "safe-name_1.0.png" ] || {
  echo "::error:: sanitize_asset_name self-test failed; refusing to post a proof that may not render" >&2
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
    -*)
      # Without this arm an unknown flag falls through to the catch-all and is
      # treated as an image path, surfacing later as a misleading "no such
      # file: --verbose". It also keeps a leading-dash path out of `basename`
      # and `cp`, which would otherwise parse it as their own option.
      echo "::error:: unknown option: $1 (for a file whose name starts with a dash, pass ./$1)" >&2
      usage
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
  # `cp` dereferences a symlink by default, so a link sitting in a capture
  # directory would publish its target's bytes rather than the file the caller
  # named and looked at. Refuse rather than resolve: what gets reviewed for
  # credentials must be what gets uploaded.
  if [ -L "$img" ]; then
    echo "::error:: refusing a symlink (publish the real file you reviewed): $img" >&2
    exit 1
  fi
  # An upload here is public and permanent, so refuse anything that is not an
  # image rather than discover afterwards that a stray shell glob matched a
  # log, a dotenv or a dump. GitHub would happily host any of them.
  case "${img,,}" in
    *.png|*.jpg|*.jpeg|*.gif|*.webp) ;;
    *)
      echo "::error:: not an image (png/jpg/jpeg/gif/webp): $img" >&2
      echo "::error:: GitHub renders no other type inline, and an upload cannot be unpublished." >&2
      exit 1
      ;;
  esac
done

repo="${GH_REPO:-$(gh repo view --json nameWithOwner --jq .nameWithOwner)}"
# Created once, then reused forever. Never delete this release: every proof
# image this script has ever posted links into it by name.
#
# If a credential-bearing asset is found later, REVOKE THE CREDENTIAL FIRST,
# then pull the one asset with
# `gh release delete-asset visual-proof-assets <name> --repo "$repo" --yes`,
# which leaves every other proof in this release untouched. Deleting the asset
# is not revocation and does not close the incident: the token stays valid
# until revoked or consumed, the PR comment and its download URL remain in the
# timeline and the API, the notification emails GitHub already sent are not
# retractable, and release-asset downloads are not logged anywhere we can read,
# so the exposure cannot be bounded after the fact.
release_tag="visual-proof-assets"

# Resolve the PR BEFORE anything is uploaded. The comment is posted last, so
# without this a mistyped or non-existent PR number is only discovered after
# every image is already public and permanent, with no comment referencing it
# and no way to unpublish.
#
# `--json state` is load-bearing: it forces the API round trip. `--json number`
# looks like the natural choice and is useless here, because gh answers it
# from the argument itself and exits 0 for a PR that does not exist (verified:
# `gh pr view 99999999 --json number` prints `{"number":99999999}`, status 0).
# That made an earlier version of this guard a no-op that published assets for
# a nonexistent PR. Do not "simplify" this field.
gh pr view "$pr" --repo "$repo" --json state >/dev/null || {
  # Do not name a cause this has not established: the same failure covers a
  # nonexistent PR, an unauthenticated or under-scoped gh, and a network error.
  echo "::error:: could not resolve pull request $pr in $repo (see the gh error above: it may not exist, or gh may be unauthenticated). Nothing was uploaded." >&2
  exit 1
}

# Non-blocking manifest. This runs unattended under agents, so a confirmation
# prompt would hang; printing what is about to become public still gives the
# caller, and anyone reading the transcript afterwards, the chance to catch a
# wrong file before it is irreversible.
{
  echo "About to publish ${#images[@]} file(s) to a PUBLIC, PERMANENT release."
  echo "  repo:    $repo"
  echo "  release: $release_tag"
  echo "  pr:      #$pr"
  for img in "${images[@]}"; do echo "  file:    $img"; done
  echo "An uploaded asset cannot be unpublished. Credentials must already be masked in the PIXELS."
} >&2

if ! gh release view "$release_tag" --repo "$repo" >/dev/null 2>&1; then
  # Several agents run this concurrently, so two can both see no release and
  # both try to create it. Losing that race is not an error: re-check instead
  # of aborting and losing the proof this run was meant to post.
  gh release create "$release_tag" --repo "$repo" \
    --title "Visual proof assets" \
    --notes "Permanent host for PR visual-proof screenshots (scripts/post-pr-visual-proof.sh). Never delete this release. Remove one leaked asset with \`gh release delete-asset $release_tag <name> --repo $repo --yes\` rather than the whole release." \
    --target main \
    || gh release view "$release_tag" --repo "$repo" >/dev/null
fi

# One temp object, one trap, installed before anything else can fail: with two
# separate mktemp calls and the trap after both, a failure of the second leaks
# the first, because `set -e` exits before the trap exists.
stage_dir="$(mktemp -d)"
trap 'rm -rf "$stage_dir"' EXIT
body_file="${stage_dir}/comment-body.md"

{
  echo "## Visual proof"
  echo
  if [ -n "$caption" ]; then
    echo "$caption"
    echo
  fi
} > "$body_file"

for img in "${images[@]}"; do
  base="$(basename -- "$img")"
  stamp="$(date -u +%Y%m%d%H%M%S)"
  # Namespaced by PR, a UTC timestamp and a short random suffix so two
  # proofs on the same PR, or two agents proving two different PRs at the
  # same second, never collide on this release's one flat asset namespace.
  # `gh release upload` derives the published asset name from the file it
  # is given, not from a `#label` suffix (that only sets a cosmetic display
  # label), so the rename has to happen on disk first.
  name="$(sanitize_asset_name "pr${pr}-${stamp}-${RANDOM}-${base}")"
  staged="${stage_dir}/${name}"
  cp -- "$img" "$staged"
  gh release upload "$release_tag" "$staged" --repo "$repo" >/dev/null
  url="https://github.com/${repo}/releases/download/${release_tag}/${name}"
  # Record every published URL as it goes. An upload failure part way through
  # a multi-image run aborts with the earlier images already public and no
  # comment posted, and the name embeds $RANDOM, so without this line the
  # caller cannot name what they need to clean up.
  echo "  published: $url" >&2
  {
    # The label is the sanitized name, not the raw basename: a basename
    # containing `]` would close the label early and the image would not
    # render, from a run that still exited 0. Alt text is cosmetic.
    echo "![${name}](${url})"
    echo
  } >> "$body_file"
done

gh pr comment "$pr" --repo "$repo" --body-file "$body_file"
