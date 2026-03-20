# Global Workflow CLAUDE.md Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Write `~/.claude/CLAUDE.md` that encodes the approved layered workflow — GSD (project management) + Superpowers (engineering discipline) + agent routing + supporting utilities.

**Architecture:** A single global config file read by Claude Code at session start. Organized as three layers: GSD outer loop, Superpowers inner loop (paired to each GSD stage), and on-demand utilities (Context7, context-mode, delegate-to-agent, gh). Agent routing replaces Claude Sonnet/Haiku subagents with GPT-5.4 / Gemini 3.1 Pro where available; Opus stays Claude.

**Tech Stack:** Markdown config file. No code. No tests. References: spec at `docs/superpowers/specs/2026-03-19-global-workflow-design.md`, delegate-to-agent skill at `~/.claude/skills/delegate-to-agent/`.

---

### Task 1: Write `~/.claude/CLAUDE.md`

**Files:**
- Modify: `/home/sakib/.claude/CLAUDE.md` (overwrite — current content is incorrect first draft)

- [ ] **Step 1: Write the file**

Content must follow the approved spec exactly. Sections in order:

**Section 1 — The Layered Model**
Brief intro paragraph explaining the three-layer model. Include the ASCII diagram:
```
┌─────────────────────────────────────────────┐
│  LAYER 1: GSD — What to work on             │
│  (project lifecycle, phases, planning)       │
│  ┌───────────────────────────────────────┐  │
│  │  LAYER 2: Superpowers — How to do it  │  │
│  │  (engineering discipline per task)    │  │
│  │  ┌─────────────────────────────────┐  │  │
│  │  │  LAYER 3: Utilities             │  │  │
│  │  │  Context7 · ctx · gh · delegate │  │  │
│  │  └─────────────────────────────────┘  │  │
│  └───────────────────────────────────────┘  │
└─────────────────────────────────────────────┘
```

**Section 2 — Layer 1: GSD**
- Start every session with `/gsd:progress`
- Command reference table (12 commands from spec)
- One-line rule: "GSD owns the stage. Never skip `/gsd:progress` at session start."

**Section 3 — Layer 2: Superpowers**
- Intro: complements GSD, does not replace its built-in functionality
- GSD Stage → Superpowers Skill → What it adds table (8 rows from spec)
- One-line rule: "Both layers are always active. Superpowers adds rigor on top of GSD's structure."

**Section 4 — Agent Routing**
- Intro: Opus stays Claude. Sonnet/Haiku routes to best external model for token pool efficiency.
- Routing table (5 rows from spec)
- Availability check block:
```bash
which gemini 2>/dev/null && echo "gemini: available" || echo "gemini: not installed"
which codex 2>/dev/null && echo "codex: available" || echo "codex: not installed"
```
- Fallback chain: GPT-5.4 → Gemini 3.1 Pro → Claude Sonnet
- Note: model IDs confirmed against agent-cli-reference.md at `~/.claude/skills/delegate-to-agent/agent-cli-reference.md`
- Note: if no external agent installed, GSD defaults apply without disruption

**Section 5 — Layer 3: Utilities**

*Context7:*
- Always use before recalling any SDK/API/library signature
- `resolve-library-id(name)` → `query-docs(id, query)`
- Trigger: any import, API call, framework question

*context-mode:*
- Always on, enforced by hooks (pre-configured — no setup needed in this file)
- Bash only for: git, mkdir, rm, mv, short-output commands
- Large output → `ctx_batch_execute` or `ctx_execute(language: "shell")`
- File analysis → `ctx_execute_file` (not Read)
- Web → `ctx_fetch_and_index` then `ctx_search`

*gh CLI:*
- All GitHub ops: PRs, issues, reviews, checks, releases
- Triggered at `/gsd:ship`, `finishing-a-development-branch`

*delegate-to-agent:*
- Skill at `~/.claude/skills/delegate-to-agent/` — read it before invoking
- Used for GSD phase routing (see Section 4) and ad-hoc parallelization
- Always append JSON output schema to delegated prompts

- [ ] **Step 2: Verify the file was written correctly**

Read back `~/.claude/CLAUDE.md` and confirm:
- All 5 sections present
- ASCII diagram renders correctly
- Routing table has 5 rows with correct model assignments (Opus → Claude, rest → external)
- delegate-to-agent skill path is explicit
- context-mode hooks noted as pre-configured

- [ ] **Step 3: Commit**

```bash
cd /home/sakib/hive
git add docs/superpowers/specs/2026-03-19-global-workflow-design.md
git add docs/superpowers/plans/2026-03-19-global-workflow-claude-md.md
git commit -m "docs: add global workflow design spec and implementation plan"
```

Note: `~/.claude/CLAUDE.md` is outside the repo — commit only the spec and plan.
