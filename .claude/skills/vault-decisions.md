---
name: Vault Decisions
description: Use when asked what was decided about something, whether a rule or decision still holds, or before designing/implementing anything that could be governed by a prior owner ruling. Covers finding a decision AND checking whether it has since been revoked, retired, superseded, amended, or mooted, since returning a dead decision as current has repeatedly caused real defects.
---

## Vault Decisions

Two layers hold decisions, and they answer different questions:

- `.wolf/decisions.md` (repo root): terse `D-ID | decision | source | date` ledger,
  every locked call, one or two lines. **Check this first.** It is the fast,
  authoritative index of current state.
- Vault decision/plan/spec docs (`/mnt/c/Users/sakib/Documents/ObsidianVault/hive/`):
  the full reasoning, evidence, and consequences behind a `D-ID`, pointed to by
  the ledger's `source` field.

### Step 1: grep the ledger, read the WHOLE matched line

Run from inside your repo checkout or worktree: `.wolf/decisions.md` is a
tracked file, so a worktree on a different branch can genuinely hold a
different version than the shared main checkout. Do not hardcode the main
checkout's path; resolve your own root first:

```bash
cd "$(git rev-parse --show-toplevel)"
grep -n -i "<topic>" .wolf/decisions.md
```

`.wolf/decisions.md` supersedes decisions **in place**: the old entry is kept for
history but prefixed with its new status, never deleted. A grep hit is not proof of
a current rule; it is proof a decision with that keyword exists somewhere in the
file's history. Read the full line for one of these prefixes immediately after the
`D-ID |`, then classify which of the two shapes below it is; they need different
handling:

`REVOKED` · `RETIRED` · `SUPERSEDED` · `AMENDED` · `MOOT` · `RESOLVED`

**Shape A: the prefix points to a DIFFERENT D-ID ("by D-0xx").** The matched
entry is dead. Follow the pointer and grep that ID too, repeat until you land
on an entry with no revocation prefix, or one whose prefix points back into
itself (Shape B). Report the ORIGINAL id's dead status, the CURRENT id, and the
current id's content. Never report the original entry's text as if it were
still the rule.

- D-013 (no-fork Open WebUI) → `REVOKED 2026-08-11 by D-036`: dead, go read D-036.
- D-028 (LibreChat replaces Open WebUI) → `RETIRED 2026-08-16 by D-040`: dead, go read D-040.
- D-029 → `MOOT since 2026-08-11` (its subject, D-013, was revoked): dead, no content of its own to fall back to.
- D-035 (BD no-FX-display rule) → `REVOKED` 2026-08-08: dead outright, no replacement decision exists.

**Shape B: the prefix names WHO amended it (an owner ruling) or scopes itself
to only PART of the entry, with no separate ID whose content replaces it.**
The entry is still the current rule: read the whole line, the live rule and
the dead design it replaced are both described inline, don't stop at the
prefix word.

- D-032 (per-route pricing) → `AMENDED 2026-08-01 by owner ruling: one model,
  one price...`: the text after "AMENDED... by owner ruling:" IS the current
  rule (alias-level pricing); the "SUPERSEDED design" named later in the same
  line is the dead one. There is no D-033 to chase.
- D-037 (coding pipeline) → `SUPERSEDED in part by D-038, which replaced the
  fixed two-stream shape with a dynamic selector; the rest of the pipeline
  stands`: only the two-stream-shape clause is dead (go read D-038 for its
  replacement); every other clause in D-037 is still current.
- D-043 (web-console prod build) → `SUPERSEDED, not by this entry's own action
  but by prior merged work`: the entry records a fact that was already true
  before this ledger line existed; read it as a status report, not a live
  pointer to chase.

If you're not sure which shape a match is, read the clause immediately after
the status word: `by D-<number>` with nothing else of substance following
means Shape A (go there); a colon followed by a full sentence describing the
rule means Shape B (you're already at the current text).

### Step 2: search past the first hit, a later entry can narrow one without revoking it

Some entries build on an earlier one without marking it revoked, and say so
explicitly (`Does NOT revoke D-036 or D-040`, D-044 and D-045 both do this while
changing what those decisions mean in practice). Grep the topic across the whole
file, not just to the first match, and read any later entry that names the same
D-ID even where the word REVOKED never appears; the scope of a live decision can
still have moved.

### Step 3: for full reasoning, follow `source` into the vault

The ledger's `source` field usually names a vault file directly (`vault
decision-<date>-<slug>.md`) or a plan/spec doc. Use `vault-search` mechanics to
grep that file rather than reading it whole. Also check the vault doc's own
frontmatter `status` field: it sometimes records supersession independently of the
ledger (e.g. a spec whose `status:` reads `partially superseded 2026-08-23 by
owner rulings... see correction block below`).

### Step 4: answer format

State all four, every time:

1. The original decision and its D-ID.
2. Current status: current / revoked / retired / superseded / amended / moot.
3. The revoking or amending decision, if any, by D-ID.
4. The date of the LATEST state, not the original decision's date.

A lookup that stops at step 1 and returns "yes, we decided X" without checking for
a later prefix is worse than no lookup: it re-injects a dead rule as if it still
binds, and it did (the no-fork rule and the BD FX-display rule were both cited as
current after being revoked, before this ledger convention made revocation
greppable in place).
