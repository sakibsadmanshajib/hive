---
name: Explore Codebase
description: Navigate and understand codebase structure using the knowledge graph
---

## Explore Codebase

Use code-review-graph MCP tools explore + understand codebase.

### Steps

1. Run `list_graph_stats` see overall codebase metrics.
2. Run `get_architecture_overview` for high-level community structure.
3. Use `list_communities` find major modules, then `get_community` for details.
4. Use `semantic_search_nodes` find specific functions/classes.
5. Use `query_graph` with patterns like `callers_of`, `callees_of`, `imports_of` trace relationships.
6. Use `list_flows` + `get_flow` understand execution paths.

### Tips

- Start broad (stats, architecture), narrow to specific areas.
- Use `children_of` on file see all functions + classes.
- Use `find_large_functions` spot complex code.

## Token Efficiency Rules
- ALWAYS start with `get_minimal_context(task="<your task>")` before any other graph tool.
- Use `detail_level="minimal"` on all calls. Escalate to "standard" only when minimal insufficient.
- Target: finish any review/debug/refactor task in ≤5 tool calls, ≤800 total output tokens.