---
name: Vault Staleness Check
description: Use before acting on, citing, or shipping anything a vault document claims (filing an issue against a gap it names, telling an integrator a status it records, or building on a scorecard's verdict) to verify each claim against current origin/main, open issues, or the live deployment instead of trusting the document's date.
---

## Vault Staleness Check

A vault document's `date` frontmatter is a fact about when it was written. It is
not a fact about current state. This project has shipped real defects from
skipping that distinction: a parity scorecard listed five chat slices as
outstanding after all five had shipped; an issue was filed for a backend that had
shipped the day before; a published spec told integrators an endpoint was
`planned_for_launch` after it had been live for months. Retrieval without this
check reproduces the same failure with better citations.

### Step 1: read the doc's own dating, and how old it actually is

Frontmatter `date` and `status`. Compute the gap to today explicitly: "written
11 days ago" changes how much you trust a claim more than the date alone conveys.

### Step 2: extract every checkable claim, don't verify the doc's vibe

Pull out, from the target section specifically (not the whole file):

- PR/issue numbers cited
- File paths, functions, or endpoints named
- Verdict words: shipped / outstanding / planned / in progress / broken / missing
- Any "is live" / "returns X" claim about a deployed surface

### Step 3: verify each claim against the cheapest real source, never the doc itself

| Claim shape | Check |
|---|---|
| Cites a PR/issue number | `gh pr view <n> --json state,mergedAt,title` / `gh issue view <n> --json state,closedAt`. A PR merged AFTER the doc's `date` is exactly the staleness this skill exists to catch. |
| Names a file/function as missing, present, or behaving a certain way | `git fetch origin main` then `git log origin/main -1 --format=%ci -- <path>` and `grep` the path on `origin/main` (`git show origin/main:<path>`), not the local worktree, which can itself be stale or mid-branch. Compare last-touch date to the doc's date. |
| "Outstanding" / "not yet shipped" | `gh issue list --search "<feature>"` and `gh pr list --search "<feature>" --state merged`, rather than trusting the doc's own bullet. |
| "Is live" / a deployed endpoint's behavior | Hit the actual deployed surface (curl, or the relevant e2e/live-auth path per this repo's `docs/live-test-auth.md`) rather than trust the doc. |

### Step 4: check for a later pass before treating the first hit as final

This vault appends corrections in the same file rather than rewriting it, see
`vault-search` for the mechanics (`# Re-score`, `> Correction`, later dated
headings). A claim's true current status may be a later section of the same
document you are checking, not a different source. Grep for that before reaching
for `git`/`gh` at all; it is cheaper and sometimes already answers the question.

### Step 5: report shape

A table, one row per claim:

`claim | doc says | current reality (with the command/output that proved it) |
verdict (confirmed-current / stale, superseded by <ref> / cannot-verify, <reason>)`

Never carry "the vault says X" into a downstream artifact (a filed issue, a PR
description, a customer-facing spec) without this table attached. This skill does
not fix anything it finds stale: it reports the mismatch so whoever is about to
act on the document can decide what to do with accurate information.
