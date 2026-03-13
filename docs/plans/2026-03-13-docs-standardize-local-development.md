## Goal
Standardize Hive's contributor-facing local setup, development workflow, and GitHub PR hygiene docs so the repository has one clear onboarding path and one clear explanation of Supabase CLI plus Docker responsibilities.

## Assumptions
- This task is docs/process only, except for updating live PR metadata for PR `#52`.
- `README.md` should remain the primary quickstart and local development entry point.
- `CONTRIBUTING.md` should focus on workflow, verification, and PR hygiene rather than duplicate full setup docs.
- `docs/README.md` and runbooks should point readers to the right canonical entry points instead of re-explaining everything.

## Plan
1. Files: `README.md`, `CONTRIBUTING.md`, `docs/README.md`, `docs/runbooks/README.md`
   Change: Audit and rewrite the contributor-facing doc hierarchy so `README.md` becomes the canonical quickstart/local dev guide, `CONTRIBUTING.md` becomes the workflow/PR hygiene guide, and doc indexes point to the correct entry points.
   Verify: `rg -n "Getting Started|Local Setup|Development Workflow|PR" README.md CONTRIBUTING.md docs/README.md docs/runbooks/README.md`

2. Files: `README.md`
   Change: Replace the current fragmented local setup guidance with a strict, standardized bootstrap sequence covering dependencies, Supabase CLI startup, local key extraction, `.env` wiring, migration/reset flow, Docker startup, verification commands, and when to use `pnpm dev` instead.
   Verify: `sed -n '1,260p' README.md`

3. Files: `README.md`, `docs/architecture/system-architecture.md`, `docs/runbooks/active/web-e2e-smoke.md`
   Change: Standardize the explanation of why Supabase runs via the CLI while Hive services run in Docker, why `api` and `web` are separate containers, and why missing copied Supabase keys breaks local auth even when containers boot.
   Verify: `rg -n "Supabase CLI|host.docker.internal|api and web are separate|ANON_KEY|SERVICE_ROLE_KEY" README.md docs/architecture/system-architecture.md docs/runbooks/active/web-e2e-smoke.md`

4. Files: `CONTRIBUTING.md`, `docs/runbooks/active/github-triage.md`, `docs/engineering/git-and-ai-practices.md`
   Change: Tighten PR hygiene guidance so PR titles are expected to be scoped and Conventional-Commit-style where practical, and ensure the repo docs consistently distinguish commit style from PR title style without encouraging vague umbrella titles.
   Verify: `rg -n "Conventional Commit|PR title|pull request template|scoped" CONTRIBUTING.md docs/runbooks/active/github-triage.md docs/engineering/git-and-ai-practices.md`

5. Files: `CHANGELOG.md`
   Change: Record the documentation/process standardization and local development onboarding cleanup in the unreleased changelog.
   Verify: `rg -n "Local Development|PR Hygiene|Supabase CLI|Getting Started" CHANGELOG.md`

6. Files: live PR `#52` metadata only
   Change: Update the PR title to an umbrella scoped title that reflects both the audit/docs work and the auth bootstrap fix.
   Verify: `gh pr view 52 --json title`

7. Files: docs touched above
   Change: Run the required sanity verification and inspect the final diff for consistency.
   Verify: `pnpm --filter @hive/api build` and `NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY="$(npx supabase status -o env | sed -n 's/^ANON_KEY=\"\\(.*\\)\"$/\\1/p')" pnpm --filter @hive/web build`

## Risks & mitigations
- Risk: duplicating setup guidance across multiple docs again.
  Mitigation: make `README.md` canonical and convert other docs to pointers plus only the extra context they uniquely own.
- Risk: over-prescribing PR titles in a way that conflicts with current practice.
  Mitigation: document "scoped and Conventional-Commit-style where practical" rather than inventing a stricter rule than the repo actually wants.
- Risk: docs drift from the real local runtime again if the Supabase/Docker explanation is inconsistent.
  Mitigation: update all current onboarding touchpoints in one change and keep the explanation identical.

## Rollback plan
- Revert the docs/process commit if the standardized onboarding or PR hygiene wording is rejected.
- Patch PR `#52` title back to the previous value if maintainers dislike the updated umbrella title.
