---
name: Memory Layers (Six-Layer Model)
description: Map a memory question to the right layer/store, retrieve in the right order, verify staleness before trusting a hit, and consolidate (promote/retire) between layers. Companion to memory-tools.md, which only covers tool detection.
---

## Memory Layers

Six layers: CoALA (Sumers et al. 2024, arXiv:2309.02427) working/episodic/
semantic/procedural, plus entity/profile (Atlan, LangGraph entity memory)
and reflection/consolidation (Park et al. 2023, arXiv:2304.03442). Full
sourcing + gap analysis: vault `hive/architecture-2026-08-28-six-layer-agent-memory.md`.

### The six layers

| # | Layer | Store(s) in this repo | Lifespan |
|---|-------|------------------------|----------|
| 1 | Working | `.wolf/anatomy.md`, `.wolf/memory.md` (hook-owned, gitignored) | session/checkout, evicted |
| 2 | Episodic | `.wolf/buglog.jsonl` (tracked, append-only); claude-mem sqlite (historical, read-only) | permanent |
| 3 | Semantic | `.wolf/decisions.md` (terse index, authoritative), MEMORY.md `project_*`/`reference_*`, vault specs | permanent, edit in place |
| 4 | Procedural | `.wolf/cerebrum.md` Do-Not-Repeat, MEMORY.md `feedback_*`, `.claude/skills/*` | permanent, edit in place |
| 5 | Entity/Profile | MEMORY.md `user_*.md`, `project_*.md` | permanent, edit in place |
| 6 | Reflection/Consolidation | no store — this skill | promotion + retirement process |

### Retrieval order

Cheapest, most-likely-current first. Stop at the first hit that answers it.

1. Working — `.wolf/anatomy.md` — already known this session?
2. Episodic — `openwolf bug search <term>`, or grep `.wolf/buglog.jsonl` — happened before?
3. Procedural — `.wolf/cerebrum.md` Do-Not-Repeat — standing rule?
4. Semantic — grep `.wolf/decisions.md`, then MEMORY.md — decision or fact? `decisions.md` is authoritative; MEMORY.md/cerebrum.md are mirrors that can lag it.
5. Entity — MEMORY.md `user_*`/`project_*` — anything specific to this person/project?
6. Vault — only when a terse layer points at a doc and full detail is needed.

### Staleness check (mandatory before trusting any hit above working)

- grep the hit's own text for `REVOKED`/`SUPERSEDED`/`STALE` first.
- Semantic/procedural hits: cross-check `grep -n "<fact>" .wolf/decisions.md` — a later `D-0xx` can revoke an earlier fact without the mirror file being touched.
- Entity hits older than ~60 days on anything fast-moving (pricing, org structure, active project state): re-verify against a live source, don't just cite the file.
- Episodic entries never get marked stale — they're append-only history — but the root cause they describe can outlive the code it was about. Re-check against the live code path before replaying an old fix.
- Working pattern already in this repo, not a bug to fix: MEMORY.md's `feedback_bdt_no_fx_display.md` opens with "REVOKED 2026-08-08" in both its frontmatter `description` and its first sentence, points at `.wolf/decisions.md` D-035, and keeps the wrong rule below so a future grep for the old term finds the correction, not a false positive.

### Consolidation: promote a session observation to a durable layer

`.wolf/anatomy.md`/`.wolf/memory.md` are hook-owned and disappear at session
end. Nothing promotes them automatically — promote by hand:

| Observation | Promotes to | Mechanism |
|---|---|---|
| Bug root-caused and fixed | Episodic | `.wolf/buglog.jsonl` — PR body "Buglog entry" heading first, separate buglog-only PR after merge. Never append on a feature branch (server-side merge conflicts server-side; `.claude/rules/openwolf.md`) |
| Standing behavioral rule learned | Procedural | `.wolf/cerebrum.md` Do-Not-Repeat, or MEMORY.md `feedback_*.md` |
| Durable fact/decision | Semantic | `.wolf/decisions.md` if it's a decision; MEMORY.md `project_*`/`reference_*` otherwise |
| Fact about one named person/project | Entity | MEMORY.md `user_*.md`/`project_*.md` |
| Finished plan/spec/ADR | Semantic, long form | Obsidian vault (`obsidian-second-brain` skill) |

Never write the same fact to two durable stores — pick the one that owns
that type.

### Retirement: a memory turns out wrong

Never delete. Edit in place:

1. Prefix `REVOKED`/`SUPERSEDED <date>` on both the frontmatter/index line and the opening sentence of the body.
2. Point at what supersedes it (decision id, PR, or new fact).
3. Leave the original wrong text below — a future grep for the old term should find the correction, not silence.
4. `.wolf/decisions.md` specifically is append-only: add a new `D-0xx` row rather than editing the old one; the OLD row gets the `REVOKED` prefix pointing at the new row's id (pattern: D-013, D-029, D-035, D-036).

### Boundary

Native auto-memory files (`~/.claude/projects/-home-sakib-hive/memory/`) are
outside this repo. A repo-worktree builder agent never edits `~/.claude/` —
retiring a stale MEMORY.md entry is a main-session/user action; a dispatched
worktree agent reports the staleness, it does not fix it.

Companion: `memory-tools.md` (which store/tool exists, at all). This skill
is what to do once you know it exists.
