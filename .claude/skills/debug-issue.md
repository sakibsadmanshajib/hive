---
name: Debug Issue
description: Systematically debug issues using graph-powered code navigation
---

## Debug Issue

Knowledge graph trace + debug issues.

### Steps

1. `semantic_search_nodes` — find code related to issue.
2. `query_graph` w/ `callers_of` + `callees_of` — trace call chains.
3. `get_flow` — full execution paths thru suspect areas.
4. `detect_changes` — check if recent changes caused issue.
5. `get_impact_radius` on suspect files — see what else affected.

### Tips

- Check callers + callees — full context.
- Affected flows → find entry point triggering bug.
- Recent changes = most common source of new issues.

## Token Efficiency Rules
- ALWAYS start with `get_minimal_context(task="<your task>")` before any other graph tool.
- Use `detail_level="minimal"` on all calls. Escalate to "standard" only when minimal insufficient.
- Target: any review/debug/refactor task in ≤5 tool calls + ≤800 output tokens.