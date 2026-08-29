---
name: Memory Tools (Second Brain)
description: Optional cross-session memory and context tools. Use if installed; ignore if absent. No mandates.
---

## Memory Tools (Second Brain)

Projects work well with optional memory/context tools. **None required.** Detect what available, use it. Tool absent? Skip, continue. No penalty.

### Tools (all optional)

| Tool | What gives you | How detect |
|------|-------------------|---------------|
| **claude-mem archive** (RETIRED 2026-06-12, read-only) | Historical cross-session observations only. No live tool, no MCP server, no capture. Survives solely as a sqlite file at `~/.claude-mem/claude-mem.db`, queryable with `python3 -c "import sqlite3; ..."` when old context is genuinely needed | `~/.claude-mem/claude-mem.db` exists on disk. Do NOT look for `mcp__plugin_claude-mem_mcp-search__*` tools or a `mem-search` command; they are gone |
| **OpenWolf** | Project-local second brain — `.wolf/anatomy.md` (file index), `.wolf/cerebrum.md` (do-not-repeat + learnings), `.wolf/memory.md` (session log), `.wolf/buglog.json` (bug registry) | `.wolf/` dir exists in project root |
| **Claude Code auto-memory** | Per-project memory dir for user/feedback/project/reference notes | `~/.claude/projects/<slug>/memory/MEMORY.md` exists |
| **context-mode** | Sandboxed exec + FTS5 indexing (`ctx_batch_execute`, `ctx_search`, `ctx_execute_file`) keep raw output out of context window | `mcp__plugin_context-mode_context-mode__*` tools available |

### Usage guidance

- **Before start work:** OpenWolf available? Check `.wolf/anatomy.md` before Reading project files and `.wolf/cerebrum.md` Do-Not-Repeat before generating code. Native auto-memory loads `MEMORY.md` for you already. Reach for the retired claude-mem sqlite archive only when the answer is plainly older than 2026-06-12 and nothing above carries it.
- **After user correction:** update active memory store — OpenWolf `cerebrum.md`, or Claude Code auto-memory (`feedback_*.md`), or both. Never persist corrections to multiple stores inconsistently.
- **After fix bug:** `.wolf/` exists? Recording an entry is mandatory, but on a feature branch it goes in the PR body, not into `.wolf/buglog.jsonl`. It is appended to `main` afterwards in a separate buglog-only PR. Protocol: `.claude/rules/openwolf.md`. Else skip.
- **Large-output shell or web fetch:** prefer `ctx_batch_execute` / `ctx_fetch_and_index` over raw Bash/WebFetch when context-mode present.
- **Session wrap:** OpenWolf active? Append line to `.wolf/memory.md` (hook-owned; nothing to do by hand). Durable learnings go to native auto-memory, not to the retired claude-mem archive, which no longer captures anything.

### Non-goals

- Do NOT install tools for user.
- Do NOT create `.wolf/` files if dir not already exist.
- Do NOT refuse task because memory tool missing. Behave like tool not there.
- Do NOT duplicate same memory across stores — pick one user already use.