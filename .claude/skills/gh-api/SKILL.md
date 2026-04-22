---
name: gh-api
description: Use when working with GitHub pull requests or review comments and you need to choose the right repository-specific GitHub CLI skill
---

# GitHub CLI Skill Router

## Overview

Entry point for GitHub CLI work this repo. Pick narrow GitHub skill match task, load for commands + pitfalls.

## Shared Assumptions

- `gh` CLI installed + authenticated via `gh auth status`
- Run from repo root so `repos/{owner}/{repo}` auto-resolves from git remote
- Prefer `gh api` for exact REST endpoints or repo-specific fallbacks

## Choose The Right Skill

### Use `gh-reading-reviews`

Inspect PR review state:

- fetch inline review comments
- fetch top-level reviews
- fetch issue/PR conversation comments
- summarize comments by file, line, severity
- handle pagination + `--jq` filtering

### Use `gh-responding-to-reviews`

Reply inside existing review threads:

- reply to inline code review comments
- distinguish review-thread replies from top-level PR comments
- avoid bad reply endpoints returning `404 Not Found`

## Use `gh-editing-prs`

Inspect/mutate PR metadata:

- fetch structured PR metadata
- update PR descriptions or titles
- handle `gh pr edit` failures from deprecated classic Projects GraphQL access
- use REST patch fallback for PR body updates

## Quick Routing Examples

- "Read all review comments on PR 42" -> `gh-reading-reviews`
- "Reply to this inline review thread" -> `gh-responding-to-reviews`
- "Update the PR description from a markdown file" -> `gh-editing-prs`

## Common Pitfall

- Don't stop at router skill for implementation details. Load matching child skill before running GitHub commands.