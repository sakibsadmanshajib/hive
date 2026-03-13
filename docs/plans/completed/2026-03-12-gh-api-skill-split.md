# GH API Skill Split Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Split the `gh-api` skill into a small routing skill plus focused workflow-specific sub-skills for reading reviews, replying to reviews, and editing pull requests.

**Architecture:** Keep `gh-api` as the stable entry point, but reduce it to shared assumptions and routing guidance. Move detailed command examples and pitfalls into three new workflow-oriented skills so agents can dynamically load only the relevant GitHub instructions.

**Tech Stack:** Markdown skill documents under `.agents/skills/`, repo policy in `AGENTS.md`, shell verification with `sed` and `rg`

---

## Goal
Split the oversized `gh-api` skill into smaller workflow-based skills without breaking the existing top-level entry point.

## Assumptions
- Existing references to `gh-api` in [AGENTS.md](/home/sakib/hive/AGENTS.md) should remain valid.
- New skills should live under `.agents/skills/` with one `SKILL.md` per directory.
- The split is documentation-only; no product code changes are required.

## Plan

### Step 1: Rewrite the parent `gh-api` skill as a router
Files: `.agents/skills/gh-api/SKILL.md`
Change: Replace most command detail with a short entry-point skill that documents shared prerequisites and routes agents to `gh-reading-reviews`, `gh-responding-to-reviews`, or `gh-editing-prs`.
Verify: `sed -n '1,220p' .agents/skills/gh-api/SKILL.md`

### Step 2: Add the review-reading sub-skill
Files: `.agents/skills/gh-reading-reviews/SKILL.md`
Change: Create a focused skill for fetching inline review comments, review summaries, issue comments, pagination, jq filtering, and summary views.
Verify: `sed -n '1,240p' .agents/skills/gh-reading-reviews/SKILL.md`

### Step 3: Add the review-response sub-skill
Files: `.agents/skills/gh-responding-to-reviews/SKILL.md`
Change: Create a focused skill for replying to inline review comments, choosing the correct endpoint, and avoiding reply-path mistakes.
Verify: `sed -n '1,220p' .agents/skills/gh-responding-to-reviews/SKILL.md`

### Step 4: Add the PR-editing sub-skill
Files: `.agents/skills/gh-editing-prs/SKILL.md`
Change: Create a focused skill for reading PR metadata, updating descriptions/titles, and handling the `gh pr edit` classic Projects GraphQL failure by using the REST patch fallback.
Verify: `sed -n '1,240p' .agents/skills/gh-editing-prs/SKILL.md`

### Step 5: Update skill discovery in `AGENTS.md`
Files: `AGENTS.md`
Change: Add the three new skill entries to the available skills list so agents can discover and load them directly in future sessions.
Verify: `rg -n "gh-reading-reviews|gh-responding-to-reviews|gh-editing-prs" AGENTS.md`

### Step 6: Verify the split and routing coherence
Files: `.agents/skills/gh-api/SKILL.md`, `.agents/skills/gh-reading-reviews/SKILL.md`, `.agents/skills/gh-responding-to-reviews/SKILL.md`, `.agents/skills/gh-editing-prs/SKILL.md`, `AGENTS.md`
Change: Review the final text to ensure the parent routes cleanly and the children stay narrowly scoped with no major duplication.
Verify: `rg -n "projectCards|pulls/<PR_NUMBER>/comments/<COMMENT_ID>/replies|Fetch PR Review Comments|Use when" .agents/skills/gh-api/SKILL.md .agents/skills/gh-reading-reviews/SKILL.md .agents/skills/gh-responding-to-reviews/SKILL.md .agents/skills/gh-editing-prs/SKILL.md AGENTS.md`

## Risks & mitigations
- Risk: the parent skill becomes too thin to be useful.
  Mitigation: keep shared assumptions plus direct routing examples in the parent.
- Risk: guidance is duplicated across sub-skills.
  Mitigation: assign one workflow per child and move shared repo assumptions back to the parent.
- Risk: agents miss the new skills because discovery text is incomplete.
  Mitigation: update `AGENTS.md` in the same change and use explicit, searchable names.

## Rollback plan
- Restore `.agents/skills/gh-api/SKILL.md` to the current monolithic version.
- Remove `.agents/skills/gh-reading-reviews/`, `.agents/skills/gh-responding-to-reviews/`, and `.agents/skills/gh-editing-prs/`.
- Remove the added skill references from `AGENTS.md`.
