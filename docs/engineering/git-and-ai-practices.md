# Git and AI Development Practices

This document defines repository standards for Git hygiene and AI-assisted development.

## Why This Exists

The codebase is developed by humans and AI agents. Clear Git and documentation standards reduce ambiguity, improve traceability, and keep changes maintainable.

## Git Practices

### Commit Style

- Prefer atomic commits (one concern per commit).
- Use Conventional Commits where practical:
  - `feat(api): ...`
  - `fix(providers): ...`
  - `docs(readme): ...`
  - `chore(infra): ...`
- Write commit messages with intent and rationale, not only a diff summary.

### Branch Naming

- Use descriptive branch names:
  - `feat/provider-status-internal`
  - `fix/payment-webhook-idempotency`
  - `docs/architecture-update`
- Avoid vague names like `updates` or `temp-branch`.

### Change Scope

- Do not mix unrelated concerns in one commit/PR.
- Avoid broad formatting-only churn unless requested.
- Keep history reviewable and revert-friendly.

## Documentation Practices

- If behavior changes, update docs in the same change:
  - `README.md` for quickstart/public behavior
  - `docs/` for architecture, runbooks, and implementation details
- Keep implementation plans in `docs/plans/`, not in local-only scratch directories, when the plan should persist across sessions.
- Prefer explicit examples (commands, payloads, env vars) over abstract prose.
- Keep endpoint and env-var docs synchronized with implementation.

## AI-Assisted Development Practices

- Treat AI output as draft until reviewed and verified.
- Run tests/build checks before completion claims.
- Preserve explicit constraints in code and docs to reduce AI guesswork.
- If a secret appears in prompts/chat/logs, rotate it immediately.

## Security Notes

- Never commit API keys or secrets.
- Keep internal diagnostics protected (for this repo: admin-token-gated internal provider status endpoint).
- Public status endpoints should expose sanitized operational data only.

## Required Verification Before Completion

For API-impacting changes:

```bash
pnpm --filter @hive/api test
pnpm --filter @hive/api build
```

For web-impacting changes:

```bash
pnpm --filter @hive/web build
```

For infra/runtime changes (when Docker is available):

```bash
docker compose up --build -d
docker compose ps
```
