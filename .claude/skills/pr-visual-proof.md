---
name: pr-visual-proof
description: Use when a change touches a live UI or UX surface and its screenshot has to go into a hive pull request, when a proof image in a PR renders as a broken-image icon, or when a proof link that displayed fine at review time now returns 404 because its branch was deleted on merge.
---

# PR Visual Proof

## Overview

Orchestrator rule 8 requires a rendered screenshot or recording in the pull
request before any UI-touching change merges or is claimed done. The artifact
has to **render as an inline image on the PR page**, not merely be linked.

Post it with the script. Do not commit the image to `docs/proof/` and link it
by hand: a `raw.githubusercontent.com` URL pinned to the PR's own branch name
renders at review time and then 404s the instant the branch is deleted, which
is exactly what this repo's squash-and-delete-branch merge policy does to
every PR on merge. That is the PR #867 failure, and it is why the owner
reported that our visual proof does not display for him.

## The one command

```bash
scripts/post-pr-visual-proof.sh <pr-number> <image1> [image2 ...] \
  [--caption "one line of context"]
```

Run from inside any checkout of this repo (worktree is fine) with `gh`
authenticated. Pass every image in one invocation; they land in one comment
rather than one comment per image.

## Before you run it: masking is your job, not the script's

The script uploads bytes. It cannot look at pixels.

Any URL carrying a credential in a query string or fragment (invitation
accept, password reset, magic link, OAuth callback) must already be masked in
the image **pixels** before you call this script. PR #578 leaked four real
invite tokens this way.

Only the image moves to the release. Commit the capture's text log under
`docs/proof/<slug>/` in the same PR: `npm run lint:proof-tokens`
(`tools/lint-no-token-in-proof-captures.mjs`) scans that directory and
nothing else, so a log left in a scratch dir or a comment is unscanned and
the check reports green over the old corpus instead of failing.

Redact the credential in the text log yourself, but know what the linter
actually gives you: it is not just a "did you remember" reminder, it
entropy-scans every text file it finds for known credential param names
(`token`, `code`, `access_token`, `refresh_token`, `token_hash`,
`hashed_token`, `email_otp`) plus bare JWTs, and fails the build on a match
that isn't an obvious placeholder. That is a real automated backstop for the
text half, the reason PR #578's leak was catchable going forward. Screenshot
pixels have no equivalent: nothing scans the upload itself either, since a
release asset is blob storage on a tag, not a git object, so GitHub secret
scanning and GitGuardian never see it. A credential in pixels has no
automated backstop at all.

**This repository is public and an upload is permanent.** The script prints
every file it is about to publish before it uploads. Read that list. A leaked
asset is pulled individually, never by deleting the release:

```bash
gh release delete-asset visual-proof-assets <asset-name> --repo <owner/repo> --yes
```

Deleting the asset does not unpublish what was already fetched or indexed.
Treat a leaked credential as compromised and revoke it, do not just delete.

## Where captures must not be written

Never write a capture into a test reporter's own output directory. Playwright's
HTML reporter **clears its output directory before writing**, so proof captured
into `playwright-report/` (or whatever `outputFolder` is set to) is created and
then deleted, and the run looks like it produced nothing. This cost a full
capture pass on PR #951. Write captures to a scratch dir or to
`docs/proof/<slug>/`, never to a directory a reporter owns.

## What renders, and what does not

Raced empirically on scratch PR #959 with real browser screenshots, logged in
and out. Full record: `.wolf/decisions.md` D-042. Do not re-derive this.

| Mechanism | Renders inline | Survives branch deletion |
|---|---|---|
| Release asset (`releases/download/...`) | Yes | Yes. Not reachable through any branch. **This is what the script uses.** |
| `raw.githubusercontent.com` pinned to a branch **name** | Yes, until the branch dies | **No.** 404s on merge. |
| `raw.githubusercontent.com` pinned to a commit **SHA** | Yes | Yes, but on undocumented retention behavior, and one slip from repeating #867. Not the standard. |
| `blob/...` link | **No.** Serves HTML, not bytes. | n/a |
| `data:` URI in the comment body | **No.** Sanitizer strips it. | n/a |

The drag-and-drop upload that produces `user-attachments/assets/...` URLs has
no REST or GraphQL equivalent: web-UI only, session-cookie and CSRF
authenticated. Confirmed, not assumed. Do not try to drive it with a browser
session.

## Common mistakes

- **Video.** GitHub does not render an `.mp4` or `.webm` release asset as a
  player. For a recording, post a still frame through the script and attach
  the video separately.
- **Deleting the `visual-proof-assets` release.** Every proof ever posted
  links into it by name. Pull the one asset instead.
- **Filenames.** GitHub rewrites an asset name on upload (`name space
  test.png` publishes as `name.space.test.png`), which would leave the URL
  pointing at nothing. The script folds every name into
  `[A-Za-z0-9._-]` first so the published name and the URL always match; the
  asset name you see in the comment is therefore not always the local one.
- **A non-image, or a symlink.** Both are refused: an upload is public and
  permanent, and `cp` would otherwise follow a link and publish bytes you
  never looked at.
- **Reporting done off a text description.** Not proof. Rule 8 is explicit.
