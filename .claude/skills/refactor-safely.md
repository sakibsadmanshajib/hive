---
name: Refactor Safely
description: Plan and execute safe refactoring using dependency analysis
---

## Refactor Safely

Use knowledge graph plan + execute refactor confident.

### Steps

1. `refactor_tool` mode="suggest" → community-driven suggestions.
2. `refactor_tool` mode="dead_code" → find unreferenced code.
3. Renames: `refactor_tool` mode="rename" → preview affected locations.
4. `apply_refactor_tool` w/ refactor_id → apply renames.
5. After changes: `detect_changes` → verify impact.

### Safety Checks

- Always preview before apply (rename mode = edit list).
- Check `get_impact_radius` before major refactor.
- `get_affected_flows` → no critical paths broken.
- `find_large_functions` → spot decomposition targets.

## Token Efficiency Rules
- ALWAYS start `get_minimal_context(task="<your task>")` before other graph tool.
- Use `detail_level="minimal"` on all calls. Escalate "standard" only when minimal insufficient.
- Target: any review/debug/refactor ≤5 tool calls + ≤800 output tokens.