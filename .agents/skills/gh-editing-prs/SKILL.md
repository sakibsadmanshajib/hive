---
name: gh-editing-prs
description: Use when you need to inspect or update GitHub pull request metadata, descriptions, or titles and handle repository-specific edit failures
---

# Editing GitHub Pull Requests

## Overview

This skill covers pull request metadata reads and PR mutation workflows in this repository, including the REST fallback to use when `gh pr edit` fails because of deprecated classic Projects GraphQL access.

## Prerequisites

- `gh` CLI installed and authenticated with `gh auth status`
- Commands run from the repo root so `repos/{owner}/{repo}` auto-resolves

## Core Patterns

### Get PR Metadata

```bash
gh pr view <PR_NUMBER> --json title,body,headRefName,baseRefName,state,url
```

- Prefer `gh pr view` for structured PR metadata reads.
- Limit `--json` fields to the ones you actually need.

### Update PR Description With REST

```bash
gh api repos/{owner}/{repo}/pulls/<PR_NUMBER> \
  -X PATCH \
  --raw-field body="$(cat /tmp/pr-body.md)"
```

- Use this when updating a PR description from prepared markdown.
- `--raw-field` preserves markdown text without requiring manual JSON escaping.
- The same REST patch pattern works for other mutable PR fields such as `title`.

## Known Failure Mode

```bash
gh pr edit <PR_NUMBER> --body-file /tmp/pr-body.md
```

- In this repository or environment, `gh pr edit` can fail with:
  `GraphQL: Projects (classic) is being deprecated ... (repository.pullRequest.projectCards)`.
- When that happens, use the REST patch endpoint instead of retrying the GraphQL-backed command.

## Common Pitfalls

1. Do not assume `gh pr edit` is safe here for PR body updates.
2. Prefer a prepared markdown file for larger PR descriptions instead of embedding long multiline strings directly in the shell.
3. If you are changing both the title and body, keep the patch explicit so it is obvious which fields are being updated.
