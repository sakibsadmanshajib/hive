---
name: Review Changes
description: Perform a structured code review using change detection and impact
---

## Review Changes

Thorough, risk-aware code review via knowledge graph.

### Steps

1. Run `detect_changes` → risk-scored change analysis.
2. Run `get_affected_flows` → find impacted execution paths.
3. Each high-risk function: run `query_graph` with pattern="tests_for" → check test coverage.
4. Run `get_impact_radius` → understand blast radius.
5. Untested changes: suggest specific test cases.

### Output Format

Group findings by risk level (high/medium/low) with:
- What changed + why matter
- Test coverage status
- Suggested improvements
- Overall merge recommendation

## Token Efficiency Rules
- ALWAYS start with `get_minimal_context(task="<your task>")` before any other graph tool.
- Use `detail_level="minimal"` all calls. Escalate to "standard" only when minimal insufficient.
- Target: any review/debug/refactor task ≤5 tool calls, ≤800 total output tokens.