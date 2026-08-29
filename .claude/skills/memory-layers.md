---
name: Memory Layers (Four-Layer Model)
description: Map a memory question to the right layer/store, retrieve in the right order, verify a citation and check staleness before trusting a hit, and consolidate (promote/retire) between layers. Companion to memory-tools.md, which only covers tool detection.
---

## Memory Layers

**Four layers, from CoALA** (Sumers et al. 2024, [arXiv:2309.02427](https://arxiv.org/abs/2309.02427)):
working, episodic, semantic, procedural. That is the frame, and it is the only
one with a primary source behind it.

Two further rows below are **Hive extensions, not CoALA and not a standard**:
entity/profile memory and reflection/consolidation. They are kept because this
project needs both jobs done and nothing else does them, not because any paper
says a memory model has six layers. No primary source states a six-layer model;
a doc or brief that presents one as established is wrong. Full sourcing, the
secondary sources that use the words five and six, why they are not treated as
authoritative, and the gap analysis: vault
`hive/architecture-2026-08-28-six-layer-agent-memory.md` (filename kept for its
inbound links; read its research section, not its title, for the frame).

### The four layers

| # | Layer | Store/source | Location | Lifespan |
|---|-------|---------------|----------|----------|
| 1 | Working | `.wolf/anatomy.md`, `.wolf/memory.md` | repo, hook-owned, gitignored | session/checkout, evicted |
| 2 | Episodic | `.wolf/buglog.jsonl`; claude-mem sqlite | repo (tracked, append-only); external (`~/.claude-mem/`, read-only) | permanent |
| 3 | Semantic | `.wolf/decisions.md` (authoritative), MEMORY.md `project_*`/`reference_*`, vault specs | repo; native (`~/.claude/projects/.../memory/`); external (Obsidian vault) | permanent, edit in place |
| 4 | Procedural | `.wolf/cerebrum.md` Do-Not-Repeat, MEMORY.md `feedback_*`, `.claude/skills/*` | repo; native; repo | permanent, edit in place |

### Two Hive extensions (not CoALA, not a standard)

| # | Layer | Store/source | Location | Lifespan |
|---|-------|---------------|----------|----------|
| E1 | Entity/Profile | MEMORY.md `user_*.md` | native | permanent, edit in place |
| E2 | Reflection/Consolidation | no store (this skill) | n/a | promotion + retirement process |

`project_*.md` lives under Semantic (row 3), not Entity/Profile: in this
repo's actual MEMORY.md convention every `project_*.md` file is a durable
fact about a facet of the one Hive project (its state, an incident, a
build decision), not a profile of a separate named entity, so it belongs
with the other durable facts. Entity/Profile today has exactly one real
instance, `user_sakib.md`; the layer stays because the category is real
(a future second tenant/customer/named sub-project would land here), not
because this repo currently has many of them.

Why the two extensions exist beyond CoALA's four: **entity/profile** is needed
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

### Citation check (enforced by a hook, not by good intentions)

Semantic memory is only worth having if a citation to it is true. On
2026-07-30 an agent killed at its session limit left two retention migrations
behind whose 14 day window was justified by citing a `.wolf/decisions.md`
D-030 that did not exist then; a person grepping for it is what caught it
(native memory: `feedback_killed_agent_work_verification`). That id has since
been minted for an unrelated decision, which is exactly why a fabricated
citation is so hard to catch after the fact: grep the same string today and it
resolves. Writing "verify your citations" here again would have changed
nothing, so the rule is mechanical instead:

`.claude/hooks/decision-citation-check.js` runs as a PreToolUse hook on every
Write, Edit and MultiEdit. It **blocks** the write when the content cites a
`D-NNN` with no entry in `.wolf/decisions.md`, and names the highest id it can
see so the fabrication is obvious. It **warns**, without blocking, when the
cited entry opens REVOKED / SUPERSEDED / AMENDED / RETIRED / MOOT (citing one
as history is legitimate; citing one as a current rule is not) and when an
"owner ruling `<date>`" names a date that appears nowhere in the ledger. That
second one stays a warning deliberately: the ruling may be real and merely
unrecorded, which is a reason to record it, not to refuse a write.

What it does **not** cover, so nobody reads more into it than is there: a
dispatch brief that misstates the ledger (claiming it tops out ten entries
below where it does, say). A brief is an Agent prompt, not a file write, so no
PreToolUse hook on Write, Edit or MultiEdit ever sees one. That class is still
entirely on you.

It skips `.wolf/decisions.md` itself, where new ids are minted, and
`.claude/hooks/`, whose fixtures carry deliberate fake ids. Both the hook and
the CLI below apply the same skip list.

Writing **about** an id that does not exist is mandatory work here (the buglog
entry after every fixed bug, and the post-mortem or review analysing one), so
there is a deliberate, greppable bypass: put the literal
`citation-check: allow-unknown-ids` in the content and the audit is skipped.
It also covers a real entry that landed on main after this worktree was cut
and cannot be refreshed in place. It is not for getting past a block you have
not read. Every use is announced on stdout, so a bypass is visible in the
transcript rather than looking like a guard that never ran.

Know the reach of the marker before using it. On an Edit or MultiEdit only the
new strings are audited, so the marker exempts that change alone. A Write
carries the whole file, so a marker anywhere in it exempts the entire write,
and a file that keeps the marker stays exempt on every later full-file write.
This section is itself an example: it contains the literal, so writes to this
file are exempt. That is the price of a greppable marker rather than a bug,
and the per-write announcement is what keeps it visible.

Stale worktree ledgers are handled before it comes to that. Every builder
works in its own worktree carrying whatever `.wolf/decisions.md` its branch
point had, so the guard merges every ledger from the edited file up to the
filesystem root: a worktree under `<repo>/.claude/worktrees/` sees the
canonical checkout's fresher copy, and a correct citation of a recently minted
id is not read as a fabrication. When a block does land, refresh
`.wolf/decisions.md` from main and re-read the entry. Never substitute a lower
id that happens to exist: that turns a stale ledger into a real fabrication,
which is the failure this whole check exists to stop.

For text a Write never passes through, a PR body or an existing file, run the
same parser directly:

```
node .claude/hooks/decision-citation-check.js --check FILE...
gh pr view <n> --json body -q .body | node .claude/hooks/decision-citation-check.js --check
```

Self-check for the guard itself: `node .claude/hooks/hooks.selfcheck.js`.

What the hook cannot check, and stays your job: whether the entry you cited
says what you claimed it says. An id existing is not agreement. Read the
line before you lean on it.

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

Applies to the mutable layers: semantic and procedural (layers 3 and 4) plus
the entity/profile extension (E1). Never delete. Edit in place:

1. Prefix `REVOKED` or `SUPERSEDED <date>` on both the frontmatter/index line and the opening sentence of the body.
2. Point at what supersedes it (decision id, PR, or new fact).
3. Leave the original wrong text below so a future grep for the old term finds the correction, not silence.
4. `.wolf/decisions.md` specifically is append-only within this pattern: add a new `D-0xx` row rather than editing the old one; the old row gets a dead-status prefix pointing at the new row's id. `REVOKED` is the usual word (pattern: D-013, D-035, D-036), and the ledger also uses `RETIRED` (D-028) and `MOOT` (D-029) where those read truer. The citation hook treats all five words as dead, so any of them earns the warning.

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
