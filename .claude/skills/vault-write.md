---
name: Vault Write
description: Use when persisting a final plan, spec, ADR, decision record, or brainstorm/session outcome to the Obsidian vault, per the obsidian-working-docs rule, including updating the MOC index. Covers picking a filename that will not silently clobber an existing document and the vault's append-a-correction convention for updating one.
---

## Vault Write

Authority: `~/.claude/rules/common/obsidian-working-docs.md` (global policy) and
this repo's `CLAUDE.md` pointer to it. This skill is the mechanical how-to.

Vault root: `/mnt/c/Users/sakib/Documents/ObsidianVault/hive/`. One flat folder, no
subfolders, kebab-case filenames. Write directly to this WSL-mounted path — no git,
no PR, no mirroring for the vault file itself (`.wolf/decisions.md`, if you also
touch it per Step 4, IS tracked in the hive repo and goes through the normal
branch+PR flow like any other tracked file). This path only resolves from a
Claude Code CLI session on the owner's WSL2 box; if `ls` on it fails outright,
the mount isn't attached in your environment (a containerized agent, a
non-WSL host) — say so plainly rather than retrying blind or inventing a
fallback location.

### Step 1: never overwrite silently

```bash
ls "/mnt/c/Users/sakib/Documents/ObsidianVault/hive/<planned-filename>.md" 2>/dev/null
```

If it exists, do not overwrite it. Pick one:

- **Same topic, a new pass on the same day/window** (a re-score, a correction, a
  status change): append a new section to the END of the existing file instead of
  a new document. This is the vault's own convention — `session-2026-08-25-claude-
  similarity-scorecard.md` appended `# Re-score, 2026-08-25 evening (live
  measurement)` rather than becoming a second file, and `spec-2026-08-16-hive-ui-
  redesign.md` carries an inline `> Correction, 2026-08-23` callout marking
  specific passages superseded without deleting them. Mark the earlier passage
  superseded in place (a short note pointing at the new section), never delete it.
- **Different topic, filename collision only**: pick a more specific filename.
- **Genuinely unsure which**: stop and ask rather than guess by overwriting.

### Step 2: filename and frontmatter

`<type>-YYYY-MM-DD-<kebab-slug>.md` — observed prefixes: `plan-`, `spec-`,
`decision-`, `session-` (used for discussion/session records), `discussion-`,
`research-`, `audit-`, `adr-`.

```yaml
---
type: plan|spec|architecture|decision|discussion
date: YYYY-MM-DD
tags: [tag1, tag2]
status: draft|locked|implemented|complete|superseded ...
source: <what triggered this — owner brief, PR #, issue #>
---
```

Match this exactly; every retrieval skill in this set (`vault-search`, `vault-
decisions`, `vault-staleness`) depends on these fields being present and accurate.

### Step 3: excluded content — never write this to the vault

Session scratch, auto-memory content, and anything with real secrets, credentials,
or production data. Same hygiene bar as this repo's public-facing HYGIENE rule
(`.wolf/cerebrum.md`): strip it before writing, don't rely on the vault being
private.

### Step 4: if this records a decision, also update `.wolf/decisions.md`

Any doc of `type: decision`, or any doc recording an owner ruling, needs a matching
terse entry in the repo-tracked ledger. Resolve your own repo root first — a
worktree's copy of `.wolf/decisions.md` can differ from the shared main
checkout's, same caveat as `vault-decisions`:

```bash
cd "$(git rev-parse --show-toplevel)"
grep -o '^- D-[0-9]\+' .wolf/decisions.md | sort -t- -k3 -n | tail -1
```

Append the next unused `D-ID` in the ledger's own format:
`D-ID | decision (1 line) | source | date`. "1 line" means one physical line in
the file (no newlines), not necessarily short — read the real entries in
`vault-decisions` (D-032, D-036, D-044, D-045) before writing yours; the
ledger's working convention is a dense, reasoned paragraph on one line, not a
terse stub, whenever the reasoning matters to a future reader. Point the entry
at the new vault file. If this decision revokes, retires, supersedes, amends,
or moots a prior `D-ID`, edit that prior line in place to prefix it (`REVOKED
2026-... by D-NNN`, etc., or the AMENDED/partial-SUPERSEDED shape `vault-
decisions` Step 1 describes) rather than deleting it — this is the exact
convention `vault-decisions` depends on to catch dead rules. This ledger edit
is a normal tracked-file change: commit it on your branch and let it ride
through the PR like any other code/doc change, it is not subject to the
buglog.jsonl main-only restriction.

### Step 5: update `MOC-plans.md`

Add a row to the matching table (Decisions or Plans) with a `[[wikilink]]`, the
date, and a one-paragraph summary in the vault's existing voice: dense, specific,
naming the concrete trigger and the concrete outcome — match the density of the
rows already there, not a one-line stub. This step is not optional: `MOC-plans.md`
is the first place `vault-search` looks, and a document left off it is effectively
invisible to every future retrieval in this skill set.

`MOC-plans.md` is a plain filesystem write with no git/PR layer to catch a
concurrent clobber (unlike `.wolf/decisions.md` in Step 4, which gets that
protection for free from the normal branch+PR flow). If multiple agents may be
writing to the vault around the same time, re-read the file immediately before
you append rather than trusting a copy you read earlier in the session.

### Non-goals

- Do not restructure, tidy, reformat, or bulk-edit unrelated parts of the vault
  while you are in there for one write. It holds the owner's real working record.
- Do not use this skill to write anything into `docs/superpowers/plans|specs` in
  the repo — that path is explicitly overridden by the vault per the global rule.
