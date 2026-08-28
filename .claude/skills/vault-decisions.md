---
name: Vault Decisions
description: Use when asked what was decided about something, whether a rule or decision still holds, or before designing/implementing anything that could be governed by a prior owner ruling. Covers finding a decision AND checking whether it has since been revoked, retired, superseded, amended, or mooted, since returning a dead decision as current has repeatedly caused real defects.
---

## Vault Decisions

Two layers hold decisions, and they answer different questions:

- `.wolf/decisions.md` (repo root) — terse `D-ID | decision | source | date` ledger,
  every locked call, one or two lines. **Check this first.** It is the fast,
  authoritative index of current state.
- Vault decision/plan/spec docs (`/mnt/c/Users/sakib/Documents/ObsidianVault/hive/`)
  — the full reasoning, evidence, and consequences behind a `D-ID`, pointed to by
  the ledger's `source` field.

### Step 1: grep the ledger, read the WHOLE matched line

```bash
grep -n -i "<topic>" /home/sakib/hive/.wolf/decisions.md
```

`.wolf/decisions.md` supersedes decisions **in place**: the old entry is kept for
history but prefixed with its new status, never deleted. A grep hit is not proof of
a current rule — it is proof a decision with that keyword exists somewhere in the
file's history. Read the full line for one of these prefixes immediately after the
`D-ID |`:

`REVOKED` · `RETIRED` · `SUPERSEDED` · `AMENDED` · `MOOT` · `RESOLVED`

Real chains in this ledger, so the pattern is not hypothetical:

- D-013 (no-fork Open WebUI) → `REVOKED 2026-08-11 by D-036`
- D-028 (LibreChat replaces Open WebUI) → `RETIRED 2026-08-16 by D-040`
- D-029 → `MOOT since 2026-08-11` (its subject, D-013, was revoked)
- D-032 (per-route pricing) → original design `SUPERSEDED`, entry itself `AMENDED
  2026-08-01`
- D-035 (BD no-FX-display rule) → `REVOKED` 2026-08-08
- D-037 → `SUPERSEDED in part by D-038`
- D-043 → `SUPERSEDED`, not by this entry's own action but by prior merged work

If the matched entry carries any of those prefixes, it is dead. Follow its "by
D-0xx" pointer and grep that ID too — repeat until you land on an entry with no
revocation prefix. Report the ORIGINAL id's dead status, the CURRENT id, and the
current id's content. Never report the original entry's text as if it were still
the rule.

### Step 2: search past the first hit — a later entry can narrow one without revoking it

Some entries build on an earlier one without marking it revoked, and say so
explicitly (`Does NOT revoke D-036 or D-040`, D-044 and D-045 both do this while
changing what those decisions mean in practice). Grep the topic across the whole
file, not just to the first match, and read any later entry that names the same
D-ID even where the word REVOKED never appears — the scope of a live decision can
still have moved.

### Step 3: for full reasoning, follow `source` into the vault

The ledger's `source` field usually names a vault file directly (`vault
decision-<date>-<slug>.md`) or a plan/spec doc. Use `vault-search` mechanics to
grep that file rather than reading it whole. Also check the vault doc's own
frontmatter `status` field — it sometimes records supersession independently of the
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
