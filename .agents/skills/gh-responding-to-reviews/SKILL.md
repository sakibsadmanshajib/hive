---
name: gh-responding-to-reviews
description: Use when you need to reply inside GitHub pull request review threads or choose the correct review-comment reply endpoint
---

# Responding To GitHub Reviews

## Overview

This skill covers how to reply to existing inline review comments in this repository without confusing review-thread replies with top-level PR comments.

## Prerequisites

- `gh` CLI installed and authenticated with `gh auth status`
- Commands run from the repo root so `repos/{owner}/{repo}` auto-resolves

## Core Pattern

### Reply To An Inline Review Comment

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments/<COMMENT_ID>/replies \
  -X POST \
  -f body='Fixed in `<COMMIT_SHA>`.

Brief explanation of the change.'
```

- This replies inside the existing inline review thread.
- Use it only for comments fetched from `/pulls/<PR_NUMBER>/comments`.

## Known Bad Pattern

```bash
gh api repos/{owner}/{repo}/pulls/comments/<COMMENT_ID>/replies -X POST ...
```

- This path is wrong in this repository and returns `404 Not Found`.
- Keep the PR number in the path: `/pulls/<PR_NUMBER>/comments/<COMMENT_ID>/replies`.

## Common Pitfalls

1. Do not use the review-thread reply endpoint for top-level PR conversation comments from `/issues/<PR_NUMBER>/comments`.
2. Do not drop the PR number from the reply path.
3. Keep replies concise and commit-specific so reviewers can verify the change quickly.
