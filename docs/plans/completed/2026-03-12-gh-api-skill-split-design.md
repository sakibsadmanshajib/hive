# GH API Skill Split Design

## Goal

Split the current `gh-api` skill into smaller workflow-oriented skills so agents can load only the GitHub guidance they need for a given task.

## Design

### Why split by workflow

The current `gh-api` skill mixes three different jobs:

- reading PR review state
- replying inside review threads
- editing PR metadata and descriptions

Agents usually approach GitHub tasks by workflow, not by GitHub object model or CLI command family. A workflow-based split keeps discovery simple and reduces loading unrelated instructions.

### Proposed structure

Keep a small parent skill:

- `.agents/skills/gh-api/SKILL.md`

Its only responsibilities:

- explain that this is the entry point for GitHub CLI usage in this repo
- route the agent to the correct sub-skill
- document shared assumptions that apply across sub-skills, such as `{owner}/{repo}` auto-resolution from the current remote

Add three focused sub-skills:

- `.agents/skills/gh-reading-reviews/SKILL.md`
  - fetch inline review comments
  - fetch top-level reviews
  - fetch issue/PR conversation comments
  - pagination, jq filtering, and summary views

- `.agents/skills/gh-responding-to-reviews/SKILL.md`
  - reply to inline review comments
  - distinguish review-thread replies from top-level PR comments
  - document endpoint pitfalls such as the invalid `/pulls/comments/<COMMENT_ID>/replies` path

- `.agents/skills/gh-editing-prs/SKILL.md`
  - fetch structured PR metadata
  - update PR descriptions and titles
  - document the `gh pr edit` failure mode tied to deprecated classic Projects GraphQL access
  - document the REST `gh api repos/{owner}/{repo}/pulls/<PR_NUMBER> -X PATCH ...` fallback

### Discovery model

The parent `gh-api` skill should stay intentionally short and discovery-oriented:

- “Use `gh-reading-reviews` when you need to inspect review state or comments.”
- “Use `gh-responding-to-reviews` when you need to reply in review threads.”
- “Use `gh-editing-prs` when you need to inspect or mutate PR metadata.”

This preserves one stable skill name while allowing narrow dynamic loading.

### Migration approach

Do not remove the `gh-api` skill. Turn it into an index/router skill and move the detailed examples into the new sub-skills. That avoids breaking existing references in `AGENTS.md` and keeps backward compatibility for future sessions.

## Risks & mitigations

- Risk: duplicated guidance across sub-skills.
  Mitigation: keep shared assumptions only in the parent skill and keep each child scoped to one workflow.

- Risk: agents may still load only `gh-api` and stop there.
  Mitigation: make the parent skill explicit that it is a router and include direct “use X when Y” guidance near the top.

- Risk: naming drift makes discovery worse.
  Mitigation: use verb-first, workflow-oriented names that match likely user requests and agent search terms.

## Verification

- Confirm the parent skill is shorter and routes clearly to the three sub-skills.
- Confirm each sub-skill contains only workflow-specific instructions.
- Confirm the PR edit fallback lesson exists only in `gh-editing-prs`.
