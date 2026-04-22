---
name: gh-responding-to-reviews
description: Use when you need to reply inside GitHub pull request review threads or choose the correct review-comment reply endpoint
---

# Responding To GitHub Reviews

## Overview

Skill cover how reply to existing inline review comments in this repo without confusing review-thread replies with top-level PR comments.

## Prerequisites

- `gh` CLI installed + authenticated via `gh auth status`
- Run from repo root so `repos/{owner}/{repo}` auto-resolves

## Core Pattern

### Reply To An Inline Review Comment

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments/<COMMENT_ID>/replies \
  -X POST \
  -f body='Fixed in `<COMMIT_SHA>`.

Brief explanation of the change.'
```

- Replies inside existing inline review thread.
- Only for comments fetched from `/pulls/<PR_NUMBER>/comments`.

## Known Bad Pattern

```bash
gh api repos/{owner}/{repo}/pulls/comments/<COMMENT_ID>/replies -X POST ...
```

- Wrong path in this repo. Returns `404 Not Found`.
- Keep PR number in path: `/pulls/<PR_NUMBER>/comments/<COMMENT_ID>/replies`.

## Common Pitfalls

1. No use review-thread reply endpoint for top-level PR conversation comments from `/issues/<PR_NUMBER>/comments`.
2. No drop PR number from reply path.
3. Keep replies terse + commit-specific so reviewers verify quick.