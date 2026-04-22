---
name: Memory Tools (Second Brain)
description: Optional cross-session memory and context tools. Use if installed; ignore if absent. No mandates.
---

## Memory Tools (Second Brain)

Project work well with optional memory/context tools. **None required.** Detect what available, use it. Tool absent? Skip, continue. No penalty.

### Tools (all optional)

| Tool | What it gives you | How to detect |
|------|-------------------|---------------|
| **claude-mem** | Cross-session observation store with search (`mem-search`, `get_observations`, `timeline`, `smart_search`, `smart_outline`, `smart_unfold`) | `mcp__plugin_claude-mem_mcp-search__*` tools available, or `claude-mem` skill listed |
| **OpenWolf** | Project-local second brain — `.wolf/anatomy.md` (file index), `.wolf/cerebrum.md` (do-not-repeat + learnings), `.wolf/memory.md` (session log), `.wolf/buglog.json` (bug registry) | `.wolf/` directory exists in project root |
| **Claude Code auto-memory** | Per-project memory directory for user/feedback/project/reference notes | `~/.claude/projects/<slug>/memory/MEMORY.md` exists |
| **context-mode** | Sandboxed execution + FTS5 indexing (`ctx_batch_execute`, `ctx_search`, `ctx_execute_file`) keep raw output out of context window | `mcp__plugin_context-mode_context-mode__*` tools available |

### Usage guidance

- **Before starting work:** claude-mem available? Run `mem-search` for prior work same area. OpenWolf available? Check `.wolf/anatomy.md` before Reading project files and `.wolf/cerebrum.md` Do-Not-Repeat before generating code.
- **After user correction:** update active memory store — OpenWolf `cerebrum.md`, or Claude Code auto-memory (`feedback_*.md`), or both. Never persist corrections to multiple stores inconsistently.
- **After fixing bug:** `.wolf/buglog.json` exists? Append bug entry per OpenWolf schema. Else skip.
- **Large-output shell or web fetch:** prefer `ctx_batch_execute` / `ctx_fetch_and_index` over raw Bash/WebFetch when context-mode present.
- **Session wrap:** OpenWolf active? Append line to `.wolf/memory.md`. claude-mem active? Captures automatic — no manual action.

### Non-goals

- Do NOT install tools for user.
- Do NOT create `.wolf/` files if directory not already exist.
- Do NOT refuse task because memory tool missing. Behave like tool not there.
- Do NOT duplicate same memory across stores — pick one user already use.