---
phase: 13-console-integration-fixes
plan: 01
type: execute
wave: 1
depends_on: [11, 12]
branch: a/phase-13-console-integration-fixes
milestone: v1.1
track: A
files_modified:
  - .planning/phases/13-console-integration-fixes/13-AUDIT.md
  - apps/web-console/lib/control-plane/client.ts
  - apps/web-console/lib/control-plane/types.ts
  - apps/web-console/lib/api-keys.ts
  - apps/web-console/app/console/page.tsx
  - apps/web-console/app/console/analytics/page.tsx
  - apps/web-console/app/console/api-keys/page.tsx
  - apps/web-console/app/console/api-keys/[id]/limits/page.tsx
  - apps/web-console/app/console/billing/page.tsx
  - apps/web-console/app/console/catalog/page.tsx
  - apps/web-console/app/console/members/page.tsx
  - apps/web-console/app/console/setup/page.tsx
  - apps/web-console/app/console/settings/billing/page.tsx
  - apps/web-console/app/console/settings/profile/page.tsx
  - apps/web-console/app/console/account-switch/route.ts
  - apps/web-console/app/console/layout.tsx
  - apps/web-console/app/api/budget/route.ts
  - apps/web-console/app/auth/sign-in/page.tsx
  - apps/web-console/app/auth/sign-up/page.tsx
  - apps/web-console/app/auth/forgot-password/page.tsx
  - apps/web-console/app/auth/reset-password/page.tsx
  - apps/web-console/app/auth/callback/route.ts
  - apps/web-console/app/auth/sign-out/route.ts
  - apps/web-console/app/invitations/accept/page.tsx
  - apps/web-console/components/billing/billing-overview.tsx
  - apps/web-console/components/billing/checkout-modal.tsx
  - apps/web-console/components/billing/invoice-list.tsx
  - apps/web-console/components/billing/invoice-download-button.tsx
  - apps/web-console/components/billing/ledger-table.tsx
  - apps/web-console/components/billing/ledger-csv-export.tsx
  - apps/web-console/components/billing/budget-alert-form.tsx
  - apps/web-console/components/billing/budget-alert-banner.tsx
  - apps/web-console/components/analytics/analytics-controls.tsx
  - apps/web-console/components/analytics/analytics-table.tsx
  - apps/web-console/components/analytics/spend-chart.tsx
  - apps/web-console/components/analytics/error-chart.tsx
  - apps/web-console/components/analytics/usage-chart.tsx
  - apps/web-console/components/analytics/time-window-picker.tsx
  - apps/web-console/components/api-keys/api-key-list.tsx
  - apps/web-console/components/api-keys/api-key-create-form.tsx
  - apps/web-console/components/api-keys/revoke-confirm-panel.tsx
  - apps/web-console/components/api-keys/rate-limit-form.tsx
  - apps/web-console/components/catalog/model-catalog-table.tsx
  - apps/web-console/components/profile/account-profile-form.tsx
  - apps/web-console/components/profile/billing-contact-form.tsx
  - apps/web-console/components/profile/business-tax-form.tsx
  - apps/web-console/components/email-settings-card.tsx
  - apps/web-console/components/verification-banner.tsx
  - apps/web-console/components/workspace-switcher.tsx
  - apps/web-console/components/nav-shell.tsx
  - apps/web-console/components/app-shell/auth-shell.tsx
  - apps/web-console/components/app-shell/console-shell.tsx
  - apps/web-console/lib/viewer-gates.ts
  - apps/web-console/lib/profile-schemas.ts
  - apps/web-console/lib/format/credits.ts
  - apps/web-console/tests/e2e/console-dashboard.spec.ts
  - apps/web-console/tests/e2e/console-api-keys.spec.ts
  - apps/web-console/tests/e2e/console-billing.spec.ts
  - apps/web-console/tests/e2e/console-analytics.spec.ts
  - apps/web-console/tests/e2e/console-catalog.spec.ts
  - apps/web-console/tests/e2e/console-members.spec.ts
  - apps/web-console/tests/e2e/console-settings.spec.ts
  - apps/web-console/tests/e2e/console-setup.spec.ts
  - apps/web-console/tests/e2e/auth-flows.spec.ts
  - apps/web-console/tests/e2e/invitations.spec.ts
  - .planning/phases/13-console-integration-fixes/13-VERIFICATION.md
  - .planning/REQUIREMENTS.md
autonomous: true
requirements:
  - CONSOLE-13-01
  - CONSOLE-13-02
  - CONSOLE-13-03
  - CONSOLE-13-04
  - CONSOLE-13-05
  - CONSOLE-13-06
  - CONSOLE-13-07
  - CONSOLE-13-08
  - CONSOLE-13-09
  - CONSOLE-13-10
must_haves:
  truths:
    - "Every console route under apps/web-console/app/ has been click-traversed by Playwright in headless Chromium and either renders without console error / network 4xx-5xx OR has its broken integration logged in 13-AUDIT.md with severity P0/P1/P2."
    - "All TypeScript types crossing the web-console <-> control-plane boundary derive from a single source-of-truth module (apps/web-console/lib/control-plane/types.ts) generated or hand-mirrored from packages/openai-contract/generated/hive-openapi.yaml + control-plane handler signatures; duplicated/handcrafted type aliases scattered across components are removed."
    - "Strict TypeScript compliance — zero occurrences of `as any`, `as unknown`, ` as ` cast operators in modified files (per feedback_strict_typescript.md). `tsc --noEmit` passes for the web-console workspace."
    - "Every fix in 13-AUDIT.md fix-list is closed by a corresponding code change AND a Playwright spec assertion that exercises the previously-broken interaction (form submit, table render, filter apply, etc.)."
    - "FX/USD leak: zero occurrences of customer-visible `amount_usd`, `usd_`, `fx_`, `exchange_rate`, or human-readable USD strings ($, USD) in any apps/web-console/ TS/TSX surface — discoveries during audit are recorded as Phase 17 hand-offs in 13-AUDIT.md, BUT any leak inside web-console code itself is fixed in this phase (web-console-only constraint)."
    - "Auth flows green: sign-in, sign-up, forgot-password, reset-password, sign-out, OAuth callback all complete end-to-end against the staging Supabase fixture; a Playwright spec exercises each happy-path."
    - "Owner-gated routes (api-keys/[id]/limits, settings/billing, console/page admin tiles) honour viewer-gates.ts — non-owner workspace member receives read-only or 403 surface, not raw error."
    - "Workspace switcher + invitation accept flow round-trip against control-plane /v1/accounts and /v1/invitations endpoints; switching workspace updates session cookies + nav-shell content."
    - "Full Playwright suite (`npx playwright test`) green in CI mode (`CI=true`) on local docker-compose stack: zero failing specs, zero retries-to-pass on the second-run."
    - "13-VERIFICATION.md records: pre-audit Playwright failure inventory (page count, error class), post-fix Playwright pass run, tsc --noEmit output, FX-leak grep output, total fix count grouped by area, Phase 14/17/18 hand-off list."
    - "13-AUDIT.md exists in the phase folder, lists every console route with status (Green | Broken-P0 | Broken-P1 | Broken-P2 | Phase-14-deferred | Phase-17-deferred), and is committed BEFORE any fix task touches code (audit-first discipline)."
  artifacts:
    - path: ".planning/phases/13-console-integration-fixes/13-AUDIT.md"
      provides: "Per-route inventory with severity, broken-integration class, and fix owner (this phase vs deferred to Phase 14/17/18). Drives Task 2 + 3 fix scope."
      contains: "CONSOLE-13"
    - path: "apps/web-console/lib/control-plane/types.ts"
      provides: "Single source-of-truth TypeScript types for every control-plane request/response consumed by the console. Imported by client.ts + every page that calls the control-plane."
      contains: "export interface"
    - path: "apps/web-console/lib/control-plane/client.ts"
      provides: "Typed fetch wrapper around control-plane endpoints. After Phase 13: zero `any`, zero unsafe casts, request/response shapes derived from types.ts, zero customer-surface `amount_usd` field references."
      contains: "ControlPlaneClient"
    - path: "apps/web-console/tests/e2e/console-dashboard.spec.ts"
      provides: "Playwright regression spec covering /console (root) — owner + non-owner role coverage, all visible widgets render, no console errors."
      contains: "test.describe"
    - path: "apps/web-console/tests/e2e/console-api-keys.spec.ts"
      provides: "Playwright regression spec for /console/api-keys + /console/api-keys/[id]/limits — list, create, revoke, manage-limits owner gate."
      contains: "manage limits"
    - path: "apps/web-console/tests/e2e/console-billing.spec.ts"
      provides: "Playwright regression spec for /console/billing + /console/settings/billing — overview, invoice list/download, ledger, checkout modal open, BDT-only assertion."
      contains: "BDT"
    - path: "apps/web-console/tests/e2e/console-analytics.spec.ts"
      provides: "Playwright regression spec for /console/analytics — usage chart, spend chart, error chart, time-window picker, controls, table render."
      contains: "analytics"
    - path: "apps/web-console/tests/e2e/console-catalog.spec.ts"
      provides: "Playwright regression spec for /console/catalog — model catalog table renders against control-plane catalog response."
      contains: "catalog"
    - path: "apps/web-console/tests/e2e/console-members.spec.ts"
      provides: "Playwright regression spec for /console/members — list, invite (modal), role chips, viewer-gate (non-owner cannot invite)."
      contains: "members"
    - path: "apps/web-console/tests/e2e/console-settings.spec.ts"
      provides: "Playwright regression spec for /console/settings/profile + /console/settings/billing — profile forms, billing contact, business-tax forms submit and persist."
      contains: "settings"
    - path: "apps/web-console/tests/e2e/console-setup.spec.ts"
      provides: "Playwright regression spec for /console/setup — first-run onboarding, profile-completion gate, redirect after complete."
      contains: "setup"
    - path: "apps/web-console/tests/e2e/auth-flows.spec.ts"
      provides: "Playwright regression spec for sign-in, sign-up, forgot-password, reset-password, sign-out, callback — happy-path each."
      contains: "sign-in"
    - path: "apps/web-console/tests/e2e/invitations.spec.ts"
      provides: "Playwright regression spec for /invitations/accept — invite token round-trip, account-switch route ties in."
      contains: "invitation"
    - path: ".planning/phases/13-console-integration-fixes/13-VERIFICATION.md"
      provides: "Closure log for Phase 13: pre/post Playwright run, tsc output, FX grep, fix-count by area, hand-off list."
      contains: "CONSOLE-13"
  key_links:
    - from: "apps/web-console/lib/control-plane/client.ts"
      to: "apps/web-console/lib/control-plane/types.ts"
      via: "import { ... } from './types'"
      pattern: "from ['\"]\\./types['\"]"
    - from: "apps/web-console/lib/control-plane/types.ts"
      to: "packages/openai-contract/generated/hive-openapi.yaml"
      via: "manual sync (or codegen) — every request/response shape mirrors the OpenAPI surface"
      pattern: "openai-contract"
    - from: "apps/web-console/app/console/api-keys/page.tsx"
      to: "apps/web-console/lib/api-keys.ts"
      via: "typed client fns getKeys/createKey/revokeKey"
      pattern: "from ['\"]@/lib/api-keys['\"]"
    - from: "apps/web-console/app/console/billing/page.tsx"
      to: "apps/web-console/lib/control-plane/client.ts"
      via: "ControlPlaneClient.getBilling / listInvoices / getLedger"
      pattern: "ControlPlaneClient"
    - from: "apps/web-console/app/console/analytics/page.tsx"
      to: "apps/control-plane/internal/usage/http.go"
      via: "fetch /v1/usage with workspace + time-window params"
      pattern: "/v1/usage"
    - from: "apps/web-console/app/console/catalog/page.tsx"
      to: "apps/control-plane/internal/catalog/http.go"
      via: "fetch /v1/catalog/models"
      pattern: "/v1/catalog"
    - from: "apps/web-console/app/console/members/page.tsx"
      to: "apps/control-plane/internal/accounts/http.go"
      via: "fetch /v1/accounts/{id}/members + /v1/invitations"
      pattern: "/v1/accounts.*/members|/v1/invitations"
    - from: "apps/web-console/tests/e2e/console-*.spec.ts"
      to: "apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs"
      via: "shared fixture: owner + non-owner JWT, workspace seed"
      pattern: "e2e-auth-fixtures"
    - from: ".planning/phases/13-console-integration-fixes/13-VERIFICATION.md"
      to: ".planning/REQUIREMENTS.md"
      via: "evidence link for CONSOLE-13-01..10 rows"
      pattern: "CONSOLE-13"
---

<objective>
Audit + fix every page under `apps/web-console/app/` against the current
`apps/control-plane/` HTTP surface and the `packages/openai-contract` OpenAPI
contract. Synchronise TypeScript types from one source-of-truth module. Land
a Playwright regression spec for every console route. Close all P0/P1
broken-integration findings discovered during audit; defer P2 cosmetic + scope
overflow into Phase 14/17/18 hand-offs.

Phase 13 is web-console-only — no Go handler changes. Discoveries that require
control-plane work are filed in `13-AUDIT.md` as Phase 14/17/18 blockers, not
fixed here.

This phase unblocks:
- Phase 14 (payments + budget + discretionary credit grant UI rests on a green
  console surface).
- Phase 17 (FX/USD audit needs a stable console to audit; this phase removes
  any web-console-internal leaks and inventories control-plane leaks for
  Phase 17 to fix).
- Phase 18 (RBAC/tier-aware viewer-gates need every console page using the
  shared `viewer-gates.ts` primitive — this phase finishes the migration).
- Track B Phase 20 (Supabase SSO depends on a stable, typed console auth
  surface).

Existing Phase 12 surface reused: `apps/web-console/lib/api-keys.ts` (typed
control-plane client) is the canonical pattern — every other client extension
in this phase mirrors its shape (named exports, typed request + response,
zero unsafe casts).
</objective>

<execution_context>
@/home/sakib/.claude/get-shit-done/workflows/execute-plan.md
@/home/sakib/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/STATE.md
@.planning/REQUIREMENTS.md
@.planning/v1.1-chatapp/V1.1-MASTER-PLAN.md
@.planning/phases/11-verification-cleanup/PLAN.md
@.planning/phases/12-key05-rate-limiting/PLAN.md
@apps/web-console/lib/control-plane/client.ts
@apps/web-console/lib/api-keys.ts
@apps/web-console/lib/viewer-gates.ts
@apps/web-console/lib/profile-schemas.ts
@apps/web-console/playwright.config.ts
@apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs
@apps/web-console/tests/e2e/openai-sdk.spec.ts
@apps/web-console/tests/e2e/auth-shell.spec.ts
@apps/web-console/tests/e2e/profile-completion.spec.ts
@apps/control-plane/internal/accounts/http.go
@apps/control-plane/internal/apikeys/http.go
@apps/control-plane/internal/payments/http.go
@apps/control-plane/internal/usage/http.go
@apps/control-plane/internal/catalog/http.go
@apps/control-plane/internal/routing/http.go
@apps/control-plane/internal/budgets/http.go
@packages/openai-contract/generated/hive-openapi.yaml

<console_route_inventory>
<!-- Discovered 2026-04-25 via `find apps/web-console/app -type f`. Every entry MUST appear in 13-AUDIT.md with a status. -->

Auth surface (6):
  app/auth/sign-in/page.tsx
  app/auth/sign-up/page.tsx
  app/auth/forgot-password/page.tsx
  app/auth/reset-password/page.tsx
  app/auth/callback/route.ts
  app/auth/sign-out/route.ts

Console surface (12):
  app/console/page.tsx                          (dashboard root)
  app/console/layout.tsx                        (shell — exercised by every nested page)
  app/console/setup/page.tsx                    (first-run onboarding)
  app/console/api-keys/page.tsx                 (list)
  app/console/api-keys/[id]/limits/page.tsx     (Phase 12 owner UI)
  app/console/billing/page.tsx                  (billing overview)
  app/console/settings/billing/page.tsx         (billing contact / tax)
  app/console/settings/profile/page.tsx         (account profile)
  app/console/analytics/page.tsx                (usage + spend + error charts)
  app/console/catalog/page.tsx                  (model catalog)
  app/console/members/page.tsx                  (workspace members + invitations)
  app/console/account-switch/route.ts           (workspace switcher action)

Public + invitations (3):
  app/page.tsx                                  (root marketing / redirect)
  app/invitations/accept/page.tsx
  app/api/budget/route.ts                       (BFF route — proxy or direct?)

Total: 21 routes (the layout.tsx counts implicitly via every nested page).
</console_route_inventory>

<known_breakage_signals>
<!-- Pre-audit signals collected during planning. NOT exhaustive — Task 1 produces the full inventory. -->

1. **FX/USD leak in console:** `grep -lr 'amount_usd\|usd_\|fx_'` reports
   `apps/web-console/lib/control-plane/client.ts` contains USD-bearing fields.
   Any customer-visible field MUST be removed in this phase (regulatory).
   Internal-only USD persistence stays in control-plane (Phase 17 scope).
2. **Strict TS violations likely:** `lib/control-plane/client.ts` is 1533
   lines — high probability of `any`/`unknown`/`as` casts. Audit MUST grep
   strict-type violations across `apps/web-console/`.
3. **Type duplication:** Pages may hand-roll request/response interfaces
   instead of importing from a central module. Audit MUST detect.
4. **Recent Phase 12 baseline:** `lib/api-keys.ts` (147 lines, recent — see
   PR #133 / commit `e1c6090`) is the canonical typed-client shape. Every
   other client extension mirrors it.
5. **E2E fixture pattern:** `tests/e2e/support/e2e-auth-fixtures.mjs` provides
   service-role-fallback auth. New specs MUST use this — do not roll fresh
   fixtures.
6. **Workers serial:** `playwright.config.ts` sets `workers: 1` — the
   Supabase fixture reset races otherwise. New specs respect serial-by-default.
</known_breakage_signals>

<deferred_to_other_phases>
<!-- Findings that surface during audit but DO NOT belong to Phase 13. -->

- Control-plane Go handler bugs → file in 13-AUDIT.md "Phase 14/17/18 hand-off" section. Do NOT modify Go.
- chat-app surfaces → ignored entirely; Track B Phase 25 owns chat-app FX audit.
- Control-plane USD/FX response leaks → Phase 17 (this phase only fixes web-console-side strings + flags control-plane leaks).
- Discretionary credit-grant UI → Phase 14 (NOT this phase; even if broken Credit-related links are found, log and defer).
- Tier-aware UI gating beyond owner/non-owner → Phase 18 (this phase honours the existing viewer-gates two-role model only).
</deferred_to_other_phases>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Audit — full Playwright crawl + manual click-through inventory; output 13-AUDIT.md with per-route severity + fix scope</name>
  <files>
    .planning/phases/13-console-integration-fixes/13-AUDIT.md
  </files>
  <action>
    Audit-first discipline: NO source code modified in this task. Output is the
    inventory document that drives Tasks 2 + 3.

    1. Stand up the local stack (per CLAUDE.md):
       ```
       cd /home/sakib/hive/deploy/docker
       docker compose --env-file ../../.env --profile local up --build -d
       ```
       Wait for healthchecks: edge-api, control-plane, web-console all green.

    2. Run the existing Playwright suite as the baseline failure inventory:
       ```
       cd /home/sakib/hive/apps/web-console
       CI=true npx playwright test --reporter=json > /tmp/13-baseline.json 2>&1 || true
       ```
       Parse `/tmp/13-baseline.json` to extract per-spec pass/fail. Do NOT
       fix anything here — record only.

    3. Crawl every route in <console_route_inventory> with a one-shot
       Playwright script (`tests/e2e/_audit/crawl.spec.ts` — temp, deleted at
       end of task). For each route, log:
       - HTTP status of any control-plane fetch (4xx/5xx = broken)
       - `console.error` events captured via `page.on('console', ...)`
       - Network failures via `page.on('requestfailed', ...)`
       - First-paint render error (React error boundary text)

    4. Run strict-TS + lint sweep across `apps/web-console/`:
       ```
       cd /home/sakib/hive/deploy/docker
       docker compose run --rm web-console npx tsc --noEmit 2>&1 | tee /tmp/13-tsc.log
       docker compose run --rm web-console grep -RnE '\bas (any|unknown)\b|: any\b|: unknown\b' app/ components/ lib/ 2>&1 | tee /tmp/13-strict.log
       ```

    5. Grep customer-surface FX/USD leaks across apps/web-console:
       ```
       grep -RnE 'amount_usd|usd_|fx_|exchange_rate|\\$[0-9]|\bUSD\b' \
         /home/sakib/hive/apps/web-console/app \
         /home/sakib/hive/apps/web-console/components \
         /home/sakib/hive/apps/web-console/lib \
         > /tmp/13-fx.log 2>&1 || true
       ```

    6. Cross-reference each console route against the canonical
       control-plane handler signature (handlers in
       `apps/control-plane/internal/{accounts,apikeys,payments,usage,catalog,routing,budgets}/http.go`)
       and the OpenAPI surface in
       `packages/openai-contract/generated/hive-openapi.yaml`. Record any
       request shape, response shape, or path mismatch.

    7. Produce `.planning/phases/13-console-integration-fixes/13-AUDIT.md`
       with these required sections:

       **Section A — Route inventory table** (one row per route in
       <console_route_inventory>):

       | Route | Status | Severity | Failure class | Fix owner |
       |-------|--------|----------|---------------|-----------|
       | /console/page.tsx | Broken | P0 | 4xx on /v1/accounts/current | Phase 13 Task 2 |
       | ... | ... | ... | ... | ... |

       Statuses: `Green` | `Broken-P0` | `Broken-P1` | `Broken-P2` |
       `Phase-14-deferred` | `Phase-17-deferred` | `Phase-18-deferred`.

       Failure classes: `4xx-response` | `5xx-response` | `type-mismatch` |
       `console-error` | `render-error` | `fx-leak` | `strict-ts-violation` |
       `missing-spec` | `network-failure` | `auth-fixture` | `none`.

       **Section B — Type-sync gaps:** list every place a request/response
       type is hand-rolled instead of imported from a (planned) central
       `lib/control-plane/types.ts`.

       **Section C — Strict-TS violations:** count + file list of `any`,
       `unknown`, `as ` casts in `apps/web-console/`.

       **Section D — FX/USD findings:** every customer-surface leak,
       split into "Fix in Phase 13" (web-console string/component) vs
       "Hand off to Phase 17" (control-plane response field).

       **Section E — Phase 14/17/18 hand-offs:** discoveries that require
       work outside web-console; each entry names the target phase + a
       one-line breakage summary. Phase 14/17/18 plans pick these up.

       **Section F — Fix-list (Phase 13 in-scope only):** numbered list of
       discrete fixes to be implemented in Tasks 2 + 3. Each fix entry has
       id (FIX-13-NN), affected files, fix description, target spec
       (which Playwright spec asserts the fix). This list is the contract
       the next two tasks execute against.

       **Section G — Spec coverage map:** for every console route, the
       Playwright spec file that will cover it post-Phase-13 (one of the
       11 spec files declared in `files_modified` frontmatter).

    8. Tear down the temp `_audit/crawl.spec.ts`, leave only `13-AUDIT.md`.

    Constraint: 13-AUDIT.md MUST be committed BEFORE Tasks 2 + 3 begin —
    audit-first discipline (avoids drift between audit findings and fix
    scope). Conventional commit:
    `docs(13): audit web-console integration vs control-plane`.

    Constraint: NO source code under apps/ modified by this task. Pure
    discovery + documentation.

    Constraint: per `feedback_no_human_verification.md`, every signal is
    captured via Playwright/grep/tsc/curl — never "open browser and check".
  </action>
  <verify>
    <automated>test -f /home/sakib/hive/.planning/phases/13-console-integration-fixes/13-AUDIT.md &amp;&amp; grep -q 'Section F' /home/sakib/hive/.planning/phases/13-console-integration-fixes/13-AUDIT.md &amp;&amp; grep -qE 'FIX-13-[0-9]+' /home/sakib/hive/.planning/phases/13-console-integration-fixes/13-AUDIT.md &amp;&amp; grep -qE '/console/page\.tsx|/console/billing|/console/analytics|/console/api-keys|/console/catalog|/console/members|/console/setup|/console/settings|/auth/sign-in|/invitations/accept' /home/sakib/hive/.planning/phases/13-console-integration-fixes/13-AUDIT.md &amp;&amp; cd /home/sakib/hive &amp;&amp; git diff --quiet -- apps/ &amp;&amp; git diff --quiet --cached -- apps/</automated>
  </verify>
  <done>
    13-AUDIT.md exists, lists every route from &lt;console_route_inventory&gt; with
    status + severity + failure class + fix owner, contains Sections A-G,
    enumerates a numbered FIX-13-NN list and a per-route spec coverage map,
    AND no production source under apps/ has been modified during Task 1
    (audit-only). Committed as a single doc-only commit.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Type sync + strict-TS cleanup + P0/P1 integration fixes (every fix from 13-AUDIT.md Section F)</name>
  <files>
    apps/web-console/lib/control-plane/types.ts,
    apps/web-console/lib/control-plane/client.ts,
    apps/web-console/lib/api-keys.ts,
    apps/web-console/lib/viewer-gates.ts,
    apps/web-console/lib/profile-schemas.ts,
    apps/web-console/lib/format/credits.ts,
    apps/web-console/app/console/page.tsx,
    apps/web-console/app/console/layout.tsx,
    apps/web-console/app/console/setup/page.tsx,
    apps/web-console/app/console/analytics/page.tsx,
    apps/web-console/app/console/api-keys/page.tsx,
    apps/web-console/app/console/api-keys/[id]/limits/page.tsx,
    apps/web-console/app/console/billing/page.tsx,
    apps/web-console/app/console/catalog/page.tsx,
    apps/web-console/app/console/members/page.tsx,
    apps/web-console/app/console/settings/billing/page.tsx,
    apps/web-console/app/console/settings/profile/page.tsx,
    apps/web-console/app/console/account-switch/route.ts,
    apps/web-console/app/api/budget/route.ts,
    apps/web-console/app/auth/sign-in/page.tsx,
    apps/web-console/app/auth/sign-up/page.tsx,
    apps/web-console/app/auth/forgot-password/page.tsx,
    apps/web-console/app/auth/reset-password/page.tsx,
    apps/web-console/app/auth/callback/route.ts,
    apps/web-console/app/auth/sign-out/route.ts,
    apps/web-console/app/invitations/accept/page.tsx,
    apps/web-console/components/billing/billing-overview.tsx,
    apps/web-console/components/billing/checkout-modal.tsx,
    apps/web-console/components/billing/invoice-list.tsx,
    apps/web-console/components/billing/invoice-download-button.tsx,
    apps/web-console/components/billing/ledger-table.tsx,
    apps/web-console/components/billing/ledger-csv-export.tsx,
    apps/web-console/components/billing/budget-alert-form.tsx,
    apps/web-console/components/billing/budget-alert-banner.tsx,
    apps/web-console/components/analytics/analytics-controls.tsx,
    apps/web-console/components/analytics/analytics-table.tsx,
    apps/web-console/components/analytics/spend-chart.tsx,
    apps/web-console/components/analytics/error-chart.tsx,
    apps/web-console/components/analytics/usage-chart.tsx,
    apps/web-console/components/analytics/time-window-picker.tsx,
    apps/web-console/components/api-keys/api-key-list.tsx,
    apps/web-console/components/api-keys/api-key-create-form.tsx,
    apps/web-console/components/api-keys/revoke-confirm-panel.tsx,
    apps/web-console/components/api-keys/rate-limit-form.tsx,
    apps/web-console/components/catalog/model-catalog-table.tsx,
    apps/web-console/components/profile/account-profile-form.tsx,
    apps/web-console/components/profile/billing-contact-form.tsx,
    apps/web-console/components/profile/business-tax-form.tsx,
    apps/web-console/components/email-settings-card.tsx,
    apps/web-console/components/verification-banner.tsx,
    apps/web-console/components/workspace-switcher.tsx,
    apps/web-console/components/nav-shell.tsx,
    apps/web-console/components/app-shell/auth-shell.tsx,
    apps/web-console/components/app-shell/console-shell.tsx
  </files>
  <behavior>
    Type-sync + fix-list execution. Each FIX-13-NN id from 13-AUDIT.md Section F
    must have a corresponding code change AND a unit-test or component-test
    assertion (where applicable — pure routing/layout fixes are validated by
    Task 3 Playwright specs only).

    - `lib/control-plane/types.ts` exports one interface per control-plane
      request/response shape consumed by the console:
      AccountSummary, MemberRecord, InvitationRecord, ApiKeyRecord,
      ApiKeyLimits, BillingOverview, InvoiceRecord, LedgerEntry,
      BudgetSettings, BudgetAlert, UsageRow, UsageWindow, CatalogModel,
      RoutingProfile, ProfileRecord, BillingContact, BusinessTaxRecord,
      WorkspaceSwitchPayload, OAuthCallbackPayload, ResetPasswordPayload.
      Names mirror control-plane Go types under
      `apps/control-plane/internal/{accounts,apikeys,payments,usage,catalog,routing,budgets,profiles}/types.go`.
      Source-of-truth ordering: control-plane Go struct -> openai-contract
      OpenAPI -> ts interface. Discrepancies between Go + OpenAPI flagged
      as Phase 17 hand-offs (NOT fixed here).

    - `lib/control-plane/client.ts` refactored:
      a) zero `any`, `unknown`, ` as ` (strict TS),
      b) every fetch call typed via `<Req, Res>` generic,
      c) zero customer-surface `amount_usd`/`usd_*`/`fx_*` fields exposed
         to consumers — internal accounting fields are dropped at the client
         boundary (omit on response parse) with a comment citing Phase 17.

    - Every page + component listed in <files> imports types from
      `lib/control-plane/types.ts` and the typed `client.ts` (or the
      Phase-12 `lib/api-keys.ts` pattern for api-keys surface). Hand-rolled
      type aliases removed.

    - `viewer-gates.ts` honoured on owner-only routes (api-keys/[id]/limits,
      settings/billing, members invite, console root admin tiles). Non-owner
      sees disabled controls or 403 — not raw error. (Tier-aware extension
      stays Phase 18.)

    - Each FIX-13-NN id closed with a vitest unit/component test (where
      page logic is testable) OR a TODO referencing the Task-3 Playwright
      spec that asserts the fix. Tests live alongside the component
      (`*.test.tsx`) per existing convention (see Phase 12
      `rate-limit-form.test.tsx`).

    - Auth flows: sign-in/up/forgot/reset/sign-out/callback all use the
      Supabase server + browser clients in `lib/supabase/`. No raw `fetch`
      to Supabase from page modules. callback/route.ts handles error states
      explicitly (no silent swallow).

    - `app/api/budget/route.ts` BFF: typed request/response, calls
      control-plane via `client.ts`, no direct DB access.

    Test cases (RED first for each unit test added):
    - `lib/format/credits.ts` test: BDT-only formatter — passing a USD
      input throws (regulatory guardrail) OR returns a sentinel (decided
      during impl, documented in 13-AUDIT.md fix entry).
    - `viewer-gates.ts` test: owner role unlocks owner-only feature key;
      member role does not.
    - Each component fix gets a render-test: given typed props, renders
      without console.error and without unsafe cast warnings.
  </behavior>
  <action>
    1. Create `lib/control-plane/types.ts`:
       - For each control-plane handler under
         `apps/control-plane/internal/*/http.go`, mirror request body +
         response struct as a TypeScript interface. Field names match
         the JSON tag, NOT the Go field name.
       - For OpenAI-compat surfaces (catalog model shape, etc.) cross-check
         `packages/openai-contract/generated/hive-openapi.yaml`.
       - Strict types only: numeric ids = `string` (control-plane uses
         UUIDv7); timestamps = `string` (ISO-8601); enums = string union
         (e.g. `'owner' | 'admin' | 'member'`).
       - NO `amount_usd` / `usd_*` / `fx_*` fields on customer-surface
         types. Internal admin-only types may include them, but ONLY if
         the consuming page is owner-gated and labeled with a Phase 17
         hand-off comment.

    2. Refactor `lib/control-plane/client.ts`:
       - Walk top-to-bottom, replacing every `any`/`unknown`/`as` with
         a typed shape from `types.ts`.
       - Drop customer-facing USD fields at the client boundary.
       - Provide one named export per endpoint: `getBillingOverview`,
         `listInvoices`, `getInvoicePdf`, `listLedger`, `exportLedgerCsv`,
         `getBudget`, `updateBudget`, `listBudgetAlerts`, `createBudgetAlert`,
         `getUsage`, `getCatalog`, `listMembers`, `inviteMember`,
         `acceptInvitation`, `switchAccount`, `getProfile`, `updateProfile`,
         `getBillingContact`, `updateBillingContact`, `getBusinessTax`,
         `updateBusinessTax`, `getRoutingProfile`.
       - For api-keys surface, REUSE `lib/api-keys.ts` named exports —
         do not duplicate.

    3. Walk each page + component in <files>:
       - Replace hand-rolled types with `types.ts` imports.
       - Replace direct `fetch` with typed client functions.
       - Apply each FIX-13-NN from 13-AUDIT.md Section F. Each fix is a
         self-contained edit; commit messages map 1:1 to FIX ids:
         `fix(13): FIX-13-04 — type-sync invoice-list table cells`.

    4. For each component listed with logic worth unit-testing
       (forms, viewer-gates, format/credits, profile-schemas), write
       a `*.test.tsx` or `*.test.ts` using vitest + react-testing-library
       (existing pattern from `checkout-modal.test.tsx`,
       `rate-limit-form.test.tsx`).

    5. Run continuously during impl:
       ```
       cd /home/sakib/hive/deploy/docker
       docker compose run --rm web-console npx tsc --noEmit
       docker compose run --rm web-console npm run test:unit
       ```
       Both must exit 0 before Task 2 completes.

    6. Final FX/USD grep — must return ZERO matches across customer-facing
       surfaces in apps/web-console/:
       ```
       grep -RnE 'amount_usd|usd_|fx_|exchange_rate' \
         /home/sakib/hive/apps/web-console/app \
         /home/sakib/hive/apps/web-console/components \
         /home/sakib/hive/apps/web-console/lib
       ```
       Internal admin/owner-gated surfaces may retain USD only if marked
       with a `// PHASE-17-OWNER-ONLY` comment. Audit log entry in
       13-VERIFICATION.md (Task 3 produces).

    Constraint: NO control-plane Go changes. If a fix demands a Go change
    (e.g. response field rename), abandon that fix in this task and append
    it to 13-AUDIT.md Section E (Phase 14/17/18 hand-off). Do NOT silently
    work around with a cast.

    Constraint: feedback_strict_typescript.md — zero `as any`, zero `as
    unknown`, zero ` as ` cast operators in modified files. Build a
    structurally valid object instead. tsc --noEmit MUST pass.

    Constraint: feedback_branching.md — work happens on
    `a/phase-13-console-integration-fixes` branch; do NOT commit to main.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose run --rm web-console npx tsc --noEmit &amp;&amp; docker compose run --rm web-console npm run test:unit &amp;&amp; docker compose run --rm web-console npm run build &amp;&amp; ! grep -RnE 'amount_usd|\busd_|\bfx_|exchange_rate' /home/sakib/hive/apps/web-console/app /home/sakib/hive/apps/web-console/components /home/sakib/hive/apps/web-console/lib 2&gt;/dev/null | grep -v 'PHASE-17-OWNER-ONLY' &amp;&amp; ! grep -RnE '\bas (any|unknown)\b|: any\b' /home/sakib/hive/apps/web-console/app /home/sakib/hive/apps/web-console/components /home/sakib/hive/apps/web-console/lib 2&gt;/dev/null</automated>
  </verify>
  <done>
    `lib/control-plane/types.ts` exports the canonical interface set; every
    page + component in &lt;files&gt; imports from it; `client.ts` is strict-TS
    clean; `tsc --noEmit` exits 0; vitest suite exits 0; `npm run build`
    exits 0; FX/USD grep returns zero customer-surface matches; strict-TS
    grep returns zero matches. Every FIX-13-NN id from 13-AUDIT.md Section F
    closed (verified by 1:1 commit-message mapping).
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Playwright regression suite for every console route + 13-VERIFICATION.md + REQUIREMENTS.md update</name>
  <files>
    apps/web-console/tests/e2e/console-dashboard.spec.ts,
    apps/web-console/tests/e2e/console-api-keys.spec.ts,
    apps/web-console/tests/e2e/console-billing.spec.ts,
    apps/web-console/tests/e2e/console-analytics.spec.ts,
    apps/web-console/tests/e2e/console-catalog.spec.ts,
    apps/web-console/tests/e2e/console-members.spec.ts,
    apps/web-console/tests/e2e/console-settings.spec.ts,
    apps/web-console/tests/e2e/console-setup.spec.ts,
    apps/web-console/tests/e2e/auth-flows.spec.ts,
    apps/web-console/tests/e2e/invitations.spec.ts,
    .planning/phases/13-console-integration-fixes/13-VERIFICATION.md,
    .planning/REQUIREMENTS.md
  </files>
  <behavior>
    Every route in <console_route_inventory> covered by a spec, every
    FIX-13-NN id asserted by at least one spec assertion (per the
    13-AUDIT.md Section G coverage map). Suite runs green via
    `CI=true npx playwright test` on the local docker-compose stack on
    the FIRST run (no flake-retries).

    Per-spec coverage:
    - console-dashboard.spec.ts: /console renders for owner + non-owner;
      no console.error; admin tiles gated correctly; navigation to
      sub-routes works.
    - console-api-keys.spec.ts: list renders; create flow opens form +
      submits + lists new key; revoke confirm panel; "Manage limits"
      link routes to `/console/api-keys/[id]/limits`; Phase-12 limits
      form renders editable for owner / disabled for non-owner.
    - console-billing.spec.ts: /console/billing overview shows BDT
      values only (assertion: NO `$` or `USD` substrings); invoice list
      renders; download click yields a non-empty PDF or 200 redirect;
      ledger table + CSV export round-trip; checkout modal opens with
      BDT amount, no FX strings; budget-alert banner visible if alert
      hot.
    - console-analytics.spec.ts: charts (usage/spend/error) render with
      mock data; time-window picker changes URL query + refetches;
      analytics-controls filter applies; analytics-table sorts.
    - console-catalog.spec.ts: catalog table renders model rows; column
      shape matches CatalogModel from types.ts.
    - console-members.spec.ts: list renders; invite modal owner-only;
      role chips render; invitations table renders.
    - console-settings.spec.ts: profile form submit persists; billing
      contact submit persists; business-tax form submit persists; all
      forms validate via profile-schemas.ts.
    - console-setup.spec.ts: first-run onboarding redirects to
      /console after profile completion; profile-completion gate honoured.
    - auth-flows.spec.ts: sign-in valid creds -> /console; invalid creds
      stay + show error; sign-up + email verification path; forgot ->
      reset round-trip via Supabase fixture; sign-out clears cookie +
      redirects to /auth/sign-in.
    - invitations.spec.ts: /invitations/accept with valid token ->
      account-switch -> /console; expired token -> error surface.

    Each spec uses `tests/e2e/support/e2e-auth-fixtures.mjs` for owner +
    non-owner JWT setup. New role helpers added in support/ if needed
    (do NOT roll fresh fixture infra).
  </behavior>
  <action>
    1. For each spec file, write the test cases listed in <behavior>. RED
       first — confirm the spec FAILS against the pre-Task-2 baseline if
       any FIX-13-NN coverage is incomplete (commit log evidence).

    2. Reuse `e2e-auth-fixtures.mjs` for owner / non-owner / unauth roles.
       If a spec needs a fresh role (e.g. invited-user-pre-accept), extend
       the fixtures file rather than rolling a new one.

    3. Respect `playwright.config.ts` `workers: 1` — fixtures race otherwise.

    4. For BDT-only assertions, use a regex: `await
       expect(page.locator('body')).not.toContainText(/\$|USD\b|amount_usd|fx_/);`
       on every billing/analytics/checkout surface.

    5. Run the full suite locally:
       ```
       cd /home/sakib/hive/apps/web-console
       CI=true npx playwright test --reporter=list 2>&1 | tee /tmp/13-final-playwright.log
       ```
       Must exit 0 with zero retries. If any spec needs `test.fixme` or
       `test.skip`, that constitutes a Phase 13 failure — the underlying
       fix belongs in Task 2 (loop back, do not skip).

    6. Produce `.planning/phases/13-console-integration-fixes/13-VERIFICATION.md`
       with sections (mirrors Phase 11/12 verification format):

       - **Pre-fix baseline:** failing-spec count from
         `/tmp/13-baseline.json` (Task 1 capture).
       - **Post-fix Playwright run:** stdout summary from
         `/tmp/13-final-playwright.log` — total / passed / failed / flaky.
         Failed + flaky MUST be zero.
       - **tsc --noEmit:** exit code + output.
       - **vitest unit run:** exit code + output.
       - **`npm run build`:** exit code.
       - **FX/USD grep:** command + output (must be empty modulo
         PHASE-17-OWNER-ONLY).
       - **Strict-TS grep:** command + output (must be empty).
       - **Fix count by area:** auth / api-keys / billing / analytics /
         catalog / members / settings / setup / invitations / shared
         (lib / components/ui).
       - **Per-route status table:** mirrors 13-AUDIT.md Section A
         post-fix — every entry now `Green` or explicitly
         `Phase-14-deferred` / `Phase-17-deferred` / `Phase-18-deferred`.
       - **Hand-off list:** Phase 14/17/18 carry-forward items extracted
         from 13-AUDIT.md Section E (one-line each, target phase named).
       - **CONSOLE-13-01..10 evidence:** for each requirement row,
         truth + command + output snippet that satisfies it.
       - **Branch + ship-gate:** branch `a/phase-13-console-integration-fixes`
         status; PR link placeholder.

    7. Update `.planning/REQUIREMENTS.md`:
       - Add CONSOLE-13-01..10 rows under a new "Console Integration"
         section (or extend the existing console section if Phase 11
         created one). Mapping:
         - CONSOLE-13-01: every console route reachable + green
         - CONSOLE-13-02: typed source-of-truth in lib/control-plane/types.ts
         - CONSOLE-13-03: client.ts strict-TS clean, no unsafe casts
         - CONSOLE-13-04: zero customer-surface FX/USD leak in web-console
         - CONSOLE-13-05: viewer-gates honoured on owner-only routes
         - CONSOLE-13-06: auth flows green (sign-in/up/forgot/reset/out/cb)
         - CONSOLE-13-07: workspace switch + invitation accept round-trip
         - CONSOLE-13-08: Playwright regression spec for every route
         - CONSOLE-13-09: tsc --noEmit + npm run build exit 0
         - CONSOLE-13-10: Phase 14/17/18 hand-off list filed
       - Each row: Status `Satisfied`, Evidence `[13-VERIFICATION.md](phases/13-console-integration-fixes/13-VERIFICATION.md)`.

    8. Run validator:
       ```
       bash /home/sakib/hive/scripts/verify-requirements-matrix.sh
       ```
       Must exit 0.

    Constraint: NO `test.skip` / `test.fixme` in landed specs. If a route
    cannot be made green in this phase, its row in the per-route status
    table is `Phase-14-deferred` / `Phase-17-deferred` / `Phase-18-deferred`
    AND there is NO spec for that route in this PR (the spec lands in
    that downstream phase). The route inventory + AUDIT must justify the
    deferral with a one-line reason.

    Constraint: feedback_no_human_verification.md — every check
    autonomous via Playwright/tsc/grep/curl. No "open browser to verify".

    Constraint: feedback_local_first.md — verify locally before pushing.
    The CI run is a regression safety-net, not the primary signal.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/apps/web-console &amp;&amp; CI=true npx playwright test --reporter=list &amp;&amp; cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose run --rm web-console npx tsc --noEmit &amp;&amp; docker compose run --rm web-console npm run build &amp;&amp; bash /home/sakib/hive/scripts/verify-requirements-matrix.sh &amp;&amp; test -f /home/sakib/hive/.planning/phases/13-console-integration-fixes/13-VERIFICATION.md &amp;&amp; grep -q 'CONSOLE-13-01' /home/sakib/hive/.planning/REQUIREMENTS.md &amp;&amp; grep -q 'CONSOLE-13-10' /home/sakib/hive/.planning/REQUIREMENTS.md &amp;&amp; grep -q 'Post-fix Playwright run' /home/sakib/hive/.planning/phases/13-console-integration-fixes/13-VERIFICATION.md &amp;&amp; grep -q 'Hand-off list' /home/sakib/hive/.planning/phases/13-console-integration-fixes/13-VERIFICATION.md</automated>
  </verify>
  <done>
    Ten Playwright spec files exist, each covering its declared route set
    per behavior; full suite passes in CI mode with zero retries; tsc
    --noEmit + npm run build exit 0; 13-VERIFICATION.md records all
    sections including post-fix Playwright run + hand-off list;
    REQUIREMENTS.md CONSOLE-13-01..10 rows are Satisfied with evidence
    link; verify-requirements-matrix.sh exits 0; zero customer-surface
    FX/USD leaks remain in apps/web-console/.
  </done>
</task>

</tasks>

<verification>
Phase-level checks (run after all tasks complete):

1. Audit committed first: `git log --diff-filter=A
   .planning/phases/13-console-integration-fixes/13-AUDIT.md` precedes
   commits touching `apps/web-console/` for Phase 13.
2. Type-sync: `grep -l "from '@/lib/control-plane/types'"
   apps/web-console/app/ apps/web-console/components/ -r | wc -l` ≥ 15
   (every page + major component imports from the new module).
3. Strict TS: `grep -RnE '\bas (any|unknown)\b|: any\b'
   apps/web-console/{app,components,lib}` returns no matches.
4. FX/USD: `grep -RnE 'amount_usd|usd_|fx_|exchange_rate'
   apps/web-console/{app,components,lib} | grep -v PHASE-17-OWNER-ONLY`
   returns no matches.
5. Build green: `cd deploy/docker && docker compose run --rm web-console
   npm run build` exits 0.
6. tsc green: same harness, `npx tsc --noEmit` exits 0.
7. Unit tests: `npm run test:unit` exits 0.
8. Playwright: `cd apps/web-console && CI=true npx playwright test`
   exits 0 with zero failed + zero flaky.
9. Spec coverage: every route in <console_route_inventory> appears in
   exactly one spec file (audit grep confirms).
10. Owner gate: console-api-keys.spec.ts asserts non-owner sees the
    Phase-12 limits form in disabled state.
11. BDT-only: console-billing.spec.ts asserts no `$` or `USD` strings
    on every billing surface.
12. Hand-off: 13-VERIFICATION.md "Hand-off list" lists every Phase-14
    / Phase-17 / Phase-18 carry-forward extracted during audit.
13. Requirements: `bash scripts/verify-requirements-matrix.sh` exits 0;
    CONSOLE-13-01..10 are Satisfied.
14. Branch: `git rev-parse --abbrev-ref HEAD` returns
    `a/phase-13-console-integration-fixes`.
</verification>

<success_criteria>
Definition of Done — also serves as v1.1.0 ship-gate input for the
console portion:

- [ ] 13-AUDIT.md committed BEFORE any apps/ fix commit; covers all 21
      routes; lists FIX-13-NN IDs + Phase 14/17/18 hand-offs.
- [ ] `apps/web-console/lib/control-plane/types.ts` is the single source
      of TS types for control-plane request/response shapes; every page
      + relevant component imports from it.
- [ ] `client.ts` refactored: zero `any`/`unknown`/`as ` casts; every
      endpoint typed `<Req, Res>`; customer-surface USD/FX fields
      stripped at client boundary.
- [ ] Every page + component in <files> updated per 13-AUDIT.md Section F
      fix-list; vitest unit/component tests cover testable logic.
- [ ] viewer-gates.ts honoured on owner-only routes; non-owner sees
      disabled / 403 surface (read-only mode for limits page).
- [ ] Auth flows (sign-in/up/forgot/reset/out/callback) green via
      Playwright; workspace switch + invitation accept round-trip green.
- [ ] 10 Playwright spec files cover all 21 routes; full suite passes
      `CI=true npx playwright test` with zero failed + zero flaky.
- [ ] `npx tsc --noEmit` + `npm run build` + `npm run test:unit` exit 0.
- [ ] FX/USD grep across apps/web-console/ returns zero customer-surface
      matches (PHASE-17-OWNER-ONLY annotated remnants only).
- [ ] 13-VERIFICATION.md records pre + post Playwright runs, tsc/build
      output, FX grep, fix count by area, per-route status, hand-off
      list, CONSOLE-13-01..10 evidence.
- [ ] `.planning/REQUIREMENTS.md` CONSOLE-13-01..10 Satisfied with
      evidence link to 13-VERIFICATION.md;
      scripts/verify-requirements-matrix.sh exits 0.
- [ ] Branch `a/phase-13-console-integration-fixes` created; single PR
      opened against main per V1.1-MASTER-PLAN.md branching strategy.
- [ ] Zero control-plane Go changes (web-console-only constraint).
- [ ] Zero chat-app changes (Track A only).

Ship-gate mapping: closes Phase 13 master-plan item; unblocks Phase 14
(payments + budget + discretionary credit grant UI), Phase 17 (FX audit
inherits the inventory), Phase 18 (RBAC matrix can extend the now-clean
viewer-gates surface), Track B Phase 20 (Supabase SSO inherits a stable,
typed auth surface).
</success_criteria>

<blockers>
Discovered during planning (2026-04-25):

1. **Console route count is 21, not "every page" abstract.** The route
   inventory above is exhaustive as of HEAD; Task 1 audit confirms +
   updates if HEAD drifts. Larger than typical phase scope — mitigated
   by audit-first task split (Task 1 inventory locks scope before Tasks
   2 + 3 execute).

2. **`lib/control-plane/client.ts` is 1533 lines.** Likely contains
   significant refactor risk: hand-rolled types, casts, possibly USD
   fields. Plan budgets a full pass in Task 2; if scope explodes during
   impl, executor MUST stop, append findings to 13-AUDIT.md, and split
   client.ts refactor into a follow-up phase rather than partial-state
   commits.

3. **FX/USD leak confirmed pre-audit:** `lib/control-plane/client.ts`
   contains USD-bearing field references. This is a Phase 17 risk
   surfaced inside Phase 13. Decision: web-console-internal removal
   happens in this phase (regulatory urgency); control-plane response
   shape change is filed as Phase 17 hand-off, not fixed here.

4. **Phase 12 dependency:** `lib/api-keys.ts` is the Phase-12 canonical
   pattern. Phase 12 ships in the same v1.1 cycle — Phase 13 starts
   ONLY after Phase 12's PR merges, so the api-keys typed-client base
   is stable. depends_on: [11, 12] enforces this.

5. **Playwright fixture serial execution:** `playwright.config.ts` has
   `workers: 1` because Supabase fixture reset races. New specs
   inherit; do NOT change worker count.

6. **`scripts/verify-requirements-matrix.sh` may not yet have a CONSOLE
   section:** Phase 11 may have shipped only v1.0 + KEY-05 rows. Task
   3 ADDS CONSOLE-13-01..10 rows if absent; updates if present. The
   validator only checks frontmatter shape on linked evidence files —
   it does not enforce row IDs against any external manifest.

7. **No human-verification path:** per `feedback_no_human_verification.md`,
   every check is Playwright/curl/tsc/grep. The "manual click-through"
   in Task 1 is automated via a temp Playwright crawl spec, not a
   human session.

8. **Local-first signal:** per `feedback_local_first.md`, all checks
   run against local docker-compose stack first. Push to PR ONLY after
   local green. CI is regression safety-net, not primary signal.

9. **Branch discipline:** per `feedback_no_direct_main_push.md`, work
   on `a/phase-13-console-integration-fixes`; PR to main, never push
   main directly. Frontmatter `branch:` field locked.

10. **Out-of-scope discoveries:** if audit reveals chat-app FX leaks,
    control-plane Go bugs, or RBAC gaps, they become Phase 14/17/18/25
    hand-offs in 13-AUDIT.md Section E. NOT fixed here.
</blockers>

<output>
After completion, create `.planning/phases/13-console-integration-fixes/13-01-SUMMARY.md`
per the GSD summary template, recording:
- Files created (types.ts, 10 Playwright specs, AUDIT, VERIFICATION)
- Files modified (count by area: auth / console / components / lib)
- Pre-fix Playwright failure count vs post-fix (target zero failed)
- FIX-13-NN IDs closed (full list)
- FX/USD grep result (must be zero customer-surface)
- Strict-TS violation delta (pre vs post)
- Phase 14/17/18 hand-offs filed (count + targets)
- CONSOLE-13-01..10 row status update in REQUIREMENTS.md
- Ship-gate status update for v1.1.0 console portion
- Branch + PR link
</output>
