---
name: gh-reading-reviews
description: Use when you need to inspect pull request review state, inline comments, or PR conversation comments with GitHub CLI
---

# Reading GitHub Reviews

## Overview

Skill cover read-only GitHub CLI patterns for PR reviews in this repo: inline comments, top-level review summaries, PR conversation comments, pagination, compact summaries.

## Prerequisites

- `gh` CLI installed + authenticated via `gh auth status`
- Run from repo root so `repos/{owner}/{repo}` auto-resolves

## Core Patterns

### Fetch PR Review Comments

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments \
  --jq '.[] | {id, path, line, body, user: .user.login}'
```

- Returns inline comments on file lines or hunks
- Use `--paginate` for full result set across pages

### Fetch PR Reviews

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/reviews \
  --jq '.[] | {id, state, body, user: .user.login}'
```

- `state` values: `APPROVED`, `CHANGES_REQUESTED`, `COMMENTED`, `DISMISSED`
- `body` = top-level review summary text

### Fetch Issue Or PR Conversation Comments

```bash
gh api repos/{owner}/{repo}/issues/<PR_NUMBER>/comments \
  --jq '.[] | {id, body: .body[0:500], user: .user.login}'
```

- PRs = issues, so endpoint returns general PR conversation comments
- Truncate large bodies for readable output

### Count Inline Comments

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments --jq 'length'
```

### Filter Comments By File

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments \
  --jq '.[] | select(.path == "path/to/file.ts") | .body'
```

### Build A Summary View

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments \
  --jq '.[] | "FILE: \(.path) | LINE: \(.line) | SEVERITY: \(.body | split("\n")[0])"'
```

## What Works Well

1. `--jq` fastest way extract only fields you need from noisy review payloads.
2. `.body[0:N]` truncation prevents terminal overflow from large bot comments.
3. `--paginate` matters — GitHub REST endpoints default 30 items per page.

## Common Pitfalls

1. `line` can be `null` for file-level comments not attached to specific line.
2. Review comments vs issue comments = different endpoints:
   - `/pulls/{pr}/comments` = inline code review comments
   - `/issues/{pr}/comments` = general PR conversation comments
   - `/pulls/{pr}/reviews` = top-level review summaries
3. Bot comments can be huge. Filter or truncate aggressively before output.
4. `diff_hunk` useful for context but extremely verbose; don't print by default.