---
name: Memory Layers (Six-Layer Model)
description: Map a memory question to the right layer/store, retrieve in the right order, verify staleness before trusting a hit, and consolidate (promote/retire) between layers. Companion to memory-tools.md, which only covers tool detection.
---

## Memory Layers

Six layers, built on CoALA's four layers (Sumers et al. 2024, arXiv:2309.02427):
working, episodic, semantic, procedural, plus two practitioner extensions
this project specifically needs: entity/profile memory and
reflection/consolidation (grounded in Park et al. 2023, arXiv:2304.03442).
"Six" is not an established standard; no primary source states it. Full
sourcing, the one source that does use the word "six" and why it is not
treated as authoritative, and the gap analysis: vault
`hive/architecture-2026-08-28-six-layer-agent-memory.md`.

### The six layers

| # | Layer | Store/source | Location | Lifespan |
|---|-------|---------------|----------|----------|
| 1 | Working | `.wolf/anatomy.md`, `.wolf/memory.md` | repo, hook-owned, gitignored | session/checkout, evicted |
| 2 | Episodic | `.wolf/buglog.jsonl`; claude-mem sqlite | repo (tracked, append-only); external (`~/.claude-mem/`, read-only) | permanent |
| 3 | Semantic | `.wolf/decisions.md` (authoritative), MEMORY.md `project_*`/`reference_*`, vault specs | repo; native (`~/.claude/projects/.../memory/`); external (Obsidian vault) | permanent, edit in place |
| 4 | Procedural | `.wolf/cerebrum.md` Do-Not-Repeat, MEMORY.md `feedback_*`, `.claude/skills/*` | repo; native; repo | permanent, edit in place |
| 5 | Entity/Profile | MEMORY.md `user_*.md` | native | permanent, edit in place |
| 6 | Reflection/Consolidation | no store (this skill) | n/a | promotion + retirement process |

`project_*.md` lives under Semantic (row 3), not Entity/Profile: in this
repo's actual MEMORY.md convention every `project_*.md` file is a durable
fact about a facet of the one Hive project (its state, an incident, a
build decision), not a profile of a separate named entity, so it belongs
with the other durable facts. Entity/Profile today has exactly one real
instance, `user_sakib.md`; the layer stays because the category is real
(a future second tenant/customer/named sub-project would land here), not
because this repo currently has many of them.

Why layers 5 and 6 exist beyond CoALA's four: **entity/profile** is needed
because a fact about one specific named person (preferences, role) is a
different shape than a general project fact, and folding it into semantic
memory loses the "whose fact is this" anchor the moment there is more than
one entity to track. **Reflection/consolidation** is needed because nothing
else moved a session-scoped observation into a durable store, or marked a
durable fact wrong once it stopped being true; without it, working memory
either evaporates unused at session end or gets promoted inconsistently,
and a revoked fact keeps getting re-cited because nothing says how to
retire it. See the vault doc's Decision section for the full reasoning.

### Retrieval order

Cheapest, most-likely-current first. Stop at the first hit that answers it,
**except** a decision- or rule-shaped question (anything "is X still true,"
"are we allowed to Y"): those always get cross-checked against
`.wolf/decisions.md` before being answered, even off a working-memory hit,
because `.wolf/decisions.md` is authoritative over the whole session, not
just over layer 3, and a working note can predate a decision made earlier
in the same session or lag one made in another concurrent worktree. Treat
a working-memory hit on a decision/rule question as provisional until
that check runs.

1. Working: `.wolf/anatomy.md`. Already known this session? (Provisional on decision/rule questions, see above.)
2. Episodic: `openwolf bug search <term>` or grep `.wolf/buglog.jsonl` for a repo-tracked incident; `mem-search` (claude-mem, if installed) for older cross-session history the buglog doesn't carry. Happened before?
3. Procedural: `.wolf/cerebrum.md` Do-Not-Repeat. Standing rule?
4. Semantic: grep `.wolf/decisions.md`, then MEMORY.md. Decision or fact? `decisions.md` is authoritative; MEMORY.md/cerebrum.md are mirrors that can lag it.
5. Entity: MEMORY.md `user_*.md`. Anything specific to this person?
6. Vault: only when a terse layer points at a doc and full detail is needed.

### Staleness check (mandatory before trusting any hit above working)

- Grep the hit's own text for `REVOKED`, `SUPERSEDED`, or `STALE` first.
- Semantic/procedural hits: cross-check `grep -n "<fact>" .wolf/decisions.md`. A later `D-0xx` can revoke an earlier fact without the mirror file being touched.
- Entity hits (the person's role, current focus, standing preferences) older than roughly 60 days: re-verify against something the person said recently before treating it as still true, don't just cite the file.
- Episodic entries never get marked stale (they're append-only history), but the root cause they describe can outlive the code it was about. Re-check against the live code path before replaying an old fix.
- Working pattern already in this repo, not a bug to fix: MEMORY.md's `feedback_bdt_no_fx_display.md` opens with "REVOKED 2026-08-08" in both its frontmatter `description` and its first sentence, points at `.wolf/decisions.md` D-035, and keeps the wrong rule below so a future grep for the old term finds the correction, not a false positive.

### Consolidation: promote a session observation to a durable layer

`.wolf/anatomy.md` and `.wolf/memory.md` are hook-owned and disappear at
session end. Nothing promotes them automatically; promote by hand:

| Observation | Promotes to | Mechanism |
|---|---|---|
| Bug root-caused and fixed | Episodic | `.wolf/buglog.jsonl`, via PR body "Buglog entry" heading first, separate buglog-only PR after merge. Never append on a feature branch: GitHub's server-side merge conflicts serially across every open PR that appended one (`.claude/rules/openwolf.md`) |
| Standing behavioral rule learned | Procedural | `.wolf/cerebrum.md` Do-Not-Repeat, or MEMORY.md `feedback_*.md` |
| Durable fact/decision, including a fact about a project or subsystem | Semantic | `.wolf/decisions.md` if it's a decision, MEMORY.md `project_*`/`reference_*` otherwise |
| Fact about one named person | Entity | MEMORY.md `user_*.md` |
| Finished plan/spec/ADR | Semantic, long form | Obsidian vault (`obsidian-second-brain` skill) |

Never write the same fact to two durable stores; pick the one that owns
that type.

### Retirement: a memory turns out wrong

Applies to the mutable layers (semantic, procedural, entity/profile: layers
3, 4, 5). Never delete. Edit in place:

1. Prefix `REVOKED` or `SUPERSEDED <date>` on both the frontmatter/index line and the opening sentence of the body.
2. Point at what supersedes it (decision id, PR, or new fact).
3. Leave the original wrong text below so a future grep for the old term finds the correction, not silence.
4. `.wolf/decisions.md` specifically is append-only within this pattern: add a new `D-0xx` row rather than editing the old one; the old row gets the `REVOKED` prefix pointing at the new row's id (pattern: D-013, D-029, D-035, D-036).

Episodic (layer 2) does not get this treatment at all: `.wolf/buglog.jsonl`
is append-only history by contract (`.claude/rules/openwolf.md`: "never
rewrite an existing line"), and editing an old entry in place would corrupt
the record it exists to preserve. A wrong or superseded episodic entry gets
a new, later entry that references it and states the correction; the old
line stays exactly as written.

### Boundary

Native auto-memory files (`~/.claude/projects/-home-sakib-hive/memory/`) are
outside this repo. A repo-worktree builder agent never edits `~/.claude/`:
retiring a stale MEMORY.md entry is a main-session/user action, not
something a dispatched worktree agent does; it reports the staleness
instead.

Companion: `memory-tools.md` covers which store or tool exists, at all.
This skill covers what to do once you know it exists.
