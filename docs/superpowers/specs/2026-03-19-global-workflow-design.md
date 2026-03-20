# Global Workflow Design

**Date:** 2026-03-19
**Scope:** Global (~/.claude/CLAUDE.md) — applies to all projects
**Status:** Approved by user

---

## Overview

A layered workflow where each tool has a single, non-overlapping responsibility.
No tool replaces another — each activates at a specific moment.

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

---

## Layer 1 — GSD (Project Management)

GSD owns the project lifecycle: phases, plans, state, artifacts, verification.
Every session starts with `/gsd:progress`.

| Command | When |
|---------|------|
| `/gsd:progress` | Session start — where am I, what's next |
| `/gsd:new-project` | New project initialization |
| `/gsd:new-milestone` | Starting a new milestone |
| `/gsd:discuss-phase` | Explore a phase before planning |
| `/gsd:plan-phase` | Create executable phase plan |
| `/gsd:execute-phase` | Execute all plans in a phase |
| `/gsd:verify-work` | UAT verification |
| `/gsd:ship` | Create PR from verified work |
| `/gsd:next` | Advance to next logical step |
| `/gsd:add-todo` / `/gsd:note` | Capture ideas mid-session |
| `/gsd:health` | Diagnose planning directory |

---

## Layer 2 — Superpowers (Engineering Discipline)

Superpowers complements GSD — it adds engineering rigor *within* GSD's process.
GSD drives the stage; Superpowers activates on top when the situation requires it.
Neither replaces the other's built-in functionality.

| GSD Stage | Superpowers Activates | What it adds |
|-----------|----------------------|--------------|
| `discuss-phase` | `brainstorming` | Structured approach comparison, surface hidden assumptions |
| `plan-phase` | `writing-plans` | Ensures plans are executable and testable, not just descriptive |
| `execute-phase` | `test-driven-development` | Write-test-first discipline within execution |
| Blocked/broken during execution | `systematic-debugging` | Scientific method before any fix attempt |
| Multiple independent tasks in a wave | `dispatching-parallel-agents` | Structured briefing of parallel agents |
| Before marking any task done | `verification-before-completion` | Evidence required before claiming complete |
| PR review feedback arrives | `receiving-code-review` | Rigor before implementing suggestions |
| `/gsd:ship` | `requesting-code-review` + `finishing-a-development-branch` | Review gate + clean branch close |

---

## Layer 2 — Agent Routing (within GSD phases)

GSD dispatches subagents during plan/execute/verify phases.
**Rule: Opus tasks stay Claude. Sonnet/Haiku tasks route to best-available external model first.**

The goal is token pool efficiency — using external agents expands which quota we draw from,
not to reduce quality. External models used are always best-in-class (no Flash-Lite, no mini).

| GSD Phase | Subagent Role | Model |
|-----------|--------------|-------|
| `plan-phase` | gsd-planner, gsd-researcher, gsd-roadmapper | **Claude Opus 4.6** (keep — judgment/architecture) |
| `execute-phase` | gsd-executor (implementation) | **GPT-5.4** → Gemini 3.1 Pro → Sonnet fallback |
| `execute-phase` | gsd-nyquist-auditor (test generation) | **GPT-5.4** → Gemini 3.1 Pro → Sonnet fallback |
| `verify-work` | gsd-verifier | **Gemini 3.1 Pro** → GPT-5.4 → Sonnet fallback |
| `execute-phase` | gsd-codebase-mapper | **Gemini 3.1 Pro** → GPT-5.4 → Sonnet fallback |

**Availability check (run before routing):**
```bash
which gemini 2>/dev/null && echo available || echo unavailable
which codex 2>/dev/null && echo available || echo unavailable
```

**Fallback chain:** GPT-5.4 → Gemini 3.1 Pro → Claude Sonnet
If no external agent is installed, GSD defaults apply without disruption.

---

## Layer 3 — Supporting Utilities

Always available, called on demand within any GSD stage or Superpowers skill.

### Context7 — Documentation lookup
Before recalling any SDK, API, or library signature from memory, use Context7.

```
resolve-library-id(libraryName) → query-docs(id, query)
```

Triggers: any `import`, any API call, any framework question, any "how does X work."
Never guess from training data — always look it up.

### context-mode — Context window protection
Always on, enforced by hooks. Key rules:
- Bash only for: `git`, `mkdir`, `rm`, `mv`, short-output commands
- Large output → `ctx_batch_execute` or `ctx_execute(language: "shell")`
- File analysis → `ctx_execute_file` (not Read)
- Web fetches → `ctx_fetch_and_index` then `ctx_search`
- WebFetch is blocked — always use `ctx_fetch_and_index`

### gh CLI — All GitHub operations
Use `gh` for everything: PRs, issues, reviews, checks, releases.
Triggered at `/gsd:ship`, `finishing-a-development-branch`, any PR/issue work.
Never construct GitHub URLs manually.

### delegate-to-agent — External model delegation
Used within GSD phases (see agent routing table above) and for ad-hoc parallelization.
Read the `delegate-to-agent` skill before invoking. Always append JSON output schema to delegated prompts.
