# Phase 2: Identity & Account Foundation - Research

**Researched:** 2026-03-28
**Domain:** Hosted Supabase auth, workspace tenancy, control-plane account ownership, Next.js console sessions, and progressive billing-profile capture
**Confidence:** HIGH

## Summary

Phase 2 is where Hive stops being only a compatibility harness and becomes a real authenticated product. The key architectural move is to keep authentication inside hosted Supabase while introducing a Hive-owned control plane and database schema for workspace accounts, memberships, invitations, and profile data. Supabase should stay responsible for email/password auth, verification, recovery emails, and browser sessions. Hive should own the durable account, membership, and billing-identity state that later billing, API-key, and console phases depend on.

The repository is still largely greenfield beyond the Phase 1 edge API and SDK harness, so Phase 2 needs to bootstrap both a Go `control-plane` service and a Next.js `web-console` app. The cleanest shape is:

1. Supabase handles sign-up, sign-in, verify-email, and reset-password.
2. The web console uses Supabase SSR session helpers so sessions survive refresh and normal revisits.
3. The Go control plane accepts authenticated Supabase bearer tokens, provisions a default workspace account on first login, and exposes Hive-owned account/profile APIs backed by direct Postgres access.
4. The console shows a real dashboard shell even for unverified users, but sensitive actions remain locked until verification is complete.
5. First-run onboarding stays short and only captures the minimal account profile. Billing, legal-entity, and tax capture live in settings or billing-triggered flows, not in initial onboarding.

**Primary recommendation:** create `apps/control-plane` and `apps/web-console`, add `accounts`, `account_memberships`, `account_invitations`, `account_profiles`, and `account_billing_profiles` tables in Supabase Postgres, bootstrap workspace membership from the first authenticated session, and use a verification-aware viewer API so the web console can enforce the limited-console experience without duplicating business rules in the browser.

<user_constraints>

## User Constraints (from CONTEXT.md)

### Locked Decisions

- Signing up creates a workspace-style Hive account, not only a standalone user profile.
- Shared accounts must be usable in Phase 2, including invites, joining, and basic member management.
- The default account label in the UI should seed from the owner's personal name even though the account itself is a workspace entity.
- Unverified users may enter a limited console instead of being hard-blocked at auth.
- The unverified experience should be mostly locked, with access limited to a view-only shell plus basic account maintenance.
- Sensitive actions remain blocked until verification is complete, including team invites and API key creation.
- The console must show a persistent verification banner until the email is verified.
- The limited-console policy does not escalate into a harder lockout over time.
- First run should use a short setup step followed by the real dashboard.
- The highest-priority first-run tasks are verifying email and completing the minimal account profile.
- Returning users should land on the main dashboard, not a setup-only route or the last deep-linked page.
- Billing, legal-entity, and tax collection should not happen during initial onboarding.
- The minimal pre-billing profile is owner name, login email, account display name, account type, country, and state/province.
- Billing and tax forms must adapt to personal vs business account context.
- Missing billing or tax details must remain optional until checkout or invoicing.

### Claude's Discretion

- Exact invite-token shape, membership-list UI structure, and workspace-switcher interaction details.
- Exact control-plane endpoint naming, provided the API boundary remains stable and explicit.
- Exact form-library choice and component structure inside the web console.

### Deferred Ideas (OUT OF SCOPE)

- Rich org hierarchies, role matrices beyond owner/member, and procurement-style approvals.
- Full checkout, invoice issuance, and tax-collection workflows before payment/invoice phases.

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| AUTH-01 | Developer can sign up and sign in with email and password using Supabase-backed authentication. | Use hosted Supabase Auth for all password flows; do not build a second password system inside Hive. The web console should use Supabase SSR helpers and explicit auth pages that call `signUp` and `signInWithPassword`. |
| AUTH-02 | Developer receives email verification and can reset password through an email-based recovery flow. | Verification and password recovery should use Supabase email flows and redirect back into the Hive console via callback/recovery routes. The viewer API should expose `email_verified` so the console can enforce limited access before verification. |
| AUTH-03 | Developer session persists across browser refresh in the billing and key-management console. | Use Next.js server-side session refresh middleware plus Supabase cookies so the console can survive hard refreshes and browser revisits without custom token plumbing in local storage. |
| AUTH-04 | Account owner can maintain billing contact, legal entity, country, and VAT/business information used for invoicing and tax handling. | Store durable Hive-owned account profile and billing profile records in Supabase Postgres through control-plane APIs. Keep advanced billing/tax forms out of onboarding, but make them available through settings and future checkout entry points. |

</phase_requirements>

## Standard Stack

### Core

| Technology | Version / Variant | Purpose | Why It Fits Phase 2 |
|------------|-------------------|---------|---------------------|
| Hosted Supabase (`yimgflllgdsbcibnaxqe`) | Managed platform | Auth plus primary transactional Postgres | Already locked by project decisions; gives email auth, recovery flows, and managed Postgres without introducing more infra in Phase 2. |
| Go | 1.26.1 line | Control-plane service | Matches the project stack, keeps direct SQL access explicit, and avoids pushing business-state writes into browser clients or generated Supabase REST layers. |
| `pgx/v5` | Go driver family | Direct Postgres access | Fits the project guidance to use direct Postgres patterns for transactional flows. |
| Next.js | 16.1 | Web console | Matches the project research and gives SSR/session middleware patterns needed for persistent authenticated console flows. |
| React | 19.2 | Console UI | Current stable pairing with Next.js 16.1 for the dashboard shell and settings/forms. |
| Supabase SSR helpers | App Router session management | Browser/session persistence | Best fit for server-rendered session refresh and auth callbacks in the web console. |
| Docker Compose | Existing repo pattern | Local development workflow | Phase 2 must preserve the Docker-only workflow rather than introducing host-installed Node or Go requirements. |

### Supporting

| Library / Tool | Purpose | When to Use |
|----------------|---------|-------------|
| Standard `net/http` + small router layer | Control-plane HTTP endpoints | Keep the service thin and consistent with the current repo style instead of introducing a large web framework. |
| Vitest + React Testing Library | Component and route-gating tests | Verify banner logic, route gating, and profile-form validation without needing a full browser for every check. |
| Playwright | Browser/session smoke tests | Verify sign-in, refresh persistence, dashboard landing, and setup/profile flows end to end. |
| SQL migrations under `supabase/migrations/` | Durable schema evolution | Keep account/profile structures explicit and reviewable. |

## Architecture Patterns

### Pattern 1: Supabase Auth, Hive-Owned Business State

**What:** Supabase owns credentials, recovery, and session issuance. Hive's control plane owns account provisioning, memberships, invitations, and billing-related profile state.

**Why:** This preserves the low-ops hosted-auth choice without giving up ownership of the product-critical business model that billing and API-key phases will depend on.

**Recommended API boundary:**

```text
Web console -> Supabase auth session -> Hive control-plane viewer/profile APIs -> Supabase Postgres
```

### Pattern 2: Workspace-First Account Bootstrap

**What:** The first authenticated visit provisions a default workspace account and owner membership if none exist.

**Why:** The product wants shared accounts to be real from day one. Provisioning only a personal profile first and retrofitting workspace semantics later creates avoidable migration pain.

**Recommended initial entities:**

- `accounts`
- `account_memberships`
- `account_invitations`
- `account_profiles`
- `account_billing_profiles`

### Pattern 3: Verification-Aware Capability Gates

**What:** The backend computes gates such as `can_invite_members` and `can_manage_api_keys` from the authenticated viewer's verification state and role.

**Why:** The browser should render the locked-console experience, but the authoritative policy belongs in the control plane so later phases reuse the same rules.

**Recommended viewer contract:**

```json
{
  "user": {
    "id": "uuid",
    "email": "owner@example.com",
    "email_verified": false
  },
  "current_account": {
    "id": "uuid",
    "display_name": "Sakib's Workspace",
    "account_type": "personal",
    "role": "owner"
  },
  "memberships": [],
  "gates": {
    "can_invite_members": false,
    "can_manage_api_keys": false
  }
}
```

### Pattern 4: Progressive Profile Completion

**What:** Split profile capture into:

1. minimal setup fields required before normal dashboard use
2. richer billing/tax identity fields used later in checkout/invoicing

**Why:** This matches the locked decisions from context: onboarding should stay short, while tax/billing data remains optional until it is actually needed.

**Recommended split:**

- `/console/setup`: owner name, account name, account type, country, state/province
- `/console/settings/profile`: edit core account info plus email maintenance
- `/console/settings/billing`: billing contact, legal-entity, VAT/tax data

## Common Pitfalls

### Pitfall 1: Treating `auth.users` as the full account model

Supabase users are identities, not workspaces. If Hive stores business state directly against `auth.users` with no account/membership layer, shared accounts and later key/billing ownership become awkward immediately.

### Pitfall 2: Writing business tables directly from the browser

Using the browser-side Supabase client to mutate account/profile tables blurs the policy boundary and makes role/verification gates harder to keep consistent. Browser code should talk to Hive-owned control-plane endpoints for business data.

### Pitfall 3: Using a hard auth-block for unverified users

That would conflict with the context. The better implementation is to allow dashboard entry with a locked shell, persistent banner, and precise backend-enforced gates on sensitive actions.

### Pitfall 4: Putting VAT/legal-entity forms into the first-run setup

That conflicts with the context and makes onboarding too heavy before a customer is anywhere near checkout or invoicing.

### Pitfall 5: Forgetting account switching or membership visibility

Even with only owner/member roles, Phase 2 needs enough membership visibility and invitation flow to make shared workspaces usable.

## Open Questions

These are not blockers, but execution should settle them early:

1. Should accepting an invitation immediately switch the active workspace, or should the user stay on the current workspace until they select the new one?
   - Recommendation: keep the current workspace unchanged and surface the new membership in the switcher. This avoids surprising context switches.
2. Should billing settings be editable while the user is unverified?
   - Recommendation: allow core profile and billing-profile maintenance, but keep invite/API-key actions blocked.
3. Should the account bootstrap happen via explicit control-plane endpoint or SQL trigger?
   - Recommendation: use an explicit control-plane bootstrap on first authenticated load. It is easier to test, version, and extend than trigger-heavy auth-user provisioning.

## Validation Architecture

### Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework (control-plane)** | `go test` with `httptest` and repository/service tests |
| **Framework (web unit)** | Vitest + React Testing Library |
| **Framework (web e2e)** | Playwright |
| **Config file** | `apps/web-console/vitest.config.ts` and `apps/web-console/playwright.config.ts` (Wave 0) |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -short && docker compose -f deploy/docker/docker-compose.yml run --rm web-console npm run test:unit` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -count=1 && docker compose -f deploy/docker/docker-compose.yml run --rm web-console npm run test:unit && docker compose -f deploy/docker/docker-compose.yml run --rm web-console npm run test:e2e` |
| **Estimated runtime** | ~180 seconds |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| AUTH-01 | Sign-up/sign-in routes call hosted Supabase auth and complete session bootstrap | integration | `docker compose -f deploy/docker/docker-compose.yml run --rm web-console npm run test:e2e -- --grep "sign in bootstraps workspace"` | No -- Wave 0 |
| AUTH-02 | Verification banner and password recovery flows render correct states | unit + integration | `docker compose -f deploy/docker/docker-compose.yml run --rm web-console npm run test:unit` | No -- Wave 0 |
| AUTH-02 | Unverified viewers cannot create invitations | API | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/accounts/... -run TestInvitationRequiresVerifiedEmail` | No -- Wave 0 |
| AUTH-03 | Console session persists across refresh and revisit | integration | `docker compose -f deploy/docker/docker-compose.yml run --rm web-console npm run test:e2e -- --grep "dashboard session persists"` | No -- Wave 0 |
| AUTH-04 | Core profile saves owner/account/country/state fields durably | API + UI | `docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/profiles/... -count=1 && docker compose -f deploy/docker/docker-compose.yml run --rm web-console npm run test:e2e -- --grep "setup saves profile"` | No -- Wave 0 |
| AUTH-04 | Billing/legal/tax profile branches correctly for personal vs business | unit + integration | `docker compose -f deploy/docker/docker-compose.yml run --rm web-console npm run test:unit && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./internal/profiles/... -run TestBillingProfileValidation` | No -- Wave 0 |

### Sampling Rate

- **Per task commit:** run the quick suite
- **Per wave merge:** run the full suite
- **Before `$gsd-verify-work`:** full suite must be green
- **Max feedback latency:** 180 seconds

### Wave 0 Gaps

- [ ] `deploy/docker/Dockerfile.control-plane` — Go image for the new service
- [ ] `deploy/docker/Dockerfile.web-console` — Next.js dev/test container
- [ ] `apps/control-plane/go.mod` — new Go module
- [ ] `apps/control-plane/cmd/server/main.go` — control-plane entrypoint
- [ ] `apps/web-console/package.json` — console package and scripts
- [ ] `apps/web-console/vitest.config.ts` — unit-test config
- [ ] `apps/web-console/playwright.config.ts` — browser-test config
- [ ] `supabase/migrations/20260328_01_identity_foundation.sql` — tenancy/profile schema
- [ ] `supabase/migrations/20260328_02_billing_identity_profiles.sql` — billing profile schema
- [ ] `.env.example` — shared Supabase and app env contract

### Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Verification emails and password recovery emails are branded and route to the correct Hive URLs | AUTH-02 | Requires real hosted email delivery | Trigger sign-up and password reset for a sandbox user, open the delivered email, confirm the CTA lands on the intended console callback/recovery route. |
| Locked-console UX is understandable for unverified users | AUTH-02 | Requires visual judgment | Sign in as an unverified user, confirm the banner is persistent, the dashboard is visible, and invite/API-key actions are visibly disabled instead of silently failing. |
| Workspace switcher and members page feel coherent once a user belongs to multiple accounts | AUTH-01 | Interaction quality | Accept an invitation, verify both accounts appear in the switcher, then switch between them and confirm the displayed members/profile data changes. |

## Sources

### Primary (HIGH confidence)

- `.planning/phases/02-identity-account-foundation/02-CONTEXT.md`
- `.planning/ROADMAP.md`
- `.planning/REQUIREMENTS.md`
- `.planning/PROJECT.md`
- `.planning/STATE.md`
- `.planning/research/SUMMARY.md`
- `.planning/research/ARCHITECTURE.md`
- `.planning/research/STACK.md`

### Secondary (MEDIUM confidence)

- Existing repository structure under `apps/`, `deploy/docker/`, and `packages/`
- Phase 1 planning artifacts, especially the Docker-only workflow and validation conventions

## Metadata

**Confidence breakdown:**
- Stack fit: HIGH
- Auth/session architecture: HIGH
- Workspace/account model: HIGH
- Validation plan: MEDIUM-HIGH

**Research date:** 2026-03-28
**Valid until:** 2026-04-28

---
*Research completed: 2026-03-28*
*Ready for planning: yes*
