---
name: gh-api
description: How to interact with the GitHub CLI (gh) API for PR reviews, comments, and issue management
---

# GitHub CLI API Skill

This skill documents how to use `gh api` to interact with GitHub's REST API from this repository.

## Prerequisites

- `gh` CLI installed and authenticated (`gh auth status`)
- Commands run from within the repo root so `{owner}/{repo}` auto-resolves

## Core Patterns

### Fetch PR Review Comments (inline code comments)

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments \
  --jq '.[] | {id, path, line, body, user: .user.login}'
```

- Returns inline comments attached to specific file lines/hunks
- `{owner}/{repo}` is auto-resolved by `gh` from the current git remote
- Use `--jq` to filter JSON output

### Fetch PR Reviews (top-level review summaries)

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/reviews \
  --jq '.[] | {id, state, body, user: .user.login}'
```

- `state` values: `APPROVED`, `CHANGES_REQUESTED`, `COMMENTED`, `DISMISSED`
- The `body` contains the full review summary text

### Fetch Issue/PR General Comments

```bash
gh api repos/{owner}/{repo}/issues/<PR_NUMBER>/comments \
  --jq '.[] | {id, body: .body[0:500], user: .user.login}'
```

- PRs are issues, so issue comments API works for PR conversation comments
- Truncate `.body` with `[0:N]` to avoid overwhelming output

### Reply To Inline Review Comments

Use the PR-scoped review-comment reply endpoint:

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments/<COMMENT_ID>/replies \
  -X POST \
  -f body='Fixed in `<COMMIT_SHA>`.

Brief explanation of the change.'
```

- This creates a reply in the existing inline review thread.
- Use this for comments from `/pulls/<PR_NUMBER>/comments`, not for top-level PR comments.

Known bad pattern:

```bash
gh api repos/{owner}/{repo}/pulls/comments/<COMMENT_ID>/replies -X POST ...
```

- This returns `404 Not Found` in this repository.
- Keep the PR number in the path: `/pulls/<PR_NUMBER>/comments/<COMMENT_ID>/replies`.

### Get PR Metadata

```bash
gh pr view <PR_NUMBER> --json title,body,headRefName,baseRefName,state,url
```

- Prefer `gh pr view` over raw API for structured PR metadata
- Supports `--json` with field selection

### Count Comments

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments --jq 'length'
```

### Filter Comments by File

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments \
  --jq '.[] | select(.path == "path/to/file.ts") | .body'
```

### Summary View (file + severity + line)

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments \
  --jq '.[] | "FILE: \(.path) | LINE: \(.line) | SEVERITY: \(.body | split("\n")[0])"'
```

## What Works Well

1. **`{owner}/{repo}` placeholder** — `gh api` auto-resolves this from the git remote. No need to hardcode the repo.
2. **`--jq` filtering** — Essential for extracting structured data from large review payloads.
3. **Truncating body text** — Use `.body[0:N]` in jq to prevent terminal overflow from long review comments.
4. **`gh pr view --json`** — Cleaner than raw API for PR metadata.
5. **Pagination** — `gh api` returns a single page by default. GitHub REST endpoints commonly default to 30 items per page, so add `--paginate` when you need the full result set.

## Common Pitfalls

1. **`line` can be `null`** — File-level comments (not attached to a specific line) return `line: null`. Handle this in jq filters.
2. **Review comments vs issue comments** — They are different API endpoints:
   - `/pulls/{pr}/comments` = inline code review comments
   - `/issues/{pr}/comments` = general PR conversation comments
   - `/pulls/{pr}/reviews` = top-level review summaries
   - `/pulls/{pr}/comments/{comment_id}/replies` = reply inside an inline review thread
3. **Bot comments are verbose** — CodeRabbit and GitGuardian comments contain large HTML/markdown bodies. Always truncate or filter.
4. **Rate limits** — Authenticated `gh` has 5000 req/hr. Unlikely to hit in practice, but avoid loops.
5. **`diff_hunk` field** — Contains the diff context around each comment. Useful for understanding what code the comment references, but very verbose.

## Workflow: Addressing PR Review Comments

1. Fetch all inline comments with file/line/severity summary
2. Fetch top-level review body for overview comments
3. Fetch issue comments for bot alerts (GitGuardian, etc.)
4. Triage each comment against current code (some may be false positives if code was updated after the review)
5. Apply fixes, verify with tests, commit and push
