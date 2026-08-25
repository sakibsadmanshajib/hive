---
type: proof
date: 2026-08-25
source: PR #1193, CodeRabbit follow-up review
---

# Fix: duplicate "Artifacts" heading when artifacts are present

CodeRabbit's follow-up pass on PR #1193 caught a regression in the same
change that added the page title fix: `+page.svelte` added a shared
`hv-panel-head` (`h1` + subtitle) at the top of the page, but the populated
list branch (`artifacts.length > 0`, nothing selected) still rendered its own
"Artifacts" title and subtitle above the list. The empty state, which is
what the earlier capture showed, only ever renders the shared head, so the
duplicate was invisible there and only appeared once a chat actually had an
artifact in it.

Fix: removed the second heading block from the populated-list branch. The
shared `hv-panel-head` above is now the only title in every state (loading,
error, empty, populated, and the single-artifact preview, which never had
its own title).

## Capture

`t1193-06-artifacts-populated-single-heading.png`: signed-in admin account
with a seeded chat containing an HTML artifact (`t1193 artifact seed`),
`/artifacts` showing exactly one "Artifacts" heading and one subtitle above
the populated list. The chat was seeded via `POST /api/v1/chats/new` from
inside the signed-in page context (no manual authoring of artifact content
through the composer needed to prove this), and removed no other page
behavior.

Same substrate as the thread-3 capture: standalone container built from this
branch, signed in with a short-lived admin token minted by
`scripts/owui-mint-admin-token.py`, no password touched. No credential-bearing
URL appears in the capture.
