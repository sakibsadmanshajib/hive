# Contributing to Hive

Thanks for your interest in contributing. This document covers how to open
issues, branches, commits, pull requests, and the project rules that reviews
and CI enforce.

## Issues first

Every change starts as an issue: bug report, feature request, or a task.
Open one before writing code, and reference it from your pull request.
Pull requests without an issue reference are harder to review and may be
closed as out of process.

## Branches

Branch from the latest `main` and name branches `<type>/<short-topic>`, where
`<type>` is one of `feat`, `fix`, `chore`, `docs`, or `test`. Examples:
`feat/rag-filters`, `fix/stripe-webhook-retry`.

## Commits

Conventional commit format:

```text
<type>: <description>
```

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`.

Keep the subject line imperative, lowercase, and under about 72 characters.
DCO sign-off is not required, but keep history clean: one logical change per
commit, no stray files, no commented-out code, and never commit secrets.

## Pull requests

- Keep PRs small and focused; one concern per PR.
- Behavior changes need tests at the right level (unit, integration, or E2E).
- CI must be green on the PR before merge.
- PRs are squash-merged into `main` by the maintainer once checks pass and
  review threads are resolved. The branch is deleted after merge.

## Testing

All tests run through Docker — no host Go / Node required.

### Go unit tests

```bash
cd deploy/docker && docker compose --profile tools run toolchain \
 "cd /workspace && go test ./apps/control-plane/... -count=1 -short"
cd deploy/docker && docker compose --profile tools run toolchain \
 "cd /workspace && go test ./apps/edge-api/... -count=1 -short"
```

Note: with `go.work`, Docker test commands must use full module-relative paths
(`./apps/control-plane/internal/...`), not short `./internal/...` form.

### Frontend build + unit tests

Unlike toolchain, the web-console service mounts no volume and copies source in
at image build time, so run it with `--build` or you will silently test stale
code:

```bash
cd deploy/docker && docker compose run --build web-console npm run build
cd deploy/docker && docker compose run --build web-console npm run test:unit
```

### SDK integration tests

Requires a healthy core stack:

```bash
cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
```

## Project rules reviewers enforce

- **Provider-blind errors**: provider names must never leak into
  customer-bound error strings. Sanitize at both the control-plane and edge
  boundaries.
- **Money math uses `math/big`**: all financial calculations use `math/big`
  to prevent `float64` corruption. No float arithmetic on money paths.
- **No hardcoded secrets**: configuration comes from environment variables
  only. Never commit `.env`, keys, or tokens, and never log them.

If your change touches billing, credits, payments, auth, or tenancy, include
the commands you ran and their results in the pull request body.

## AI agent contributors

Coding agents working in this repository must read
[AGENTS.md](AGENTS.md) before making changes. It defines worktree rules,
review pipeline expectations, and repo-specific constraints that apply on top
of this guide.
