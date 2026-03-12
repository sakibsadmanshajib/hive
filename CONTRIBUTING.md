# Contributing to Hive

Thanks for contributing to Hive.

## Before You Start

- Read `README.md` for product and runtime context.
- Read `AGENTS.md` for repository operating rules, required verification, and documentation discipline.
- Check `docs/README.md` for architecture, runbooks, and active planning artifacts.
- Keep changes minimal and localized. Do not mix unrelated concerns in one PR.

## Development Workflow

1. Open or reference the issue you are working on for non-trivial changes.
2. Write or update a plan in `docs/plans/` before code edits for multi-step work.
3. Keep tests, docs, and implementation aligned in the same change.
4. Use descriptive branch names when working outside the main branch.
5. Prefer atomic commits with Conventional Commit style when practical.

## Local Setup

Install dependencies:

```bash
pnpm install
```

Common verification commands:

```bash
pnpm --filter @hive/api test
pnpm --filter @hive/api build
pnpm --filter @hive/web build
```

If your change touches web auth/bootstrap, browser env usage, or smoke flows, also verify the production bundle with required public envs:

```bash
NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 \
NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 \
NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key \
pnpm --filter @hive/web build
```

If your change touches auth, chat, billing, or related integration flows, run the smoke flow when your environment supports it:

```bash
pnpm --filter @hive/web exec playwright install chromium
pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts
```

## Repository Structure

- `apps/api` - Fastify API, routing, provider integrations, billing, and runtime services
- `apps/web` - Next.js application
- `packages/openapi/openapi.yaml` - canonical OpenAPI contract
- `docs/` - architecture, design, plans, runbooks, release, and engineering guidance
- `supabase/migrations/` - database schema migrations

## Pull Request Expectations

- Explain the user-facing or operator-facing change clearly.
- Use the repository pull request template and complete the verification/docs/risk checklist.
- Add or update tests for behavior changes.
- Update docs in the same change when behavior, policy, or operations change.
- Update `CHANGELOG.md` for notable changes.
- Keep public and internal provider boundaries intact:
  - public status and metrics endpoints must remain sanitized
  - internal diagnostics must remain admin-token protected

## Verification Expectations

For API-impacting changes:

```bash
pnpm --filter @hive/api test
pnpm --filter @hive/api build
```

For web-impacting changes:

```bash
pnpm --filter @hive/web build
```

For docs-only or policy-only changes, provide explicit verification checks such as file presence, link discoverability, and any repo-wide sanity build requested by the issue.

## Documentation Discipline

- Update `README.md` for quickstart or contributor-facing behavior changes.
- Update `docs/` for architecture, runbooks, design, or operational changes.
- Keep implementation plans in `docs/plans/`.
- Prefer concrete commands, paths, and examples over abstract guidance.

## Security and Sensitive Changes

- Never commit secrets, tokens, or credentials.
- Do not include private provider diagnostics or sensitive billing data in public docs or issues.
- Report vulnerabilities using the process in `SECURITY.md`.

## Need Help?

- Use `SUPPORT.md` for support routing.
- Use `CODE_OF_CONDUCT.md` for community behavior expectations.
- Use `GOVERNANCE.md` for project decision-making and maintainer-role guidance.
- Use `docs/runbooks/active/github-triage.md` for issue forms, label taxonomy, milestone routing, and GitHub metadata sync operations.
