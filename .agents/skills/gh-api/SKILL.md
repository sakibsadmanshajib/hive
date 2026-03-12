---
name: gh-api
description: Use when working with GitHub pull requests or review comments and you need to choose the right repository-specific GitHub CLI skill
---

# GitHub CLI Skill Router

## Overview

This skill is the entry point for GitHub CLI work in this repository. Use it to choose the narrow GitHub skill that matches the task, then load that skill for the detailed commands and pitfalls.

## Shared Assumptions

- `gh` CLI is installed and authenticated with `gh auth status`
- Commands run from the repo root so `repos/{owner}/{repo}` auto-resolves from the current git remote
- Prefer `gh api` when you need exact REST endpoints or repository-specific fallbacks

## Choose The Right Skill

### Use `gh-reading-reviews`

Use when you need to inspect pull request review state:

- fetch inline review comments
- fetch top-level reviews
- fetch issue or PR conversation comments
- summarize comments by file, line, or severity
- handle pagination and `--jq` filtering

### Use `gh-responding-to-reviews`

Use when you need to reply inside existing review threads:

- reply to inline code review comments
- distinguish review-thread replies from top-level PR comments
- avoid bad reply endpoints that return `404 Not Found`

### Use `gh-editing-prs`

Use when you need to inspect or mutate pull request metadata:

- fetch structured PR metadata
- update PR descriptions or titles
- handle `gh pr edit` failures caused by deprecated classic Projects GraphQL access
- use the REST patch fallback for PR body updates

## Quick Routing Examples

- "Read all review comments on PR 42" -> `gh-reading-reviews`
- "Reply to this inline review thread" -> `gh-responding-to-reviews`
- "Update the PR description from a markdown file" -> `gh-editing-prs`

## Common Pitfall

- Do not stop at this router skill for implementation details. Load the matching child skill before running GitHub commands.
