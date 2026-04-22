---
name: gh-reading-reviews
description: Use when you need to inspect pull request review state, inline comments, or PR conversation comments with GitHub CLI
---

# Reading GitHub Reviews

## Overview

This skill covers the read-only GitHub CLI patterns for pull request reviews in this repository: inline comments, top-level review summaries, PR conversation comments, pagination, and compact summaries.

## Prerequisites

- `gh` CLI installed and authenticated with `gh auth status`
- Commands run from the repo root so `repos/{owner}/{repo}` auto-resolves

## Core Patterns

### Fetch PR Review Comments

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments \
  --jq '.[] | {id, path, line, body, user: .user.login}'
```

- Returns inline comments attached to specific file lines or hunks
- Use `--paginate` when you need the full result set across multiple pages

### Fetch PR Reviews

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/reviews \
  --jq '.[] | {id, state, body, user: .user.login}'
```

- `state` values include `APPROVED`, `CHANGES_REQUESTED`, `COMMENTED`, and `DISMISSED`
- The `body` contains the top-level review summary text

### Fetch Issue Or PR Conversation Comments

```bash
gh api repos/{owner}/{repo}/issues/<PR_NUMBER>/comments \
  --jq '.[] | {id, body: .body[0:500], user: .user.login}'
```

- Pull requests are issues, so this endpoint returns general PR conversation comments
- Truncate large bodies to keep output readable

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

1. `--jq` is the fastest way to extract only the fields you need from noisy review payloads.
2. `.body[0:N]` truncation prevents terminal overflow from large bot comments.
3. `--paginate` matters because GitHub REST endpoints usually default to 30 items per page.

## Common Pitfalls

1. `line` can be `null` for file-level comments not attached to a specific line.
2. Review comments and issue comments are different endpoints:
   - `/pulls/{pr}/comments` = inline code review comments
   - `/issues/{pr}/comments` = general PR conversation comments
   - `/pulls/{pr}/reviews` = top-level review summaries
3. Bot comments can be very large. Filter or truncate aggressively before presenting output.
4. `diff_hunk` is useful for context but extremely verbose; do not print it by default.
