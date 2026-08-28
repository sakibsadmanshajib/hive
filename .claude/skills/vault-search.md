---
name: Vault Search
description: Use when starting a plan, spec, or investigation, or when asked what prior work, decisions, or research already exists on a topic, before re-deriving anything the Obsidian vault already answers. Covers finding and quoting the right passage in a large vault doc without dumping the whole file.
---

## Vault Search

Vault root: `/mnt/c/Users/sakib/Documents/ObsidianVault/hive/` (WSL mount, read-only
for this skill). One flat folder, kebab-case files, frontmatter `type: plan|spec|
decision|discussion|architecture`, `date`, often `status`/`tags`/`source`. Files run
large: several plans and specs are 50-100KB. Never `cat` a whole file into context;
grep for headings and lines, then read only the matched range.

### Step 1: search the indexes first, not the raw files

Two files exist precisely to answer "what do we have on X" without opening source
docs:

- `MOC-plans.md` — every plan/decision doc, one dense paragraph each, dated, newest
  first, in two tables (Decisions, Plans).
- `MOC-timeline-current.md` — the same universe in chronological order.
- `README.md` — top-level "Start here" pointers plus an architecture/topic index.

```bash
grep -n -i "<topic>" \
  "/mnt/c/Users/sakib/Documents/ObsidianVault/hive/MOC-plans.md" \
  "/mnt/c/Users/sakib/Documents/ObsidianVault/hive/MOC-timeline-current.md" \
  "/mnt/c/Users/sakib/Documents/ObsidianVault/hive/README.md"
```

The MOC paragraph is often the whole answer: it already names the file, the date,
the trigger, and the concrete outcome. Read it before deciding you need the source
document at all.

### Step 2: go to the source doc only for a quote or a detail the MOC lacks

```bash
V="/mnt/c/Users/sakib/Documents/ObsidianVault/hive"
grep -n '^#\{1,3\} ' "$V/<file>.md"          # heading map with line numbers
grep -n -i "<topic>" "$V/<file>.md"          # candidate line numbers
sed -n '<start>,<end>p' "$V/<file>.md"       # read just that section
```

Pick the section from the heading map that brackets the matched line, and read only
that span. A 90KB spec does not need a full read for one paragraph.

### Step 3: a match is not automatically current — check for a later pass in the same file

This vault does not delete superseded content, it appends corrections in place.
Concretely observed patterns:

- `session-2026-08-25-claude-similarity-scorecard.md` scored every surface in the
  afternoon, then appended a whole second pass, `# Re-score, 2026-08-25 evening
  (live measurement)`, later in the *same file*, with different numbers for the same
  rows and a section titled "What actually shipped, and what only appears to have
  shipped."
- `spec-2026-08-16-hive-ui-redesign.md` carries an inline `> Correction, 2026-08-23`
  callout near the top marking specific passages below it superseded, without
  removing them.

Before quoting a match, grep the same file for a later dated heading or a
`Correction`/`Re-score`/`Revised`/`superseded` marker that postdates the passage:

```bash
grep -n '^#.*[0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}\|Correction\|Re-score\|Revised\|superseded' "$V/<file>.md"
```

If one exists after your match's line number, prefer it and say in your answer that
an earlier passage in the same file was superseded by a later section.

### Step 4: always hand back date and status with the quote

Every answer this skill produces must carry, alongside the excerpt: the file's
`date` and `status` frontmatter, and whether a later in-file pass superseded the
quoted section. Returning content with no timestamp attached is how stale claims
re-enter downstream work. If the topic looks decision-shaped (an owner ruling, a
locked call, something that could have been revoked), use the `vault-decisions`
skill instead of, or alongside, this one — `.wolf/decisions.md` is the faster and
more authoritative source for "is this still the rule"; this skill is for
everything else (plans, specs, research, session narratives).

### Non-goals

- Do not restructure, tidy, or bulk-edit the vault. It is the owner's real working
  record.
- Do not write anything here. Writing a new doc or updating the MOC is `vault-write`.
- Do not paste an entire matched file back into the conversation; excerpt only what
  answers the question.
