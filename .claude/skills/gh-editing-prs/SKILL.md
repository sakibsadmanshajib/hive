---
name: gh-editing-prs
description: Use when you need to inspect or update GitHub pull request metadata, descriptions, or titles and handle repository-specific edit failures
---

# Editing GitHub Pull Requests

## Overview

Skill cover PR metadata reads + mutation workflows here, include REST fallback when `gh pr edit` fail from deprecated classic Projects GraphQL access.

## Prerequisites

- `gh` CLI installed + authenticated via `gh auth status`
- Run from repo root so `repos/{owner}/{repo}` auto-resolves

## Core Patterns

### Get PR Metadata

```bash
gh pr view <PR_NUMBER> --json title,body,headRefName,baseRefName,state,url
```

- Prefer `gh pr view` for structured PR metadata reads.
- Limit `--json` fields to only ones needed.

### Update PR Description With REST

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER> \
  -X PATCH \
  --raw-field body="$(cat /tmp/pr-body.md)"
```

- Use when update PR description from prepared markdown.
- `--raw-field` preserve markdown text, no manual JSON escape.
- Same REST patch pattern work for other mutable PR fields like `title`.

## Known Failure Mode

```bash
gh pr edit <PR_NUMBER> --body-file /tmp/pr-body.md
```

- Here, `gh pr edit` can fail with:
  `GraphQL: Projects (classic) is being deprecated ... (repository.pullRequest.projectCards)`.
- When happen, use REST patch endpoint instead of retry GraphQL-backed command.

## Common Pitfalls

1. Don't assume `gh pr edit` safe here for body updates.
2. Prefer prepared markdown file for big PR descriptions vs embed long multiline strings in shell.
3. Changing both title + body → keep patch explicit so obvious which fields update.