# Hive Platform Audit - 2026-03-13

## Goal

Audit Hive's current repository, documentation, and GitHub backlog state to determine:

- what is already implemented
- what remains meaningfully incomplete
- where docs and backlog state drift from the actual product
- which next work items best support Hive as a broader AI inference platform

## Evidence Reviewed

### Repository and docs

- Root docs: `README.md`, `CHANGELOG.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `SUPPORT.md`, `GOVERNANCE.md`
- Docs indexes: `docs/README.md`, `docs/design/README.md`, `docs/runbooks/README.md`, `docs/release/README.md`, `docs/plans/README.md`
- Architecture and product docs:
  - `docs/architecture/system-architecture.md`
  - `docs/architecture/archive/2026-02-28-python-mvp-migration-map.md`
  - `docs/design/active/product-and-routing.md`
  - `docs/design/archive/2026-02-24-chat-first-guarded-home.md`
  - `docs/design/archive/2026-02-24-web-flow-critical-review.md`
  - `docs/design/archive/2026-02-28-repo-audit-cleanup-design.md`
  - `docs/design/archive/2026-02-28-repo-audit-cleanup-decision-process.md`
- Runbooks and release docs:
  - `docs/runbooks/active/*.md`
  - `docs/release/active/*.md`
- Prior audits and active planning docs:
  - `docs/audits/2026-02-28-*.md`
  - `docs/plans/active/future-implementation-roadmap.md`
  - relevant plan artifacts for issue #5, #6, #10, and the prior repo audit track

### Runtime surface checks

- Route inventory from `apps/api/src/routes/*.ts`
- Route registration from `apps/api/src/routes/index.ts`
- Workspace scripts from `apps/api/package.json` and `apps/web/package.json`
- Frontend route and shell review across:
  - `apps/web/src/app/layout.tsx`
  - `apps/web/src/app/page.tsx`
  - `apps/web/src/app/auth/page.tsx`
  - `apps/web/src/app/billing/page.tsx`
  - `apps/web/src/app/developer/page.tsx`
  - `apps/web/src/app/settings/page.tsx`
  - `apps/web/src/components/layout/*`
  - `apps/web/src/features/chat/*`
  - `apps/web/src/features/settings/*`
- Backend security/runtime review across:
  - `apps/api/src/server.ts`
  - `apps/api/src/config/env.ts`
  - `apps/api/src/runtime/services.ts`
  - `apps/api/src/runtime/redis-rate-limiter.ts`
  - `apps/api/src/runtime/supabase-*.ts`
  - `apps/api/src/providers/*`
  - payment, auth, usage, and user routes
- Local runtime verification against Docker Compose and Supabase CLI:
  - Docker `api` and `web` containers
  - local Supabase stack via `npx supabase start`
  - host-network checks for `/auth`, Supabase Auth health, and API CORS preflight
- Visual review from maintainer-provided screenshots covering:
  - `/auth`
  - `/`
  - `/billing`
  - `/developer`
  - `/settings`

### GitHub state

- Live labels and milestones
- Open issues list
- Detailed issue review for #19, #31, #35, and #37
- Issue creation and metadata verification for `#45` through `#51`

## Executive Summary

Hive is already more than a Bangladesh-specific AI gateway. The current implementation supports a broader AI inference platform story:

- OpenAI-compatible API surface
- multi-provider routing with fallback and circuit breaking
- operator-safe public/internal status and metrics boundaries
- prepaid credits, billing persistence, and reconciliation
- managed API-key lifecycle
- web workspace plus developer panel
- contributor-facing OSS process and triage machinery

The main problem is not an absence of platform substance. The main problem is drift between:

1. top-level product positioning
2. roadmap language
3. issue backlog quality

The repo increasingly behaves like an inference platform, but the docs and backlog still partially frame it as a narrower local-market API gateway or as an older MVP hardening track.

## What Is Clearly Done

### Core platform foundation

- TypeScript monorepo is the sole active runtime path.
- Fastify API and Next.js web app are both established and documented.
- Core AI endpoints exist:
  - `/v1/models`
  - `/v1/chat/completions`
  - `/v1/responses`
  - `/v1/images/generations`
- Billing and usage endpoints exist:
  - `/v1/credits/balance`
  - `/v1/usage`
  - `/v1/payments/intents`
  - `/v1/payments/demo/confirm`
  - `/v1/payments/webhook`
- Developer key-management endpoints exist:
  - `/v1/users/me`
  - `/v1/users/api-keys`
  - `/v1/users/api-keys/:id/revoke`

### Provider platform basics

- Provider registry with fallback is implemented.
- Ollama and Groq are active providers; mock remains the placeholder fallback path.
- Provider circuit breaker is implemented and documented.
- Public/internal provider status split exists and is protected as intended.
- Public/internal provider metrics split exists and is protected as intended.
- Startup provider model readiness checks are implemented using zero-token metadata endpoints.

### Billing and trust surfaces

- Credit ledger and payment persistence are documented as current behavior.
- Decimal-safe payment credit conversion is a tracked and documented invariant.
- Payment reconciliation scheduler and drift alerts are implemented and documented.
- API key lifecycle events and expiration handling are implemented and documented.

### Product and OSS operations

- Chat-first guarded home flow is implemented.
- Production-aware smoke E2E guidance exists.
- Governance, contribution, security, and support docs are present.
- GitHub issue forms, PR template, labels, milestones, and maintainer lifecycle docs are present.

## What Is Still Incomplete

### Product capability gaps

- Image generation is still mock-backed.
- File ingestion does not exist yet as a real product capability.
- Usage analytics remain shallow compared with what an inference platform should expose.
- Admin/support tooling remains thin.
- No organization/team/budget layer exists yet.
- No stronger deployment or SLO posture is documented beyond local/Docker workflows.
- Web information architecture is still fragmented:
  - sidebar only exposes Chat
  - header and profile menu expose Developer, Settings, and Billing separately
  - `/billing` is effectively a redirect card, not a real product surface
- User settings UI is partially shipped ahead of the backend contract; the panel calls `/v1/users/settings`, but no API route exists.

### Provider strategy gaps

- The provider portfolio is still narrow for a platform narrative.
- There is no durable provider/model catalog beyond static routing.
- There is no explicit cost-governance layer for multi-provider optimization.
- OpenRouter-related strategy is unresolved and currently split between a stale runtime-integration issue and a narrower metadata-collector issue.
- The current hosted-provider story is too thin for serious platform breadth:
  - Groq is the only real cloud provider path
  - Ollama is useful for local/dev and fallback, not sufficient platform breadth on its own
  - OpenRouter is the fastest breadth unlock; direct OpenAI/Anthropic/Gemini style integrations can follow once catalog and routing policy mature

### Documentation and planning gaps

- Top-level docs still foreground "Bangladesh-focused AI API gateway" more strongly than the implemented platform surface justifies.
- The active roadmap still lists several already-shipped hardening items as future work.
- Release docs remain beta-checklist oriented and under-describe the broader platform maturity path.
- Some historical audit artifacts contain stale route references from removed auth flows and should be treated as historical snapshots, not current truth.
- Local bootstrap docs were missing one critical step before this session: copying the real local Supabase `ANON_KEY` and `SERVICE_ROLE_KEY` into `.env` before starting Docker `api` and `web`.

## Deeper Frontend and Backend Findings

### Frontend surface assessment

- The web shell is functional but still feels like a stitched-together MVP rather than a coherent product workspace.
- Stale product copy remains in current user-facing code:
  - `apps/web/src/app/layout.tsx` still used `BD AI Gateway Web`
  - `apps/web/src/components/layout/app-header.tsx` still rendered `BD AI Gateway`
  - `apps/web/src/features/chat/components/chat-workspace-shell.tsx` still rendered `BD AI Chat`
  - `apps/web/src/features/auth/auth-session.ts` still used `bdai.auth.session`
- Navigation is inconsistent:
  - sidebar only includes `Chat`
  - header links to `Developer Panel` and `Settings`
  - profile dropdown also links to `Billing`
  - `/billing` itself is only a handoff card to `/settings` and `/developer`
- The product shape visible in the web app lags behind the broader platform positioning now documented elsewhere in the repo.
- The screenshot set confirms these UX problems visually:
  - `/auth` still renders stale `BD AI Gateway` branding and exposes authenticated navigation chrome before login
  - `/` chat looks usable but visually disconnected from the rest of the product and still labeled `BD AI Chat`
  - `/billing` is effectively an empty page with one explanatory card, which makes the route feel abandoned
  - `/developer` and `/settings` have real structure but read as admin-style forms dropped into the same shell rather than a coherent product workspace
  - `/settings` visibly confirms the missing backend contract with the message that the user settings endpoint is not available yet
- The mobile screenshots add a second layer of concern:
  - responsive behavior mostly works, but the top navigation is overcrowded on small screens
  - `/auth` still shows authenticated workspace actions on mobile, which is even more confusing when horizontal space is tight
  - the mobile chat shell prioritizes empty transcript space over composition and app orientation, so the product value is not obvious at first glance
  - settings remains usable on mobile, but it looks like a long internal control form rather than a polished customer-facing account surface

### Auth/bootstrap assessment

- Supabase Auth usage from the browser is intentional:
  - `/auth` posts directly to `http://127.0.0.1:54321/auth/v1/signup` via the Supabase browser SDK
  - this is an auth API call, not a direct database call
- The local runtime initially failed because the running Docker `web` and `api` containers were using placeholder Supabase credentials.
- The repo docs now clarify that local Supabase keys from `npx supabase status -o env` must be copied into `.env` before starting the stack.
- There is still a likely first-login provisioning bug:
  - `user_profiles` exists
  - the API depends on it
  - no trigger/bootstrap path was found that inserts a profile row for a new `auth.users` record

### Backend/security assessment

- No obvious browser-side secret leakage was found in the web app:
  - browser code consumes only `NEXT_PUBLIC_*` values and Supabase session tokens
  - service-role and provider secrets remain server-side
- Public/internal provider status and metrics boundaries are implemented correctly at a high level.
- Two meaningful runtime defects were identified:
  - API CORS support was missing entirely before this session
  - Redis rate limiting fails open on backend errors
- The API now has local CORS support for browser requests from the current web origins, and the targeted preflight test plus host-network verification passed.
- Startup logs still show provider-readiness weakness in local environments when Ollama is reachable late or the configured model is missing.

### Efficiency and product maturity assessment

- The current platform is operationally credible for a single-user or early-beta environment, but it is not yet efficient enough for a stronger platform claim in three areas:
  - provider breadth and model coverage
  - analytics and support tooling
  - account/org/commercial controls
- The codebase has enough surface area that the next wins should come from reducing mismatch and dead ends, not from adding many more half-wired UI panels.

## Backlog Audit

### Open issues that still make sense

- `#19` Free tier / zero-cost access control:
  - still relevant as a platform monetization and abuse-control problem
  - blocked status is plausible, but the issue body still uses old package names in verification commands and frames the feature as a narrower access-policy slice
- `#37` OpenRouter metadata collector:
  - strategically plausible if reframed as provider intelligence and catalog enrichment
  - fits a platform direction better than direct runtime integration as a first step

### Open issues with backlog drift

- `#35` Repo-wide audit cleanup:
  - most acceptance criteria are already substantially delivered
  - leaving it open as `status:ready` with no milestone now creates confusion
  - this should likely be closed or explicitly reduced to a smaller follow-up scope

- `#31` OpenRouter runtime integration:
  - issue body is stale, over-scoped, and anchored in a removed implementation direction
  - it still carries old assumptions, outdated planning references, and low-quality metadata hygiene
  - as written, it is not implementation-ready and should not remain a normal open feature issue without re-triage

## GitHub Actions Executed In This Session

### Closed as stale or superseded

- `#31` Closed because the runtime OpenRouter integration issue no longer represented a safe or current implementation direction.
- `#35` Closed because the umbrella repo-audit cleanup scope is already materially delivered and should not remain as a stale open tracker.

### Refined and kept active

- `#12` Rewritten as a real image-provider integration issue and moved to `status:ready`.
- `#13` Rewritten as a focused analytics/support-tooling issue and moved to `status:ready`.
- `#19` Rewritten to match the current `@hive/*` repo shape while remaining `status:blocked`.
- `#37` kept active as the narrower OpenRouter metadata/intelligence issue and moved to `status:ready`.

### Created from audit gaps

- `#45` Add normalized provider and model catalog with provenance
- `#46` Add organization, team, and budget-control foundations
- `#47` Fix first-login provisioning for Supabase-authenticated users
- `#48` Add OpenRouter provider adapter as the first hosted breadth unlock
- `#49` Consolidate web IA and remove stale BD AI Gateway placeholder surfaces
- `#50` Implement `/v1/users/settings` for the shipped web settings panel
- `#51` Harden Redis rate limiting so backend failures do not silently bypass limits

### Resulting open backlog

After cleanup, the active open issue set is:

- `#12` real image provider integration
- `#13` analytics and support tooling
- `#19` zero-cost free-tier policy
- `#37` OpenRouter metadata collector
- `#45` provider/model catalog layer
- `#46` organization/team/budget foundations
- `#47` first-login provisioning for Supabase-authenticated users
- `#48` OpenRouter provider adapter
- `#49` web IA and stale-branding cleanup
- `#50` missing `/v1/users/settings` backend contract
- `#51` rate limiter failure-mode hardening

### Milestone and label observations

- Milestone definitions themselves are coherent.
- Milestone usage is incomplete:
  - `#35`, `#31`, and `#37` currently have no milestone
  - `#37` should likely live in `Post-MVP - Enhancements` if retained
  - `#31` should either be closed or reclassified before milestone assignment
- Status-label hygiene is uneven:
  - some recently closed issues retained `status:needs-triage` until closure
  - some open issues have weak status semantics relative to current repo reality

## Strategic Positioning Assessment

### Strongest current story

Hive is best described today as:

> an OpenAI-compatible AI inference platform with provider routing, prepaid billing, operational observability, and a lightweight developer workspace, where Bangladesh-native payments are an important distribution and monetization wedge

That story is defensible from the code and docs already in the repo.

### Weaker current story

The narrower "Bangladesh-focused AI API gateway" framing is still true historically, but it undersells:

- provider platform features
- operator observability
- API compatibility
- managed developer access
- repo maturity as an OSS platform project

## Recommended Next Work Themes

### Near-term platform themes

1. Clarify positioning around inference-platform capabilities.
2. Clean the backlog so remaining issues are actionable and current.
3. Fix first-run auth/bootstrap and missing backend contracts before polishing more web UI.
4. Prioritize provider breadth and provider intelligence over speculative runtime sprawl.
5. Expand analytics and support tooling.
6. Define the next maturity layer for admin, org, and deployment operations.

### Recommended issue framing style

Future issues should emphasize:

- problem statement before implementation recipe
- why the feature matters to Hive's platform strategy
- concrete acceptance criteria
- verification paths that match the current package names and repo structure
- minimal but correct label and milestone assignment

## Immediate Recommendations For This Session

1. Reposition top-level docs around the broader AI inference platform narrative.
2. Rewrite the active roadmap so shipped work is removed from future phases.
3. Clean or close stale GitHub issues that no longer describe real current work.
4. Create a small number of high-signal issues for genuinely missing platform capabilities.

## Recommended Next Execution Order

1. `#47` first-login provisioning for Supabase-authenticated users
2. `#50` `/v1/users/settings` backend contract
3. `#51` rate limiter failure-mode hardening
4. `#49` web IA and stale-branding cleanup
5. `#45` provider/model catalog layer
6. `#37` OpenRouter metadata collector as one possible catalog source
7. `#48` OpenRouter provider adapter
8. `#12` real image-provider integration
9. `#13` analytics and support tooling
10. `#19` free-tier policy after provider-intelligence inputs are clearer
11. `#46` organization/team/budget foundations after the single-user platform surfaces are stronger

## Risks

- Overstating platform maturity before provider breadth and analytics catch up.
- Keeping stale OpenRouter backlog items can attract low-quality or conflicting implementation work.
- Leaving historical audit docs unqualified can confuse contributors about current route and auth behavior.
- Shipping more UI before fixing auth bootstrap and missing backend contracts will make the web app feel less trustworthy, not more complete.
- Silent fail-open infrastructure policies can turn ordinary dependency incidents into abuse or cost-amplification events.

## Conclusion

Hive has crossed the threshold where the limiting factor is no longer "does this repo contain a credible platform core?" The answer is yes.

The limiting factors are now:

- sharper strategic narrative
- more disciplined backlog shape
- clearer separation between shipped platform primitives and still-missing expansion work
