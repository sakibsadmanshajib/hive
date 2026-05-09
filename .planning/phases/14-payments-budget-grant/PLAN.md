---
phase: 14-payments-budget-grant
plan: 01
type: execute
wave: 1
depends_on: [13]
branch: a/phase-14-payments-budget-grant
milestone: v1.1
track: A
files_modified:
  - .planning/phases/14-payments-budget-grant/14-AUDIT.md
  - .planning/phases/14-payments-budget-grant/14-VERIFICATION.md
  - .planning/REQUIREMENTS.md
  - supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql
  - apps/control-plane/internal/budgets/types.go
  - apps/control-plane/internal/budgets/repository.go
  - apps/control-plane/internal/budgets/service.go
  - apps/control-plane/internal/budgets/service_test.go
  - apps/control-plane/internal/budgets/http.go
  - apps/control-plane/internal/budgets/http_test.go
  - apps/control-plane/internal/budgets/notifier.go
  - apps/control-plane/internal/budgets/notifier_test.go
  - apps/control-plane/internal/budgets/cron.go
  - apps/control-plane/internal/budgets/cron_test.go
  - apps/control-plane/internal/payments/invoices/types.go
  - apps/control-plane/internal/payments/invoices/repository.go
  - apps/control-plane/internal/payments/invoices/service.go
  - apps/control-plane/internal/payments/invoices/service_test.go
  - apps/control-plane/internal/payments/invoices/pdf.go
  - apps/control-plane/internal/payments/invoices/pdf_test.go
  - apps/control-plane/internal/payments/invoices/http.go
  - apps/control-plane/internal/payments/invoices/http_test.go
  - apps/control-plane/internal/payments/invoices/cron.go
  - apps/control-plane/internal/payments/invoices/cron_test.go
  - apps/control-plane/internal/grants/types.go
  - apps/control-plane/internal/grants/repository.go
  - apps/control-plane/internal/grants/service.go
  - apps/control-plane/internal/grants/service_test.go
  - apps/control-plane/internal/grants/http.go
  - apps/control-plane/internal/grants/http_test.go
  - apps/control-plane/internal/grants/audit.go
  - apps/control-plane/internal/grants/audit_test.go
  - apps/control-plane/internal/platform/role.go
  - apps/control-plane/internal/platform/role_test.go
  - apps/control-plane/cmd/server/main.go
  - apps/control-plane/cmd/server/wire.go
  - apps/edge-api/internal/limits/budget_gate.go
  - apps/edge-api/internal/limits/budget_gate_test.go
  - apps/edge-api/internal/server/router.go
  - apps/web-console/lib/control-plane/types.ts
  - apps/web-console/lib/control-plane/client.ts
  - apps/web-console/lib/owner-gate.ts
  - apps/web-console/app/console/billing/budgets/page.tsx
  - apps/web-console/app/console/billing/alerts/page.tsx
  - apps/web-console/app/console/billing/invoices/page.tsx
  - apps/web-console/app/console/billing/invoices/[id]/route.ts
  - apps/web-console/app/console/admin/grants/page.tsx
  - apps/web-console/app/console/admin/grants/new/page.tsx
  - apps/web-console/app/console/admin/grants/[id]/page.tsx
  - apps/web-console/components/billing/budget-form.tsx
  - apps/web-console/components/billing/budget-form.test.tsx
  - apps/web-console/components/billing/spend-alert-form.tsx
  - apps/web-console/components/billing/spend-alert-form.test.tsx
  - apps/web-console/components/billing/invoice-row.tsx
  - apps/web-console/components/admin/grant-form.tsx
  - apps/web-console/components/admin/grant-form.test.tsx
  - apps/web-console/components/admin/grant-list.tsx
  - apps/web-console/components/admin/grant-list.test.tsx
  - apps/web-console/components/admin/grant-detail.tsx
  - apps/web-console/tests/e2e/console-budgets.spec.ts
  - apps/web-console/tests/e2e/console-spend-alerts.spec.ts
  - apps/web-console/tests/e2e/console-invoices.spec.ts
  - apps/web-console/tests/e2e/admin-credit-grants.spec.ts
  - apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs
  - packages/openai-contract/generated/hive-openapi.yaml
  - packages/openai-contract/spec/paths/budgets.yaml
  - packages/openai-contract/spec/paths/spend-alerts.yaml
  - packages/openai-contract/spec/paths/invoices.yaml
  - packages/openai-contract/spec/paths/grants.yaml
  - packages/openai-contract/scripts/lint-no-customer-usd.mjs
  - packages/openai-contract/scripts/lint-no-customer-usd.test.mjs
  - deploy/docker/docker-compose.yml
autonomous: true
requirements:
  - PAY-14-01
  - PAY-14-02
  - PAY-14-03
  - PAY-14-04
  - PAY-14-05
  - PAY-14-06
  - PAY-14-07
  - PAY-14-08
  - PAY-14-09
  - PAY-14-10
  - PAY-14-11
  - PAY-14-12
must_haves:
  truths:
    - "Workspace owner can set monthly soft + hard budget caps in BDT subunits via /console/billing/budgets; values persist round-trip through control-plane and are returned typed via lib/control-plane/types.ts (no `any`/`as` casts)."
    - "Hard-cap is enforced on the inference hot-path: an inbound /v1/chat/completions request from a workspace whose month-to-date spend ledger total >= hard_cap_bdt_subunits is rejected with HTTP 402 + provider-blind sanitized error before any provider call."
    - "Soft-cap + threshold spend alerts (50%/80%/100%) fire exactly once per (workspace, period, threshold) tuple via the budgets cron evaluator; delivery dispatches email (SMTP fixture) and optional webhook URL; idempotency key is recorded so re-runs do not duplicate."
    - "Monthly invoice cron generates one invoice row per workspace covering the prior calendar month, denominated in BDT subunits only; invoice PDF download (`GET /v1/invoices/{id}/pdf`) returns a non-empty PDF whose rendered text contains BDT amounts and zero `$`, `USD`, `amount_usd`, `fx_`, `exchange_rate`, or `price_per_credit_usd` tokens (regulatory)."
    - "Owner-only discretionary credit-grant API (`POST /v1/admin/grants`) accepts {grantee_email | grantee_phone, amount_bdt_subunits, reason_note?}; resolves grantee to a user_id; appends an immutable audit row to `credit_grants` with grantor + grantee user ids, BDT subunit amount, optional note, ISO-8601 timestamp; in the SAME database transaction credits the grantee workspace's prepaid ledger via the existing append-only credit ledger primitive."
    - "Non-owner workspace member (role=admin OR role=member) calling `POST /v1/admin/grants` receives HTTP 403 with provider-blind error JSON; tested via Go http_test.go AND Playwright e2e/admin-credit-grants.spec.ts using non-owner JWT."
    - "credit_grants table is append-only at the schema level: UPDATE/DELETE on `credit_grants` raises a Postgres exception via row-level trigger `credit_grants_immutable_trg` (BEFORE UPDATE OR DELETE) — verified by a Go integration test that attempts UPDATE/DELETE and asserts the error code."
    - "Phase-18 RBAC contract stub: owner-gate decision is centralized in `apps/control-plane/internal/platform/role.go` exposing `IsWorkspaceOwner(ctx, userID, workspaceID) (bool, error)` — Phase 18 will replace the body with the tier-aware permission matrix without changing the signature; every Phase 14 owner-gated handler invokes this single function (verified by grep)."
    - "Customer-surface FX-leak grep across every Phase 14 control-plane response shape (paths/budgets.yaml, paths/spend-alerts.yaml, paths/invoices.yaml, paths/grants.yaml) returns zero `amount_usd`, `usd_`, `fx_`, `price_per_credit_usd`, `exchange_rate` keys; CI lint script `lint-no-customer-usd.mjs` enforces this on PR (the same guardrail Phase 17 will extend repo-wide)."
    - "math/big used for every BDT subunit arithmetic path (budgets.evaluate, invoices.aggregate, grants.credit, edge-api budget gate compare); zero `float64` in any new monetary code path — verified by `go vet ./apps/control-plane/internal/{budgets,grants,payments/invoices}/... ./apps/edge-api/internal/limits/...` plus a custom AST grep test that fails on float64 in flagged packages."
    - "Discretionary grant flow consumed end-to-end by Playwright: owner JWT visits /console/admin/grants/new, picks user by email, enters BDT amount, submits, sees the grant row at /console/admin/grants with note + grantor + grantee + ISO timestamp; a follow-up assertion that grantee workspace credit balance increased by the granted amount (control-plane balance endpoint)."
    - "REQUIREMENTS.md PAY-14-01..12 rows added with status `Satisfied`, evidence `[14-VERIFICATION.md](phases/14-payments-budget-grant/14-VERIFICATION.md)`; `bash scripts/verify-requirements-matrix.sh` exits 0."
    - "Phase 13 carry-forward closed: HANDOFF-13-01 (workspace switcher fixture seed), HANDOFF-13-02 (dashboard reminder ordering), HANDOFF-13-06 (discretionary credit UI surface) all listed in 14-AUDIT.md and resolved in this phase (each with linked test + commit)."
  artifacts:
    - path: ".planning/phases/14-payments-budget-grant/14-AUDIT.md"
      provides: "Pre-build inventory: existing budgets surface gaps, FX-leak survey of payment surface, Phase 13 carry-forward absorption list (HANDOFF-13-01/02/06), grantee-resolution decision (email vs phone vs both), invoice PDF library decision (gofpdf vs pdfcpu vs html-to-pdf — chosen lib + reasoning), BD VAT header research note. Drives Tasks 2-7 fix scope."
      contains: "PAY-14"
    - path: "supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql"
      provides: "Schema for `budgets` (workspace_id, period_start, soft_cap_bdt_subunits BIGINT, hard_cap_bdt_subunits BIGINT, currency CHECK = 'BDT'), `spend_alerts` (workspace_id, threshold_pct SMALLINT CHECK in (50,80,100), email TEXT, webhook_url TEXT, last_fired_at, last_fired_period), `invoices` (id UUIDv7, workspace_id, period_start, period_end, total_bdt_subunits BIGINT, line_items JSONB, pdf_storage_key TEXT, generated_at), `credit_grants` (id UUIDv7, granted_by_user_id, granted_to_user_id, granted_to_workspace_id, amount_bdt_subunits BIGINT, reason_note TEXT, ledger_entry_id UUIDv7, created_at). Includes BEFORE UPDATE OR DELETE trigger `credit_grants_immutable_trg`. Filename strictly follows `20260428_01_*` convention (next number after `20260427_01_batch_local_executor.sql`)."
      contains: "credit_grants"
    - path: "apps/control-plane/internal/grants/service.go"
      provides: "Discretionary grant service: ResolveGrantee(email|phone) -> user_id + workspace_id; CreateGrant(ctx, owner, grantee, amount_bdt, note) -> Grant. Single transaction across `credit_grants` insert + `credit_ledger` append (re-uses existing accounting primitive). math/big on every monetary path. Owner check via platform.IsWorkspaceOwner."
      contains: "IsWorkspaceOwner"
    - path: "apps/control-plane/internal/grants/http.go"
      provides: "HTTP handlers: POST /v1/admin/grants (owner-only), GET /v1/admin/grants (list w/ pagination), GET /v1/admin/grants/{id}. Provider-blind 403 on non-owner. Response shape strictly BDT subunits, zero customer-USD fields."
      contains: "POST /v1/admin/grants"
    - path: "apps/control-plane/internal/payments/invoices/pdf.go"
      provides: "PDF renderer for monthly BDT-only invoice. Header includes workspace name, period, BD VAT placeholder block (TBD note flagged in 14-AUDIT.md research finding — NOT a launch blocker), line items in BDT subunits formatted via existing format/credits primitive, total BDT. Uses gofpdf (decision documented in 14-AUDIT.md). Zero USD/FX strings."
      contains: "BDT"
    - path: "apps/control-plane/internal/budgets/cron.go"
      provides: "Nightly cron entrypoint EvaluateBudgets(ctx, now) — for each workspace with active budget, computes month-to-date spend via ledger aggregator, fires alerts whose threshold crossed since last_fired_period. Idempotent on (workspace, period, threshold)."
      contains: "EvaluateBudgets"
    - path: "apps/edge-api/internal/limits/budget_gate.go"
      provides: "Hot-path budget gate executed before provider dispatch in edge-api router. Reads cached hard_cap (Redis-backed, TTL 60s, invalidated on budget update via control-plane->Redis pub/sub or refresh-on-write). Returns 402 with provider-blind error if month-to-date spend >= hard_cap. math/big comparison."
      contains: "budget_gate"
    - path: "apps/control-plane/internal/platform/role.go"
      provides: "Centralised owner-gate primitive `IsWorkspaceOwner(ctx, userID, workspaceID) (bool, error)`. Phase 14 implements role==owner check; Phase 18 swaps body for tier-aware matrix without signature change. Single source-of-truth — every Phase 14 owner-gated handler imports this."
      contains: "IsWorkspaceOwner"
    - path: "apps/web-console/lib/control-plane/types.ts"
      provides: "Phase 14 type extensions: BudgetSettings, SpendAlert, InvoiceRecord (post-Phase 13 strip — already FX-clean), CreditGrant, GranteeLookupRequest, GranteeLookupResponse. All field names mirror Go JSON tags. Strict TS — no any/unknown/as casts."
      contains: "CreditGrant"
    - path: "apps/web-console/app/console/admin/grants/new/page.tsx"
      provides: "Owner-gated admin page: pick user by email/phone (typeahead), enter BDT amount, optional note, submit. Submits via typed client.createGrant(). Non-owner -> 403 surface (read-only)."
      contains: "grant"
    - path: "apps/web-console/app/console/admin/grants/page.tsx"
      provides: "Owner-gated admin page: paginated list of all grants in workspace scope. Shows grantor, grantee, BDT amount, ISO timestamp, note. Immutable display — no edit/delete UI affordances (matches schema-level immutability)."
      contains: "grant"
    - path: "apps/web-console/tests/e2e/admin-credit-grants.spec.ts"
      provides: "Owner happy-path: visit /console/admin/grants/new, lookup grantee by email, submit BDT amount + note, assert row appears at /console/admin/grants AND grantee workspace balance increased by exact BDT amount via control-plane balance endpoint. Non-owner negative path: same URL with non-owner JWT -> 403 / read-only surface (no submit affordance). FX-leak assertion on every grant page: regex /\\$|USD\\b|amount_usd|fx_|price_per_credit_usd/ has zero match."
      contains: "credit grant"
    - path: "apps/web-console/tests/e2e/console-budgets.spec.ts"
      provides: "Owner sets soft + hard cap; values persist on reload; non-owner sees read-only form. Hard-cap enforcement asserted by issuing a request that crosses cap and expecting 402 from edge-api (test-only fixture path)."
      contains: "budget"
    - path: "apps/web-console/tests/e2e/console-invoices.spec.ts"
      provides: "Invoice list renders BDT-only; download click yields a PDF with content-type application/pdf and zero USD strings (asserted on PDF text extraction via pdf-parse helper)."
      contains: "BDT"
    - path: "packages/openai-contract/scripts/lint-no-customer-usd.mjs"
      provides: "CI lint: scans every customer-facing path under packages/openai-contract/spec/paths/ for `amount_usd`, `usd_`, `fx_`, `price_per_credit_usd`, `exchange_rate` keys; non-zero exit on any match. Phase 17 extends to repo-wide; Phase 14 ships the primitive against the four new path files."
      contains: "amount_usd"
    - path: ".planning/phases/14-payments-budget-grant/14-VERIFICATION.md"
      provides: "Closure log: cron dry-run output, hard-cap 402 e2e capture, alert idempotency proof (run cron twice, assert single fire), grant flow Playwright pass, immutability trigger test output, FX lint output (zero matches), tsc + go test + go vet exit codes, REQUIREMENTS.md row evidence."
      contains: "PAY-14"
  key_links:
    - from: "apps/control-plane/internal/grants/http.go"
      to: "apps/control-plane/internal/platform/role.go"
      via: "platform.IsWorkspaceOwner(ctx, userID, workspaceID)"
      pattern: "IsWorkspaceOwner"
    - from: "apps/control-plane/internal/grants/service.go"
      to: "apps/control-plane/internal/ledger/"
      via: "single-tx ledger append for BDT credit"
      pattern: "ledger\\.Append|credit_ledger"
    - from: "apps/control-plane/internal/grants/service.go"
      to: "supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql"
      via: "INSERT INTO credit_grants under credit_grants_immutable_trg"
      pattern: "credit_grants"
    - from: "apps/edge-api/internal/limits/budget_gate.go"
      to: "apps/control-plane/internal/budgets/repository.go"
      via: "Redis-cached hard_cap read; control-plane invalidates on update"
      pattern: "budget_gate|hard_cap"
    - from: "apps/control-plane/internal/budgets/cron.go"
      to: "apps/control-plane/internal/budgets/notifier.go"
      via: "Notify(ctx, alert, threshold) email + webhook dispatch"
      pattern: "Notify"
    - from: "apps/control-plane/internal/payments/invoices/cron.go"
      to: "apps/control-plane/internal/payments/invoices/pdf.go"
      via: "RenderPDF(invoice) -> Supabase Storage hive-files"
      pattern: "RenderPDF"
    - from: "apps/web-console/app/console/admin/grants/new/page.tsx"
      to: "apps/web-console/lib/control-plane/client.ts"
      via: "createGrant({email|phone, amount_bdt_subunits, note?})"
      pattern: "createGrant"
    - from: "apps/web-console/lib/control-plane/types.ts"
      to: "packages/openai-contract/spec/paths/grants.yaml"
      via: "manual mirror (Phase 13 source-of-truth pattern)"
      pattern: "CreditGrant"
    - from: "packages/openai-contract/scripts/lint-no-customer-usd.mjs"
      to: "packages/openai-contract/spec/paths/{budgets,spend-alerts,invoices,grants}.yaml"
      via: "scan + fail on USD key"
      pattern: "lint-no-customer-usd"
    - from: ".planning/phases/14-payments-budget-grant/14-VERIFICATION.md"
      to: ".planning/REQUIREMENTS.md"
      via: "evidence link for PAY-14-01..12 rows"
      pattern: "PAY-14"
---

<objective>
Land the Phase 14 v1.1 Track A payments + budgets + invoicing + owner-discretionary credit-grant primitive — the credit-issuance surface that Phase 21 (chat-app trial-equivalent) consumes.

Scope:

1. **Schema** — single migration creating `budgets`, `spend_alerts`, `invoices`, `credit_grants` (with immutability trigger).
2. **Budgets** — workspace-level soft + hard caps in BDT subunits; soft cap = email/webhook alert at 50/80/100 thresholds; hard cap = pre-provider 402 block on the edge-api hot path.
3. **Spend alerts** — owner-managed thresholds + delivery channels; idempotent per (workspace, period, threshold) via cron.
4. **Invoices** — monthly cron renders one BDT-only PDF per workspace; downloadable from /console/billing/invoices.
5. **Discretionary credit grant** — owner-only API + admin UI (`POST /v1/admin/grants`, `/console/admin/grants*`); audit-logged + schema-immutable; consumed Phase 21.
6. **Phase 18 RBAC contract stub** — owner-gate centralised in `apps/control-plane/internal/platform/role.go` so Phase 18 swaps the body without breaking call-sites.
7. **Phase 17 customer-USD lint primitive** — guardrail script lands in `packages/openai-contract/scripts/lint-no-customer-usd.mjs` against the four new path files; Phase 17 extends repo-wide.
8. **Phase 13 carry-forward** — absorb HANDOFF-13-01 (workspace switcher fixture seed), HANDOFF-13-02 (dashboard reminder ordering), HANDOFF-13-06 (discretionary credit UI surface).

Out of scope (filed as hand-offs):

- Tier-aware permission matrix beyond owner/non-owner — Phase 18.
- Repo-wide FX/USD strip — Phase 17 (HANDOFF-13-03 strips control-plane Invoice.amount_usd at source; HANDOFF-13-04 splits CheckoutOptions.price_per_credit_usd into per-country pricing). Phase 14 MUST NOT introduce new customer-USD fields and MUST NOT modify those response shapes.
- Public trial-credit endpoint / auto-grant on signup — explicitly excluded per V1.1 master plan; chat-app Phase 21 calls discretionary grant API only.
- Chat-app surfacing of credit balance / top-up CTA — Phase 21.

Non-negotiable constraints (locked decisions):

- BDT-only on every customer-facing surface (regulatory).
- math/big for every BDT subunit arithmetic operation; zero float64 in new monetary code paths.
- Owner-gating airtight; non-owner -> 403; tested both at handler (Go) and at console (Playwright) layers.
- credit_grants append-only at the schema level (DB trigger), not just at the application layer.
- Atomic conventional commits per FIX-14-NN id; 1:1 commit-to-fix mapping for traceability.
- feedback_strict_typescript.md — zero `as any`/`as unknown`/` as ` casts in new web-console code.
- feedback_no_human_verification.md — every check via Playwright/curl/go test/grep; no "open browser to verify".
- feedback_local_first.md — full local docker stack green before push.
- feedback_branching.md — work on `a/phase-14-payments-budget-grant`; PR against main.

This phase unblocks:

- Phase 17 (FX/USD audit) — inherits the customer-USD lint primitive + a clean Phase 14 surface to extend it across.
- Phase 21 (chat-app credited-tier wallet + top-up CTA) — calls `POST /v1/admin/grants` for invited-user trial-equivalent credits.
- Phase 25 (BD soft launch) — billing flows must be live + green.
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
@.planning/phases/13-console-integration-fixes/PLAN.md
@.planning/phases/13-console-integration-fixes/13-AUDIT.md
@.planning/phases/13-console-integration-fixes/13-VERIFICATION.md
@.planning/phases/13-console-integration-fixes/13-01-SUMMARY.md
@apps/control-plane/internal/budgets/types.go
@apps/control-plane/internal/budgets/http.go
@apps/control-plane/internal/budgets/repository.go
@apps/control-plane/internal/budgets/service.go
@apps/control-plane/internal/budgets/notifier.go
@apps/control-plane/internal/payments/types.go
@apps/control-plane/internal/payments/http.go
@apps/control-plane/internal/payments/repository.go
@apps/control-plane/internal/payments/service.go
@apps/control-plane/internal/payments/fx.go
@apps/control-plane/internal/payments/tax.go
@apps/control-plane/internal/accounts/types.go
@apps/control-plane/internal/accounts/http.go
@apps/control-plane/internal/accounts/repository.go
@apps/control-plane/internal/ledger/
@apps/control-plane/internal/accounting/
@apps/control-plane/internal/platform/
@apps/edge-api/internal/limits/
@apps/edge-api/internal/server/router.go
@apps/web-console/lib/control-plane/types.ts
@apps/web-console/lib/control-plane/client.ts
@apps/web-console/lib/viewer-gates.ts
@apps/web-console/app/console/billing/page.tsx
@apps/web-console/components/billing/billing-overview.tsx
@apps/web-console/components/billing/budget-alert-form.tsx
@apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs
@apps/web-console/playwright.config.ts
@packages/openai-contract/generated/hive-openapi.yaml
@supabase/migrations/20260427_01_batch_local_executor.sql

<phase_13_handoffs_absorbed>
<!-- From .planning/phases/13-console-integration-fixes/13-AUDIT.md Section E -->

- HANDOFF-13-01 — Phase 14 — workspace switcher E2E spec depends on multi-account fixture seed (`auth-shell.spec.ts:88`). Fix in Task 6 (e2e-auth-fixtures extension: `seedSecondaryWorkspace`).
- HANDOFF-13-02 — Phase 14 — dashboard "Complete setup" reminder spec ordering (`profile-completion.spec.ts:71`); profile state polluted by sibling test. Fix in Task 6 (per-spec fixture cleanup hook).
- HANDOFF-13-06 — Phase 14 — discretionary credit-grant UI not present in console. Implemented Task 5 (admin/grants pages + components).

NOT carried forward into Phase 14:

- HANDOFF-13-03 — Phase 17 — control-plane Invoice.amount_usd strip at source. Phase 14 MUST NOT modify Invoice response shape; Phase 14 invoice surface produces NEW invoice records that emit BDT-only fields (no amount_usd added).
- HANDOFF-13-04 — Phase 17 — CheckoutOptions.price_per_credit_usd split. Phase 14 MUST NOT touch CheckoutOptions.
- HANDOFF-13-05 — Phase 18 — tier-aware viewer-gates extension. Phase 14 ships owner/non-owner only via platform.IsWorkspaceOwner.
</phase_13_handoffs_absorbed>

<existing_surface_inventory>
<!-- Confirmed via `ls apps/control-plane/internal/{budgets,payments,accounts,ledger,accounting}/`. -->

Pre-existing (will be EXTENDED, NOT recreated):
- `apps/control-plane/internal/budgets/{types,http,http_test,repository,service,service_test,notifier}.go` — Phase 14 ADDS cron.go, notifier_test.go, cron_test.go; EXTENDS types.go (soft/hard cap fields), service.go (eval pipeline), http.go (CRUD endpoints).
- `apps/control-plane/internal/payments/` — checkout, fx, tax, repository. Phase 14 ADDS new sub-package `internal/payments/invoices/` (separation: invoice gen is its own concern; existing payments/ stays focused on checkout rails).
- `apps/control-plane/internal/ledger/` — append-only credit ledger primitive (re-used by grants service).
- `apps/control-plane/internal/accounting/` — month-to-date spend aggregator (re-used by budgets cron).
- `apps/control-plane/internal/platform/` — currently empty / minimal. Phase 14 SEEDS `role.go` here.
- `apps/control-plane/internal/accounts/` — workspace + role model (consumed by platform/role.go owner check).
- `apps/edge-api/internal/limits/` — Phase 12 KEY-05 rate-limiter lives here. Phase 14 ADDS budget_gate.go alongside; both run pre-dispatch.
- Last applied migration: `20260427_01_batch_local_executor.sql`. Phase 14 ADDS `20260428_01_*`.

NEW packages (Phase 14 creates):
- `apps/control-plane/internal/grants/` — discretionary credit grant package (types/repository/service/http/audit).
- `apps/control-plane/internal/payments/invoices/` — invoice gen sub-package (types/repository/service/pdf/http/cron).
</existing_surface_inventory>

<known_decisions_locked>
<!-- Locked at planning time (2026-04-27); MUST be honoured during execution. -->

1. **PDF library: gofpdf** (`github.com/jung-kurt/gofpdf`). Pure Go, MIT, no CGO, no headless-browser dep. Lightweight (BDT-only invoice has minimal layout demand). Document choice + reasoning in 14-AUDIT.md research note. Alternative considered: html-to-pdf via wkhtmltopdf (rejected — adds binary dep + Docker image bloat).

2. **Grantee resolution: email OR phone (either single field).** Both indexed in `auth.users` via Supabase Auth. Lookup order: email exact-match -> phone exact-match -> 404 with provider-blind error. Phase 21 chat-app callers will pass email predominantly; phone path validated via Playwright fixture.

3. **Owner-gate primitive location: `apps/control-plane/internal/platform/role.go`.** Single source-of-truth function `IsWorkspaceOwner(ctx, userID, workspaceID) (bool, error)`. Phase 18 swaps body for tier-aware matrix without changing the signature; Phase 14 every owner-gated handler calls this exactly once at the top.

4. **Hard-cap enforcement layer: edge-api hot path** (NOT control-plane). Reason: hot-path latency budget — cannot afford a control-plane round-trip per inference request. Implementation: `apps/edge-api/internal/limits/budget_gate.go`, Redis-cached hard_cap (TTL 60s), control-plane refreshes Redis on budget update.

5. **Hard-cap error: HTTP 402 Payment Required.** Provider-blind sanitized JSON `{error: {message: "workspace credit balance below required threshold", type: "insufficient_quota", code: "budget_hard_cap_exceeded"}}`. Same shape as existing low-balance error to keep customer-facing error surface consistent.

6. **Alert idempotency: (workspace_id, period_start, threshold_pct) tuple** stored in `spend_alerts.last_fired_period`. Cron compares; fires only when crossing threshold inside a not-yet-fired period. Two cron runs in same period = single delivery.

7. **Invoice cadence: monthly, calendar-month, UTC.** Cron runs at 02:00 UTC on the 1st (next-day generation gives ledger settlement time). Idempotent: re-running the cron for an existing (workspace, period) returns the existing invoice id.

8. **BD VAT format: TBD — research note in 14-AUDIT.md, NOT a launch blocker.** Phase 14 ships invoice with placeholder VAT block (workspace name, period, BDT total, line items). 14-AUDIT.md Section H records: "BD VAT compliant header format requires legal review; deferred to launch readiness audit; placeholder ships in v1.1.0; refinement is a doc-only PR post-launch."

9. **No control-plane modifications to existing Invoice / CheckoutOptions response shapes.** Those belong to Phase 17 (HANDOFF-13-03 / HANDOFF-13-04). Phase 14 invoice records are NEW rows in a NEW `invoices` table with a NEW response shape (BDT-only, FX-clean) — does NOT replace the existing checkout-side `Invoice` struct in `payments/types.go`. Naming clarified in code: existing `payments.Invoice` -> renamed to `payments.CheckoutInvoice` if collision; new package `payments/invoices/` defines its own `Invoice` type. Decision documented in 14-AUDIT.md.

10. **Branch policy: a/phase-14-payments-budget-grant.** Single PR against main. Frontmatter `branch:` field locked; do not push to main directly.
</known_decisions_locked>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Audit + research — discover existing budgets/payments surface gaps, choose PDF lib, design schema, absorb Phase 13 hand-offs; output 14-AUDIT.md (no code changes)</name>
  <files>
    .planning/phases/14-payments-budget-grant/14-AUDIT.md
  </files>
  <action>
    Audit-first discipline: NO source code modified in this task.

    1. Inventory existing budgets surface:
       - Read `apps/control-plane/internal/budgets/{types,http,repository,service,notifier}.go`. Record current fields, current endpoints, current alert mechanism.
       - Identify gap between current surface and Phase 14 scope (soft+hard caps, threshold alerts, cron, idempotency).

    2. Inventory existing payments surface:
       - Read `apps/control-plane/internal/payments/{types,http,repository,service,fx,tax}.go`.
       - Confirm `payments.Invoice` (existing checkout-context struct) vs new `payments/invoices/Invoice` (Phase 14 monthly billing) — record naming collision + decision (rename existing OR new sub-package — locked decision #9 says new sub-package).
       - Grep `payments/` for `amount_usd`/`price_per_credit_usd` — confirm Phase 13 left them on the wire (HANDOFF-13-03 / HANDOFF-13-04 are Phase 17 work). Phase 14 must NOT touch.

    3. Inventory ledger + accounting primitives:
       - `apps/control-plane/internal/ledger/` — confirm append-only signature for credit entries (used by grants service).
       - `apps/control-plane/internal/accounting/` — confirm month-to-date spend aggregator API (used by budgets cron + invoice cron).
       - If aggregator API is missing or incomplete, add a "Phase 14 internal extension" entry, NOT a hand-off (in-scope: budgets cron depends on it).

    4. Confirm Phase 13 hand-offs absorbed:
       - HANDOFF-13-01: read `apps/web-console/tests/e2e/auth-shell.spec.ts:88` to identify the failing fixture-seed. Plan absorption as Task 6 fixture extension.
       - HANDOFF-13-02: read `tests/e2e/profile-completion.spec.ts:71` and identify the ordering pollution. Plan absorption as Task 6 fixture cleanup hook.
       - HANDOFF-13-06: confirm no `app/console/admin/grants/` exists. Plan creation in Task 5.

    5. PDF library decision:
       - Document the gofpdf decision (locked decision #1) in 14-AUDIT.md research section.
       - Confirm gofpdf is not already in `go.mod`; if it is, version-pin the existing one. If it isn't, add to go.mod (recorded for Task 4).

    6. BD VAT format research:
       - Document the placeholder approach (locked decision #8) — placeholder block, not blocked on legal review.
       - Capture a sample placeholder VAT block layout in 14-AUDIT.md.

    7. Schema design — enumerate every column for each of the four tables, including types, constraints, indexes, FK refs:
       - `budgets`: workspace_id (FK accounts.workspaces.id, ON DELETE CASCADE), period_start (date, first of month UTC), soft_cap_bdt_subunits BIGINT NOT NULL CHECK >= 0, hard_cap_bdt_subunits BIGINT NOT NULL CHECK >= soft_cap_bdt_subunits, currency CHAR(3) NOT NULL CHECK = 'BDT', created_at, updated_at. UNIQUE(workspace_id, period_start).
       - `spend_alerts`: id UUIDv7 PK, workspace_id (FK), threshold_pct SMALLINT NOT NULL CHECK IN (50,80,100), email TEXT, webhook_url TEXT, last_fired_at TIMESTAMPTZ, last_fired_period DATE, created_at. UNIQUE(workspace_id, threshold_pct).
       - `invoices`: id UUIDv7 PK, workspace_id (FK), period_start DATE NOT NULL, period_end DATE NOT NULL, total_bdt_subunits BIGINT NOT NULL CHECK >= 0, line_items JSONB NOT NULL, pdf_storage_key TEXT, generated_at TIMESTAMPTZ NOT NULL DEFAULT now(). UNIQUE(workspace_id, period_start).
       - `credit_grants`: id UUIDv7 PK, granted_by_user_id UUID NOT NULL (FK auth.users.id), granted_to_user_id UUID NOT NULL (FK auth.users.id), granted_to_workspace_id UUID NOT NULL (FK accounts.workspaces.id), amount_bdt_subunits BIGINT NOT NULL CHECK > 0, reason_note TEXT, ledger_entry_id UUID NOT NULL (FK ledger.entries.id), created_at TIMESTAMPTZ NOT NULL DEFAULT now(). NO updated_at (immutable).
       - Trigger `credit_grants_immutable_trg` BEFORE UPDATE OR DELETE ON credit_grants -> RAISE EXCEPTION 'credit_grants is append-only'.

    8. Endpoint design — enumerate every new HTTP route, request shape, response shape, owner-gating:
       - `GET  /v1/budgets` -> {workspace_id, period_start, soft_cap_bdt_subunits, hard_cap_bdt_subunits, currency}
       - `PUT  /v1/budgets` (owner) -> same shape
       - `GET  /v1/spend-alerts` -> [SpendAlert]
       - `POST /v1/spend-alerts` (owner)
       - `PATCH /v1/spend-alerts/{id}` (owner)
       - `DELETE /v1/spend-alerts/{id}` (owner)
       - `GET  /v1/invoices` -> [InvoiceRecord]
       - `GET  /v1/invoices/{id}` -> InvoiceRecord
       - `GET  /v1/invoices/{id}/pdf` -> application/pdf
       - `POST /v1/admin/grantees:lookup` (owner) -> {user_id, workspace_id, display_name} | 404
       - `POST /v1/admin/grants` (owner) -> CreditGrant
       - `GET  /v1/admin/grants` (owner) -> {items: [CreditGrant], next_cursor?}
       - `GET  /v1/admin/grants/{id}` (owner) -> CreditGrant

       Every response shape: BDT subunits, zero customer-USD fields. Document in 14-AUDIT.md.

    9. Edge-api budget gate design:
       - Where in `apps/edge-api/internal/server/router.go` to wire it (post-auth, post-rate-limit, pre-provider-dispatch).
       - Redis cache key shape: `budget:hard_cap:{workspace_id}` -> bytes (math/big string). TTL 60s.
       - Invalidation: control-plane PUT /v1/budgets writes Redis on success.
       - 402 response shape (locked decision #5).

    10. FX/USD lint primitive design:
        - Script `packages/openai-contract/scripts/lint-no-customer-usd.mjs`: read each YAML under `spec/paths/`, parse, walk `properties` keys, fail on `amount_usd|usd_*|fx_*|price_per_credit_usd|exchange_rate`. Phase 14 scope = the four new path files. Phase 17 will extend args to include all paths.
        - Test `lint-no-customer-usd.test.mjs`: synthetic spec with offending key -> non-zero exit; clean spec -> zero.

    11. Fix-list — produce numbered FIX-14-NN entries in 14-AUDIT.md Section F. Each fix:
        - id (FIX-14-NN)
        - file(s) touched
        - one-line fix description
        - target test (Go test or Playwright spec) that asserts the fix
        - PAY-14-NN requirement it satisfies

    12. Required sections in 14-AUDIT.md:
        - **Section A — Existing surface inventory** (budgets, payments, ledger, accounting, edge-api/limits, web-console/billing).
        - **Section B — Schema design** (full DDL preview; the migration body is generated in Task 2 from this section).
        - **Section C — Endpoint design** (full route table with shapes + owner-gating).
        - **Section D — Edge-api budget gate design**.
        - **Section E — Phase 13 carry-forward absorption** (HANDOFF-13-01 / 02 / 06 — what file, what change, what test).
        - **Section F — FIX-14-NN list** (drives Tasks 2-7).
        - **Section G — PDF library decision** (gofpdf + reasoning).
        - **Section H — BD VAT placeholder format research note**.
        - **Section I — FX/USD lint primitive design**.
        - **Section J — REQUIREMENTS.md mapping** (PAY-14-01..12 -> truths -> FIX-14-NN).

    13. Commit (doc-only): `docs(14): audit existing surface + design schema + absorb Phase 13 hand-offs`. Audit-first discipline — NO source under apps/, supabase/, packages/openai-contract/scripts/ touched in this task.
  </action>
  <verify>
    <automated>test -f /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -q 'Section A' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -q 'Section J' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -qE 'FIX-14-[0-9]+' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -qE 'PAY-14-(01|02|03|04|05|06|07|08|09|10|11|12)' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -q 'HANDOFF-13-01' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -q 'HANDOFF-13-02' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -q 'HANDOFF-13-06' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -q 'gofpdf' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && grep -q 'credit_grants_immutable_trg' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-AUDIT.md && cd /home/sakib/hive && git diff --quiet -- apps/ supabase/ packages/ && git diff --quiet --cached -- apps/ supabase/ packages/</automated>
  </verify>
  <done>
    14-AUDIT.md exists with Sections A-J, FIX-14-NN list, PAY-14-01..12 mapping, HANDOFF-13-01/02/06 absorption plans, gofpdf decision, immutability trigger design, and edge-api budget gate design. Zero source code changes under apps/, supabase/, packages/. Single doc-only conventional commit landed.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Schema migration — budgets / spend_alerts / invoices / credit_grants tables + immutability trigger + Phase 18 RBAC contract stub (platform/role.go)</name>
  <files>
    supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql,
    apps/control-plane/internal/platform/role.go,
    apps/control-plane/internal/platform/role_test.go
  </files>
  <behavior>
    Migration creates four tables exactly per Section B of 14-AUDIT.md.

    Test cases (RED first for role_test.go; migration validated via integration tests in Tasks 3-5):

    - `IsWorkspaceOwner(ctx, ownerUserID, workspaceID)` returns (true, nil) when accounts.workspace_members row exists with role='owner'.
    - `IsWorkspaceOwner(ctx, memberUserID, workspaceID)` returns (false, nil) when role='member' or 'admin' (Phase 18 will refine; Phase 14 strictly owner-only for grant scope).
    - `IsWorkspaceOwner(ctx, strangerUserID, workspaceID)` returns (false, nil) when no membership row.
    - `IsWorkspaceOwner(ctx, userID, missingWorkspaceID)` returns (false, ErrWorkspaceNotFound).
    - Migration: UPDATE on credit_grants raises Postgres error matching '/append-only/'.
    - Migration: DELETE on credit_grants raises Postgres error matching '/append-only/'.
    - Migration: INSERT into credit_grants with amount_bdt_subunits = 0 fails CHECK constraint.
    - Migration: INSERT into budgets with hard_cap < soft_cap fails CHECK constraint.
    - Migration: INSERT into spend_alerts with threshold_pct = 75 fails CHECK constraint.
  </behavior>
  <action>
    1. Author migration `supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql`:
       - DDL exactly per 14-AUDIT.md Section B.
       - All money columns BIGINT (BDT subunits = paisa; 1 BDT = 100 paisa). NO numeric/decimal — math/big is enforced application-side.
       - currency CHAR(3) DEFAULT 'BDT' CHECK = 'BDT' on every money-bearing table (regulatory belt-and-braces).
       - Trigger function `tg_credit_grants_immutable()` RAISE EXCEPTION 'credit_grants is append-only — UPDATE/DELETE forbidden' (matches Postgres error pattern checked in test).
       - Indexes: budgets(workspace_id, period_start), spend_alerts(workspace_id), invoices(workspace_id, period_start), credit_grants(granted_to_workspace_id, created_at DESC), credit_grants(granted_by_user_id, created_at DESC).
       - Forward-only: NO down migration (project policy — confirm against existing migration files).

    2. Author `apps/control-plane/internal/platform/role.go`:
       - Package: `platform`
       - Public function `IsWorkspaceOwner(ctx context.Context, userID, workspaceID uuid.UUID) (bool, error)`.
       - Read `accounts.workspace_members` for (user_id, workspace_id); compare role.
       - Sentinel error `ErrWorkspaceNotFound = errors.New("workspace not found")`.
       - Doc comment cites Phase 18: "Phase 18 RBAC matrix replaces this body with a tier-aware permission matrix evaluator. Signature is the contract; do NOT change without updating Phase 18 plan."

    3. Author `apps/control-plane/internal/platform/role_test.go`:
       - Use existing pgtest harness from `apps/control-plane/internal/accounts/repository_test.go` (mirror its setup). Boots a transactional test DB.
       - Implement RED-first cases from <behavior>.
       - Migration trigger tests: open a separate test connection, attempt UPDATE/DELETE on a seeded credit_grants row, assert error string contains 'append-only'.
       - CHECK constraint tests: attempt offending INSERT, assert error string contains 'check constraint'.

    4. Run:
       ```
       cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c \
         "cd /workspace && go test ./apps/control-plane/internal/platform/... -count=1 -short"
       ```
       Must exit 0.

    5. Apply migration locally + assert idempotency:
       ```
       cd /home/sakib/hive && supabase db reset --local && supabase db push --local
       ```
       (or equivalent against the docker postgres if no Supabase CLI link).

    6. Commits (atomic, conventional):
       - `feat(14): migration 20260428_01 — budgets, spend_alerts, invoices, credit_grants + immutability trigger` (FIX-14-01)
       - `feat(14): platform/role.IsWorkspaceOwner — Phase 18 RBAC contract stub` (FIX-14-02)

    Constraints:
    - No edits to `payments.Invoice` (existing struct in `apps/control-plane/internal/payments/types.go`); locked decision #9.
    - math/big NOT applicable here (DB schema, not arithmetic) — but the BIGINT columns guarantee exact subunit precision so application code can map cleanly to *big.Int.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && go test ./apps/control-plane/internal/platform/... -count=1 -short" && grep -q 'CREATE TABLE.*budgets' /home/sakib/hive/supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql && grep -q 'CREATE TABLE.*spend_alerts' /home/sakib/hive/supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql && grep -q 'CREATE TABLE.*invoices' /home/sakib/hive/supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql && grep -q 'CREATE TABLE.*credit_grants' /home/sakib/hive/supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql && grep -q 'credit_grants_immutable_trg' /home/sakib/hive/supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql && grep -q "currency.*BDT" /home/sakib/hive/supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql && grep -q 'IsWorkspaceOwner' /home/sakib/hive/apps/control-plane/internal/platform/role.go && grep -q 'Phase 18' /home/sakib/hive/apps/control-plane/internal/platform/role.go</automated>
  </verify>
  <done>
    Migration file applied cleanly (idempotent on re-run); all four tables present with constraints + trigger; platform/role.IsWorkspaceOwner exists with sentinel error + Phase 18 contract comment; role_test.go RED-first cases all pass; CHECK + immutability trigger tests pass; two atomic commits landed.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Budgets — soft/hard cap CRUD + spend-alert CRUD + cron evaluator + edge-api hard-cap gate (402)</name>
  <files>
    apps/control-plane/internal/budgets/types.go,
    apps/control-plane/internal/budgets/repository.go,
    apps/control-plane/internal/budgets/service.go,
    apps/control-plane/internal/budgets/service_test.go,
    apps/control-plane/internal/budgets/http.go,
    apps/control-plane/internal/budgets/http_test.go,
    apps/control-plane/internal/budgets/notifier.go,
    apps/control-plane/internal/budgets/notifier_test.go,
    apps/control-plane/internal/budgets/cron.go,
    apps/control-plane/internal/budgets/cron_test.go,
    apps/control-plane/cmd/server/main.go,
    apps/control-plane/cmd/server/wire.go,
    apps/edge-api/internal/limits/budget_gate.go,
    apps/edge-api/internal/limits/budget_gate_test.go,
    apps/edge-api/internal/server/router.go,
    packages/openai-contract/spec/paths/budgets.yaml,
    packages/openai-contract/spec/paths/spend-alerts.yaml,
    packages/openai-contract/generated/hive-openapi.yaml
  </files>
  <behavior>
    Test cases (RED first):

    Service:
    - SetBudget(ws, soft=1000_00, hard=2000_00) persists; GetBudget round-trips identical *big.Int values.
    - SetBudget(ws, soft=2000_00, hard=1000_00) -> ErrInvalidCaps.
    - ListAlerts(ws) returns the three default thresholds after CreateAlert(ws, 50, "x@y", nil), CreateAlert(ws, 80, "x@y", nil), CreateAlert(ws, 100, "x@y", nil).
    - CreateAlert(ws, 75, ...) -> ErrInvalidThreshold (DB CHECK plus app-side guard).

    Cron:
    - EvaluateBudgets(now) with workspace whose MTD spend = 50% of soft_cap fires the 50-threshold alert exactly once.
    - Second EvaluateBudgets(now+1m) within same period does NOT fire again (idempotency via last_fired_period).
    - EvaluateBudgets(nowNextPeriod) with same threshold-crossed state fires again (new period).
    - Notifier dispatches both email + webhook when both configured; only email if webhook null.

    Edge-api budget_gate:
    - Cap=2000_00; ledger MTD=2100_00 -> CheckBudget returns ErrHardCapExceeded.
    - Cap=2000_00; ledger MTD=1500_00 -> CheckBudget returns nil.
    - Cap=2000_00; ledger MTD=2000_00 (exact) -> CheckBudget returns ErrHardCapExceeded (>= comparison).
    - Cache hit (Redis) avoids control-plane round-trip; cache miss reads through.
    - Comparison uses *big.Int.Cmp (asserted via test that overflow values past int64 limit still compare correctly).

    HTTP:
    - GET /v1/budgets owner -> 200 + shape; non-owner -> 200 read-only same shape.
    - PUT /v1/budgets owner -> 200; non-owner -> 403 provider-blind.
    - POST /v1/spend-alerts owner -> 201; non-owner -> 403.
    - All response shapes: zero `amount_usd|usd_|fx_|exchange_rate|price_per_credit_usd` keys (FX-clean response audit grep).

    402 hot-path:
    - Edge-api router: inbound /v1/chat/completions for ws over hard_cap -> 402 with body {error.code: "budget_hard_cap_exceeded"} BEFORE provider dispatch (assert by mocking provider client and verifying it was NOT called).
  </behavior>
  <action>
    1. Extend `apps/control-plane/internal/budgets/types.go`:
       - `Budget { WorkspaceID uuid.UUID; PeriodStart time.Time; SoftCap, HardCap *big.Int; Currency string }`.
       - `SpendAlert { ID uuid.UUID; WorkspaceID uuid.UUID; ThresholdPct int; Email *string; WebhookURL *string; LastFiredAt *time.Time; LastFiredPeriod *time.Time }`.
       - Sentinel errors: `ErrInvalidCaps`, `ErrInvalidThreshold`, `ErrHardCapExceeded`.

    2. Extend `repository.go` with CRUD on budgets + spend_alerts; use `pgx` typed scans; *big.Int marshalled via `Int64()` (BIGINT column = int64 ceiling — assertion: BDT subunit caps fit int64 since max BDT subunit = ~9.2e18 paisa = 92 quadrillion BDT, far exceeds any plausible workspace cap; documented in service_test).

    3. Service `service.go`:
       - SetBudget enforces hard >= soft (math/big.Int.Cmp).
       - On successful PUT, publish Redis invalidation key `budget:hard_cap:{workspace_id}` -> new value (so edge-api gate sees it within TTL).
       - CreateAlert / UpdateAlert / DeleteAlert.

    4. Cron `cron.go`:
       - `EvaluateBudgets(ctx, now time.Time, period time.Time)` walks workspaces with active budgets.
       - For each: aggregate MTD spend via `accounting.MonthToDateSpend(ctx, ws, period)` returning *big.Int.
       - For each alert in (50, 80, 100): if (mtd / soft_cap * 100) >= threshold AND (last_fired_period < period OR null), fire via notifier and stamp last_fired_period.
       - All math via *big.Int (mtd, soft_cap, hard_cap, percentage). Compute threshold cross via `mtd*100 >= soft_cap*threshold` to avoid float division.

    5. Notifier `notifier.go`:
       - Email via existing SMTP primitive (read existing notifier.go for the dispatch pattern; extend if needed).
       - Webhook via `net/http` POST with HMAC signature header `X-Hive-Signature` (HMAC-SHA256 over body using webhook secret stored alongside webhook_url — schema decision: extend `spend_alerts` with `webhook_secret` TEXT? Plan: store secret in same row; decision documented Task 1 14-AUDIT.md Section B).
       - Retry: 3 attempts, exponential backoff (200ms, 400ms, 800ms); abandon after — log warn.
       - Test via httptest.NewServer.

    6. HTTP `http.go`:
       - Routes registered: GET/PUT /v1/budgets, GET/POST /v1/spend-alerts, PATCH/DELETE /v1/spend-alerts/{id}.
       - Owner-gating via `platform.IsWorkspaceOwner` (single line at top of mutating handlers).
       - Response JSON: BDT subunits as int64 (fits — see step 2). Field names mirror types.ts.
       - Provider-blind 403 / 404 / 400 via existing `errors_blind.go` helper if present; otherwise inline.

    7. Wire registration in `apps/control-plane/cmd/server/{main,wire}.go`:
       - Register budgets HTTP routes.
       - Register cron job: `EvaluateBudgets` runs hourly (decision: hourly, not nightly — alert latency target ≤1h; documented in 14-AUDIT.md Section D).

    8. Edge-api `apps/edge-api/internal/limits/budget_gate.go`:
       - `CheckBudget(ctx, ws) error`. Reads Redis `budget:hard_cap:{ws}`; on miss, reads control-plane `GET /internal/budgets/{ws}/hard-cap` (NEW internal-only endpoint added in step 6 — annotate INTERNAL_ONLY in OpenAPI spec; this endpoint is service-mesh authenticated, not customer-facing).
       - Reads MTD via existing accounting aggregator (or hot-path cached-counter — decision: re-use Redis spend counter if Phase 12 created one; otherwise hit control-plane internal endpoint with same TTL pattern).
       - Returns ErrHardCapExceeded if `mtd >= hardCap` (*big.Int.Cmp >= 0).
    9. Wire gate into `apps/edge-api/internal/server/router.go`:
       - Insert AFTER auth + AFTER rate-limit (Phase 12 KEY-05 ordering preserved), BEFORE provider dispatch.
       - On ErrHardCapExceeded -> respond 402 with provider-blind body.

    10. OpenAPI spec:
        - `packages/openai-contract/spec/paths/budgets.yaml` — Budget shape, GET, PUT.
        - `packages/openai-contract/spec/paths/spend-alerts.yaml` — SpendAlert shape, GET, POST, PATCH, DELETE.
        - Regenerate `packages/openai-contract/generated/hive-openapi.yaml` via existing build pipeline.
        - Zero `amount_usd|usd_|fx_|price_per_credit_usd|exchange_rate` keys.

    11. Tests — Go (RED first):
        ```
        cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c \
          "cd /workspace && go test ./apps/control-plane/internal/budgets/... ./apps/edge-api/internal/limits/... -count=1 -short"
        ```

    12. math/big audit grep — must return zero hits in modified packages:
        ```
        grep -RnE '\bfloat64\b|\bfloat32\b' \
          apps/control-plane/internal/budgets/ \
          apps/edge-api/internal/limits/budget_gate.go \
          apps/edge-api/internal/limits/budget_gate_test.go \
          | grep -v '_test\.go.*overflow' || true
        ```
        (Expect zero non-test occurrences. Test files may reference float64 only when proving big.Int handles overflow values float64 cannot.)

    13. Commits (atomic, conventional, 1:1 with FIX-14-NN):
        - `feat(14): budgets soft/hard cap CRUD + types extension` (FIX-14-03)
        - `feat(14): spend alert CRUD with threshold guard` (FIX-14-04)
        - `feat(14): budgets cron evaluator with idempotent threshold dispatch` (FIX-14-05)
        - `feat(14): notifier — webhook HMAC + retry` (FIX-14-06)
        - `feat(14): edge-api hard-cap budget gate (402, math/big, Redis cache)` (FIX-14-07)
        - `feat(14): OpenAPI spec — budgets + spend-alerts paths` (FIX-14-08)

    Constraints:
    - math/big on every monetary path. float64 banned in budgets/, edge-api/limits/budget_gate*.go (verified by grep above).
    - Provider-blind errors. Provider names never appear in error JSON.
    - Existing payments.Invoice / CheckoutOptions UNTOUCHED (Phase 17 owns).
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && go test ./apps/control-plane/internal/budgets/... ./apps/edge-api/internal/limits/... -count=1 -short" && cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && go vet ./apps/control-plane/internal/budgets/... ./apps/edge-api/internal/limits/..." && ! grep -RnE '\bfloat64\b|\bfloat32\b' /home/sakib/hive/apps/control-plane/internal/budgets/ /home/sakib/hive/apps/edge-api/internal/limits/budget_gate.go 2>/dev/null | grep -v _test.go && ! grep -RnE 'amount_usd|usd_|fx_|price_per_credit_usd|exchange_rate' /home/sakib/hive/packages/openai-contract/spec/paths/budgets.yaml /home/sakib/hive/packages/openai-contract/spec/paths/spend-alerts.yaml 2>/dev/null && grep -q 'IsWorkspaceOwner' /home/sakib/hive/apps/control-plane/internal/budgets/http.go && grep -q '402' /home/sakib/hive/apps/edge-api/internal/limits/budget_gate.go</automated>
  </verify>
  <done>
    Budgets CRUD, spend-alert CRUD, cron evaluator with idempotency, edge-api hard-cap gate (402, math/big, Redis-cached) all green; six atomic commits landed; OpenAPI spec extends with two new paths (zero customer-USD keys); float64 grep zero in flagged packages; IsWorkspaceOwner gates every mutating handler; no modifications to payments.Invoice / CheckoutOptions.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 4: Invoices — monthly cron + BDT-only PDF generator + list/download HTTP + Supabase Storage write</name>
  <files>
    apps/control-plane/internal/payments/invoices/types.go,
    apps/control-plane/internal/payments/invoices/repository.go,
    apps/control-plane/internal/payments/invoices/service.go,
    apps/control-plane/internal/payments/invoices/service_test.go,
    apps/control-plane/internal/payments/invoices/pdf.go,
    apps/control-plane/internal/payments/invoices/pdf_test.go,
    apps/control-plane/internal/payments/invoices/http.go,
    apps/control-plane/internal/payments/invoices/http_test.go,
    apps/control-plane/internal/payments/invoices/cron.go,
    apps/control-plane/internal/payments/invoices/cron_test.go,
    apps/control-plane/cmd/server/main.go,
    apps/control-plane/cmd/server/wire.go,
    packages/openai-contract/spec/paths/invoices.yaml,
    packages/openai-contract/generated/hive-openapi.yaml
  </files>
  <behavior>
    Test cases (RED first):

    Service:
    - GenerateInvoiceForPeriod(ws, period) aggregates ledger entries -> returns Invoice with total_bdt_subunits = sum (math/big.Int) across ledger entries; line_items JSONB has one entry per (model_id, request_count, total_bdt_subunits).
    - Re-running for same (ws, period) returns the existing invoice id (idempotent via UNIQUE constraint).
    - Period boundary: ledger entries timestamped at exact period_end UTC midnight included in current period (locked: `created_at >= period_start AND created_at < period_end`).

    PDF:
    - RenderPDF(invoice) -> []byte; pdf-parse extracted text contains workspace name, period, BDT total, "BDT" or "৳" symbol; contains zero of `$|USD|amount_usd|fx_|exchange_rate|price_per_credit_usd`.
    - PDF size < 200KB for an invoice with ≤100 line items (sanity).

    HTTP:
    - GET /v1/invoices owner+member -> list (member can only see own workspace's).
    - GET /v1/invoices/{id} 200 with InvoiceRecord shape (BDT-only).
    - GET /v1/invoices/{id}/pdf -> 200 application/pdf + Content-Disposition; non-owner of workspace -> 404 (not 403, to avoid id-enumeration leak).
    - Response shapes: zero customer-USD fields.

    Cron:
    - GenerateMonthlyInvoices(now) for now=2026-05-01 02:00 UTC generates invoices for period 2026-04-01..2026-05-01 across every active workspace.
    - Re-running same cron is a no-op (idempotent).
    - Failed PDF render for one workspace does not block others (per-ws error isolation; test injects a render failure for one ws).
  </behavior>
  <action>
    1. Add gofpdf to go.mod: `go get github.com/jung-kurt/gofpdf@latest` (pin version per Task 1 decision); commit `chore(14): add gofpdf dependency for BDT invoice PDF` (FIX-14-09).

    2. Create new sub-package `apps/control-plane/internal/payments/invoices/`:
       - `types.go`: `Invoice { ID uuid.UUID; WorkspaceID uuid.UUID; PeriodStart, PeriodEnd time.Time; TotalBDTSubunits *big.Int; LineItems []InvoiceLineItem; PDFStorageKey string; GeneratedAt time.Time }`. Currency=BDT implicit (no field on customer surface — regulatory).
       - InvoiceLineItem: ModelID string, RequestCount int64, BDTSubunits *big.Int.

    3. Repository: pgx CRUD; UNIQUE(workspace_id, period_start) enforces idempotency.

    4. Service `service.go`:
       - `GenerateInvoiceForPeriod(ctx, ws, period)`:
         - Aggregate ledger via `accounting.AggregateByModel(ctx, ws, period.Start, period.End)` returning `[]InvoiceLineItem`.
         - Sum total via *big.Int.
         - Render PDF, write to Supabase Storage `hive-files` bucket key `invoices/{ws}/{period}.pdf`.
         - Insert invoices row.
       - Idempotency: ON CONFLICT (workspace_id, period_start) DO NOTHING RETURNING; if no row, fetch existing.

    5. PDF `pdf.go`:
       - gofpdf Letter portrait. Header: "Invoice — Hive — BDT". Workspace name + period + invoice id + generated_at.
       - VAT placeholder block (locked decision #8): "BD VAT registration: TBD" + tax line `০ BDT` (Bengali numerals — note locale handling decision; English "0 BDT" is acceptable v1.1.0 — documented).
       - Line items table: model | requests | BDT amount.
       - Total row: bold, BDT total formatted via existing format/credits primitive (must produce BDT-only string).
       - Zero $ / USD / fx_ tokens — regression-tested by pdf_test.go against extracted text.

    6. HTTP `http.go`:
       - GET /v1/invoices, GET /v1/invoices/{id}, GET /v1/invoices/{id}/pdf.
       - PDF endpoint: read from Supabase Storage signed URL OR proxy bytes (decision: signed URL with short TTL — avoids edge-api proxy CPU cost); fallback to proxy if storage env var missing.
       - Owner OR same-workspace member can access (member-read allowed); cross-workspace -> 404.

    7. Cron `cron.go`:
       - `GenerateMonthlyInvoices(ctx, now)` at 02:00 UTC on day 1.
       - Walks `accounts.workspaces` active set; per workspace, calls service.GenerateInvoiceForPeriod for previous calendar month.
       - Per-workspace error isolation: log + continue.
       - Wire into cmd/server/wire.go.

    8. OpenAPI `packages/openai-contract/spec/paths/invoices.yaml`:
       - InvoiceRecord shape: id, workspace_id, period_start, period_end, total_bdt_subunits, line_items[], pdf_url, generated_at.
       - Zero customer-USD keys (verified by lint primitive in Task 7).

    9. Tests:
       - service_test.go: idempotency, aggregation correctness (mock ledger), boundary inclusivity.
       - pdf_test.go: extract text via `github.com/ledongthuc/pdf` (or shell out to `pdftotext` available in toolchain image — confirm); assert BDT-only.
       - http_test.go: owner/member/cross-workspace matrix.
       - cron_test.go: idempotency + per-ws error isolation.

       ```
       cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c \
         "cd /workspace && go test ./apps/control-plane/internal/payments/invoices/... -count=1 -short"
       ```

    10. math/big audit:
        ```
        ! grep -RnE '\bfloat64\b|\bfloat32\b' /home/sakib/hive/apps/control-plane/internal/payments/invoices/ | grep -v _test.go
        ```

    11. Commits (atomic):
        - `feat(14): invoices sub-package — types + repository` (FIX-14-10)
        - `feat(14): invoice generation service with ledger aggregation` (FIX-14-11)
        - `feat(14): invoice PDF render — gofpdf, BDT-only` (FIX-14-12)
        - `feat(14): invoice HTTP — list + detail + PDF download` (FIX-14-13)
        - `feat(14): monthly invoice cron with per-workspace error isolation` (FIX-14-14)
        - `feat(14): OpenAPI spec — invoices path` (FIX-14-15)

    Constraints:
    - NEW sub-package; do NOT touch `apps/control-plane/internal/payments/{types,http,service}.go` (existing checkout surface — Phase 17 territory).
    - Supabase Storage only (per project_no_minio.md memory). Bucket `hive-files` must already exist (pre-condition).
    - PDF MUST contain zero USD strings; pdf_test.go is the tripwire.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && go test ./apps/control-plane/internal/payments/invoices/... -count=1 -short" && cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && go vet ./apps/control-plane/internal/payments/invoices/..." && ! grep -RnE '\bfloat64\b|\bfloat32\b' /home/sakib/hive/apps/control-plane/internal/payments/invoices/ 2>/dev/null | grep -v _test.go && ! grep -RnE 'amount_usd|usd_|fx_|price_per_credit_usd|exchange_rate' /home/sakib/hive/packages/openai-contract/spec/paths/invoices.yaml 2>/dev/null && grep -q 'gofpdf' /home/sakib/hive/go.work.sum /home/sakib/hive/apps/control-plane/go.sum 2>/dev/null</automated>
  </verify>
  <done>
    invoices sub-package compiles + tests pass; PDF round-trip extraction asserts BDT-only with zero USD tokens; HTTP list/detail/download exposed; monthly cron wired with per-workspace isolation; OpenAPI spec has invoices.yaml; six atomic commits landed; existing payments/ package untouched.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 5: Discretionary credit grant — owner-only API + immutable audit + ledger credit (single-tx) + admin UI pages + grantee lookup</name>
  <files>
    apps/control-plane/internal/grants/types.go,
    apps/control-plane/internal/grants/repository.go,
    apps/control-plane/internal/grants/service.go,
    apps/control-plane/internal/grants/service_test.go,
    apps/control-plane/internal/grants/http.go,
    apps/control-plane/internal/grants/http_test.go,
    apps/control-plane/internal/grants/audit.go,
    apps/control-plane/internal/grants/audit_test.go,
    apps/control-plane/cmd/server/main.go,
    apps/control-plane/cmd/server/wire.go,
    apps/web-console/lib/control-plane/types.ts,
    apps/web-console/lib/control-plane/client.ts,
    apps/web-console/lib/owner-gate.ts,
    apps/web-console/app/console/admin/grants/page.tsx,
    apps/web-console/app/console/admin/grants/new/page.tsx,
    apps/web-console/app/console/admin/grants/[id]/page.tsx,
    apps/web-console/components/admin/grant-form.tsx,
    apps/web-console/components/admin/grant-form.test.tsx,
    apps/web-console/components/admin/grant-list.tsx,
    apps/web-console/components/admin/grant-list.test.tsx,
    apps/web-console/components/admin/grant-detail.tsx,
    apps/web-console/tests/e2e/admin-credit-grants.spec.ts,
    apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs,
    packages/openai-contract/spec/paths/grants.yaml,
    packages/openai-contract/generated/hive-openapi.yaml
  </files>
  <behavior>
    Test cases (RED first):

    Service:
    - ResolveGrantee(email="user@x.com") with existing user -> {user_id, primary_workspace_id, display_name}.
    - ResolveGrantee(phone="+8801...") with existing user -> resolved.
    - ResolveGrantee(email="missing@x.com") -> ErrGranteeNotFound.
    - ResolveGrantee with both email AND phone -> ErrAmbiguousLookup (force one-or-other).
    - CreateGrant(owner, grantee, amount=1000_00, note="trial credit"):
      - Inserts credit_grants row with all fields.
      - Appends credit ledger entry crediting grantee_workspace +1000_00 BDT subunits.
      - Both INSERTs in single DB transaction (test: panic between inserts -> assert no row in either table).
      - Returns Grant with all fields.
    - CreateGrant(owner, grantee, amount=0, ...) -> ErrInvalidAmount (must be >0).
    - CreateGrant(owner=non-owner-userID, ...) -> ErrNotOwner.

    Audit immutability:
    - Direct repository.Update(grant) -> error contains 'append-only' (DB trigger fires).
    - Direct repository.Delete(grantID) -> error contains 'append-only'.

    HTTP:
    - POST /v1/admin/grants owner JWT -> 201 + Grant.
    - POST /v1/admin/grants member JWT -> 403 provider-blind.
    - POST /v1/admin/grants admin JWT -> 403 (Phase 14 strictly owner; Phase 18 may relax).
    - POST /v1/admin/grants unauth -> 401.
    - GET /v1/admin/grants owner -> paginated list ordered by created_at DESC.
    - GET /v1/admin/grants/{id} owner -> Grant; cross-workspace owner -> 404.
    - POST /v1/admin/grantees:lookup owner with email -> 200; non-owner -> 403.

    Console:
    - vitest grant-form.test.tsx: render form; submit valid -> calls client.createGrant with typed payload; validation rejects amount<=0; validation rejects empty email AND empty phone.
    - vitest grant-list.test.tsx: render list of mock grants; no edit/delete affordances rendered (immutability surfaced in UI).
    - Playwright admin-credit-grants.spec.ts:
      - Owner happy-path: lookup grantee by email -> submit BDT amount + note -> assert row at /console/admin/grants AND grantee balance increased by exact subunit amount (probe via GET /v1/balances/{ws}).
      - Non-owner negative: same URL with non-owner JWT -> page renders read-only (no submit affordance) AND POST /v1/admin/grants direct API call returns 403.
      - FX-leak: regex `/\$|USD\b|amount_usd|fx_|price_per_credit_usd/` on rendered body -> zero matches across all three grant pages.
      - Audit immutability surfaced: list page has no edit/delete button.
  </behavior>
  <action>
    1. Create `apps/control-plane/internal/grants/`:
       - types.go: `Grant { ID uuid.UUID; GrantedByUserID, GrantedToUserID, GrantedToWorkspaceID uuid.UUID; AmountBDTSubunits *big.Int; ReasonNote *string; LedgerEntryID uuid.UUID; CreatedAt time.Time }`.
       - Sentinels: ErrGranteeNotFound, ErrAmbiguousLookup, ErrInvalidAmount, ErrNotOwner.

    2. repository.go: pgx CRUD (Insert + List + Get only — Update/Delete deliberately omitted at the application layer; DB trigger is the second line of defence).

    3. service.go:
       - ResolveGrantee(ctx, email *string, phone *string) -> reads Supabase auth.users (via existing accounts repository). Exactly one of email/phone non-nil.
       - CreateGrant(ctx, ownerID, granteeID, granteeWorkspaceID, amount *big.Int, note *string):
         - Owner check: `platform.IsWorkspaceOwner(ctx, ownerID, granteeWorkspaceID)` -- owner of the GRANTEE workspace? Decision: NO. Owner-gate is "is ownerID a Sakib-tier owner?" Phase 14 implementation: simplest viable — `IsWorkspaceOwner(ctx, ownerID, ownerID's-default-workspace)` is wrong. CORRECT contract: owner-gate is "is this user permitted to grant"; in Phase 14 that = "is the caller the owner of ANY workspace that has the platform-admin flag". For Phase 14 simplicity: gate on `accounts.is_platform_admin(userID)` flag (NEW boolean column on `accounts.users` set true only for Sakib via migration seed in Task 2). Update Task 2 plan to include `is_platform_admin BOOLEAN NOT NULL DEFAULT false` on accounts.users + seed Sakib's row.
         - **PLAN-LEVEL CORRECTION:** Task 2 migration MUST also add `ALTER TABLE accounts.users ADD COLUMN is_platform_admin BOOLEAN NOT NULL DEFAULT false;` and a seed row for Sakib (env-var driven seed: `PLATFORM_ADMIN_EMAIL`).
         - Phase 18 RBAC will replace `is_platform_admin` with the tier-aware matrix; Phase 14 ships the simple flag as the contract stub.
         - Renames: `platform.IsWorkspaceOwner` stays; ADDS `platform.IsPlatformAdmin(ctx, userID) (bool, error)` — also in role.go.
       - Single transaction: pgx Tx, INSERT credit_grants -> append ledger entry -> COMMIT. Rollback on any step fail.

    4. audit.go: helper to format grant audit log entry for downstream Phase 21 consumption (chat-app may want to show grant history). Logs to platform's existing structured logger (`slog`).

    5. HTTP http.go:
       - Routes: POST /v1/admin/grants, GET /v1/admin/grants, GET /v1/admin/grants/{id}, POST /v1/admin/grantees:lookup.
       - Owner-gate via `platform.IsPlatformAdmin`. Provider-blind 403.
       - Response shape: zero customer-USD fields.

    6. Wire in cmd/server/{main,wire}.go.

    7. Web-console — types extension:
       - apps/web-console/lib/control-plane/types.ts: ADD `CreditGrant`, `GranteeLookupRequest`, `GranteeLookupResponse`. Strict TS, mirror Go json tags.
       - apps/web-console/lib/control-plane/client.ts: ADD `createGrant`, `listGrants`, `getGrant`, `lookupGrantee`. Typed `<Req, Res>` per Phase 13 pattern. Zero `as`/`any`.
       - apps/web-console/lib/owner-gate.ts: NEW server-side helper `requirePlatformAdmin(req): Promise<UserContext>`; throws 403 redirect for non-admin. Calls control-plane `GET /v1/me` to read `is_platform_admin` flag.

    8. Pages:
       - app/console/admin/grants/page.tsx — list (server component using client.listGrants, owner-gated via owner-gate.ts).
       - app/console/admin/grants/new/page.tsx — create form (server component shell + client component grant-form.tsx).
       - app/console/admin/grants/[id]/page.tsx — detail (server component using client.getGrant).

    9. Components:
       - components/admin/grant-form.tsx — controlled form, BDT subunit input (BDT whole-unit -> subunit conversion via existing format/credits primitive — multiply by 100), grantee lookup typeahead (POST /v1/admin/grantees:lookup on email blur), note textarea, submit -> client.createGrant -> redirect to /console/admin/grants/[id].
       - components/admin/grant-list.tsx — paginated table; columns: when (ISO), grantor, grantee, BDT amount, note. NO edit/delete buttons (immutability mirror).
       - components/admin/grant-detail.tsx — read-only detail card.
       - vitest tests for form + list per <behavior>.

    10. Playwright admin-credit-grants.spec.ts:
        - Use e2e-auth-fixtures.mjs; ADD `seedPlatformAdmin` (sets is_platform_admin=true on the test owner) and `seedNonAdminOwner` helpers.
        - Owner happy-path test as in <behavior>.
        - Non-owner negative test.
        - FX-leak regex assertion on each grant page.
        - workers: 1 honoured.

    11. OpenAPI spec packages/openai-contract/spec/paths/grants.yaml — full path definitions; zero customer-USD keys.

    12. Run tests:
        ```
        # Go
        cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c \
          "cd /workspace && go test ./apps/control-plane/internal/grants/... ./apps/control-plane/internal/platform/... -count=1 -short"
        # web-console
        cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console npx tsc --noEmit
        cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console npm run test:unit
        cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console npm run build
        # Playwright
        cd /home/sakib/hive/apps/web-console && CI=true npx playwright test admin-credit-grants console-budgets console-spend-alerts console-invoices
        ```

    13. Strict-TS audit:
        ```
        ! grep -RnE '\bas (any|unknown)\b|: any\b' \
          /home/sakib/hive/apps/web-console/app/console/admin/grants/ \
          /home/sakib/hive/apps/web-console/components/admin/ \
          /home/sakib/hive/apps/web-console/lib/owner-gate.ts
        ```

    14. Commits (atomic, conventional, 1:1 with FIX-14-NN):
        - `feat(14): grants package — types + repository + audit` (FIX-14-16)
        - `feat(14): grants service — single-tx ledger credit + grantee lookup` (FIX-14-17)
        - `feat(14): grants HTTP — admin endpoints + owner-gate` (FIX-14-18)
        - `feat(14): platform/role.IsPlatformAdmin + accounts.users.is_platform_admin migration extension` (FIX-14-19) [extends Task 2 migration; squash-OK if before merge]
        - `feat(14): web-console types + client — CreditGrant + lookupGrantee` (FIX-14-20)
        - `feat(14): web-console admin/grants pages + form + list components` (FIX-14-21)
        - `feat(14): OpenAPI spec — grants path` (FIX-14-22)
        - `test(14): admin-credit-grants Playwright spec — owner + non-owner + FX-leak guard` (FIX-14-23)

    Constraints:
    - feedback_strict_typescript.md — zero `as any`/`as unknown`/` as ` casts in new TS files.
    - feedback_chatapp_credits.md — owner-discretionary only; no auto-grant; no public trial-credit endpoint. Phase 14 ships exactly that contract.
    - math/big in service.go for amount handling.
    - Provider-blind 403/401/404.
    - Single tx for grant insert + ledger append; rollback safety asserted by test.
    - IsPlatformAdmin flag is the contract Phase 18 will replace; do NOT hard-code Sakib's user_id anywhere — use the flag.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && go test ./apps/control-plane/internal/grants/... ./apps/control-plane/internal/platform/... -count=1 -short" && cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console npx tsc --noEmit && docker compose run --rm web-console npm run test:unit && docker compose run --rm web-console npm run build && cd /home/sakib/hive/apps/web-console && CI=true npx playwright test admin-credit-grants console-budgets console-spend-alerts console-invoices --reporter=list && ! grep -RnE '\bas (any|unknown)\b|: any\b' /home/sakib/hive/apps/web-console/app/console/admin/grants/ /home/sakib/hive/apps/web-console/components/admin/ /home/sakib/hive/apps/web-console/lib/owner-gate.ts 2>/dev/null && ! grep -RnE 'amount_usd|usd_|fx_|price_per_credit_usd|exchange_rate' /home/sakib/hive/packages/openai-contract/spec/paths/grants.yaml /home/sakib/hive/apps/web-console/app/console/admin/grants/ /home/sakib/hive/apps/web-console/components/admin/ 2>/dev/null && grep -q 'IsPlatformAdmin' /home/sakib/hive/apps/control-plane/internal/grants/http.go && grep -q 'IsPlatformAdmin' /home/sakib/hive/apps/control-plane/internal/platform/role.go</automated>
  </verify>
  <done>
    grants package compiles + tests pass (RED-first cases all green); single-tx rollback proven; immutability trigger verified at DB layer; owner/non-owner/admin/unauth HTTP matrix green; web-console admin pages render with strict TS (zero `as`/`any`); Playwright admin-credit-grants spec passes for owner happy-path + non-owner negative + FX-leak guard; eight atomic commits landed; OpenAPI grants.yaml has zero customer-USD keys.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 6: Web-console budgets/alerts/invoices pages + Phase 13 carry-forward fixture absorption (HANDOFF-13-01/02)</name>
  <files>
    apps/web-console/lib/control-plane/types.ts,
    apps/web-console/lib/control-plane/client.ts,
    apps/web-console/app/console/billing/budgets/page.tsx,
    apps/web-console/app/console/billing/alerts/page.tsx,
    apps/web-console/app/console/billing/invoices/page.tsx,
    apps/web-console/app/console/billing/invoices/[id]/route.ts,
    apps/web-console/components/billing/budget-form.tsx,
    apps/web-console/components/billing/budget-form.test.tsx,
    apps/web-console/components/billing/spend-alert-form.tsx,
    apps/web-console/components/billing/spend-alert-form.test.tsx,
    apps/web-console/components/billing/invoice-row.tsx,
    apps/web-console/tests/e2e/console-budgets.spec.ts,
    apps/web-console/tests/e2e/console-spend-alerts.spec.ts,
    apps/web-console/tests/e2e/console-invoices.spec.ts,
    apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs
  </files>
  <behavior>
    Test cases (RED first):

    Component vitest:
    - budget-form.test.tsx: render with existing BudgetSettings; soft<=hard validation rejects soft>hard; submit calls client.updateBudget with typed *big.Int-compatible payload (BDT subunits as numeric string OR int64-bounded number).
    - spend-alert-form.test.tsx: threshold dropdown limited to 50/80/100; email + webhook URL fields; submit calls client.createBudgetAlert.

    Playwright:
    - console-budgets.spec.ts: owner sets soft=1000_00 BDT, hard=2000_00 BDT, save -> reload -> values persist. Non-owner -> read-only (form disabled). FX-leak regex zero matches.
    - console-spend-alerts.spec.ts: owner creates 50%/80%/100% alerts with email; rows render; non-owner cannot create. FX-leak regex zero matches.
    - console-invoices.spec.ts: list renders BDT-only; download click -> 200 application/pdf; pdf-parse extraction asserts zero $/USD tokens; non-workspace-member -> 404. FX-leak regex zero matches across page render.

    Carry-forward absorption:
    - HANDOFF-13-01: e2e-auth-fixtures.mjs gains `seedSecondaryWorkspace(ownerEmail) -> {workspaceID, jwt}`. workspace switcher spec (existing apps/web-console/tests/e2e/auth-shell.spec.ts:88) re-runs green using this fixture.
    - HANDOFF-13-02: e2e-auth-fixtures.mjs gains `resetProfileBetweenSpecs()` per-spec hook. profile-completion.spec.ts:71 re-runs green.
  </behavior>
  <action>
    1. Extend `apps/web-console/lib/control-plane/types.ts` with BudgetSettings, SpendAlert, InvoiceRecord (Phase 14 shape — BDT-only). Strict TS, Phase 13 patterns.

    2. Extend `apps/web-console/lib/control-plane/client.ts` with: getBudget, updateBudget, listBudgetAlerts, createBudgetAlert, updateBudgetAlert, deleteBudgetAlert, listInvoices, getInvoice, getInvoicePdfUrl. All typed `<Req, Res>`; zero `as`/`any`.

    3. Create pages:
       - app/console/billing/budgets/page.tsx — server component; reads viewer-gates owner role; renders budget-form.tsx with read-only flag for non-owner.
       - app/console/billing/alerts/page.tsx — list + form for spend alerts; owner-only mutation.
       - app/console/billing/invoices/page.tsx — list invoices for current workspace.
       - app/console/billing/invoices/[id]/route.ts — proxy or signed-URL redirect for PDF download (Phase 14 chooses signed URL via control-plane response).

    4. Components:
       - budget-form.tsx + .test.tsx (vitest).
       - spend-alert-form.tsx + .test.tsx.
       - invoice-row.tsx.

    5. Playwright specs:
       - console-budgets.spec.ts (owner+non-owner; persist; FX-leak guard).
       - console-spend-alerts.spec.ts (CRUD owner-only; FX-leak guard).
       - console-invoices.spec.ts (list + PDF download + pdf-parse text extract assertion zero USD; FX-leak guard).

    6. Carry-forward fixture extensions in `tests/e2e/support/e2e-auth-fixtures.mjs`:
       - `seedSecondaryWorkspace(primaryOwnerEmail)`: provisions a second workspace, returns id + JWT. Closes HANDOFF-13-01.
       - `resetProfileBetweenSpecs(testInfo)`: per-test hook resetting profile-completion state to a known baseline. Closes HANDOFF-13-02.
       - Re-run `auth-shell.spec.ts` and `profile-completion.spec.ts` GREEN (no edits to those spec files unless strictly necessary; if minor selector update needed, document in commit).

    7. Run:
       ```
       cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console npx tsc --noEmit
       cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console npm run test:unit
       cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console npm run build
       cd /home/sakib/hive/apps/web-console && CI=true npx playwright test console-budgets console-spend-alerts console-invoices auth-shell profile-completion --reporter=list
       ```

    8. Strict-TS + FX-leak audit on new web-console surfaces:
       ```
       ! grep -RnE '\bas (any|unknown)\b|: any\b' \
         apps/web-console/app/console/billing/budgets/ \
         apps/web-console/app/console/billing/alerts/ \
         apps/web-console/app/console/billing/invoices/ \
         apps/web-console/components/billing/budget-form.tsx \
         apps/web-console/components/billing/spend-alert-form.tsx \
         apps/web-console/components/billing/invoice-row.tsx
       ! grep -RnE 'amount_usd|\busd_|\bfx_|price_per_credit_usd|exchange_rate' \
         apps/web-console/app/console/billing/budgets/ \
         apps/web-console/app/console/billing/alerts/ \
         apps/web-console/app/console/billing/invoices/ \
         apps/web-console/components/billing/budget-form.tsx \
         apps/web-console/components/billing/spend-alert-form.tsx \
         apps/web-console/components/billing/invoice-row.tsx \
         | grep -v PHASE-17-OWNER-ONLY
       ```

    9. Commits (atomic):
       - `feat(14): types + client extensions — BudgetSettings, SpendAlert, InvoiceRecord` (FIX-14-24)
       - `feat(14): budgets settings page + budget-form` (FIX-14-25)
       - `feat(14): spend alerts CRUD page + form` (FIX-14-26)
       - `feat(14): invoices list page + PDF signed-URL download` (FIX-14-27)
       - `test(14): Playwright console-budgets / spend-alerts / invoices` (FIX-14-28)
       - `test(14): e2e fixtures — seedSecondaryWorkspace + resetProfileBetweenSpecs (HANDOFF-13-01/02)` (FIX-14-29)
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console npx tsc --noEmit && docker compose run --rm web-console npm run test:unit && docker compose run --rm web-console npm run build && cd /home/sakib/hive/apps/web-console && CI=true npx playwright test console-budgets console-spend-alerts console-invoices auth-shell profile-completion --reporter=list && ! grep -RnE '\bas (any|unknown)\b|: any\b' /home/sakib/hive/apps/web-console/app/console/billing/budgets/ /home/sakib/hive/apps/web-console/app/console/billing/alerts/ /home/sakib/hive/apps/web-console/app/console/billing/invoices/ /home/sakib/hive/apps/web-console/components/billing/budget-form.tsx /home/sakib/hive/apps/web-console/components/billing/spend-alert-form.tsx /home/sakib/hive/apps/web-console/components/billing/invoice-row.tsx 2>/dev/null && grep -q 'seedSecondaryWorkspace' /home/sakib/hive/apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs && grep -q 'resetProfileBetweenSpecs' /home/sakib/hive/apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs</automated>
  </verify>
  <done>
    Three new Playwright specs pass; existing auth-shell + profile-completion specs flip green via fixture extensions (HANDOFF-13-01/02 closed); tsc + build + unit tests exit 0; strict-TS + FX-leak grep clean on new surfaces; six atomic commits landed.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 7: FX/USD CI lint primitive + REQUIREMENTS.md PAY-14-01..12 + 14-VERIFICATION.md closure log</name>
  <files>
    packages/openai-contract/scripts/lint-no-customer-usd.mjs,
    packages/openai-contract/scripts/lint-no-customer-usd.test.mjs,
    deploy/docker/docker-compose.yml,
    .planning/REQUIREMENTS.md,
    .planning/phases/14-payments-budget-grant/14-VERIFICATION.md
  </files>
  <behavior>
    Test cases (RED first):

    Lint:
    - lint-no-customer-usd.test.mjs: synthetic spec under tmp dir with `amount_usd` key -> script exit code != 0.
    - synthetic spec with `usd_balance` key -> exit != 0.
    - synthetic spec with `price_per_credit_usd` -> exit != 0.
    - clean spec (BDT only) -> exit 0.
    - script accepts CLI arg list of YAML files; default arg = the four Phase 14 path files.

    REQUIREMENTS.md:
    - PAY-14-01..12 rows added with status `Satisfied`, evidence `[14-VERIFICATION.md](phases/14-payments-budget-grant/14-VERIFICATION.md)`.
    - `bash scripts/verify-requirements-matrix.sh` exits 0.

    14-VERIFICATION.md:
    - Pre-build inventory snapshot vs post-build state.
    - Cron evaluator dry-run output.
    - Hard-cap 402 e2e capture (curl trace OR Playwright failed-request log).
    - Alert idempotency proof (cron run twice -> single delivery).
    - Grant Playwright pass output.
    - Immutability trigger pg-test output.
    - FX lint output (zero matches).
    - tsc + go test + go vet exit codes.
    - Per-route Phase 13 carry-forward closure: HANDOFF-13-01 / 02 / 06 each with linked test + commit hash.
  </behavior>
  <action>
    1. Write `packages/openai-contract/scripts/lint-no-customer-usd.mjs`:
       - ES module Node script.
       - Default args: the four new path files (`spec/paths/budgets.yaml`, `spend-alerts.yaml`, `invoices.yaml`, `grants.yaml`).
       - Accepts `--all` flag to scan every file under `spec/paths/` (Phase 17 will use this; Phase 14 ships the flag but does NOT enable repo-wide enforcement).
       - For each file: parse YAML, walk recursively, collect every `properties.{key}`; fail if any key matches /^(amount_usd|usd_.*|fx_.*|price_per_credit_usd|exchange_rate)$/.
       - Print offending file:path:key on failure; exit 1.

    2. Write `packages/openai-contract/scripts/lint-no-customer-usd.test.mjs`:
       - Spawn child process with synthetic specs in os.tmpdir().
       - Assertions per <behavior>.

    3. Add lint to docker compose `web-console` or new service `contract-lint`:
       - Edit `deploy/docker/docker-compose.yml` to add a one-off run command for the lint script (or wire into existing toolchain profile).
       - Decision: add a `make lint-contracts` Makefile-style npm script in packages/openai-contract/package.json (decision in 14-AUDIT.md).

    4. Update `.planning/REQUIREMENTS.md`:
       - Add new section `## Phase 14 — Payments, Budgets & Discretionary Grant` with rows:
         - **PAY-14-01** Workspace soft + hard budget caps (BDT subunits) — `truths[0]`.
         - **PAY-14-02** Hard-cap enforced on edge-api hot path with 402 + provider-blind body — `truths[1]`.
         - **PAY-14-03** Spend alerts at 50/80/100 thresholds with email + webhook delivery — `truths[2]`.
         - **PAY-14-04** Alert idempotency per (workspace, period, threshold) — `truths[2]`.
         - **PAY-14-05** Monthly invoice cron generates BDT-only PDF per workspace — `truths[3]`.
         - **PAY-14-06** Invoice PDF download zero USD/FX strings — `truths[3]`.
         - **PAY-14-07** Owner-only discretionary credit grant API with grantee lookup — `truths[4]`.
         - **PAY-14-08** Non-owner cannot grant (HTTP 403 + Playwright assertion) — `truths[5]`.
         - **PAY-14-09** credit_grants append-only at schema level (DB trigger) — `truths[6]`.
         - **PAY-14-10** Phase 18 RBAC contract stub via `platform.IsWorkspaceOwner` + `IsPlatformAdmin` — `truths[7]`.
         - **PAY-14-11** Customer-surface FX/USD lint on the four new path files — `truths[8]`.
         - **PAY-14-12** math/big enforced on every BDT subunit arithmetic path — `truths[9]`.
       - Each row: Status `Satisfied`, Evidence `[14-VERIFICATION.md](phases/14-payments-budget-grant/14-VERIFICATION.md)`.

    5. Write `.planning/phases/14-payments-budget-grant/14-VERIFICATION.md`:

       Sections:
       - **Pre-build state** — git log -1 + branch + counts (existing budgets/payments LOC, prior REQUIREMENTS row count).
       - **Schema** — migration applied; trigger present; pg-test transcript for UPDATE/DELETE failure.
       - **Budgets** — go test pass log (control-plane/internal/budgets); cron dry-run two-runs-single-fire transcript.
       - **Edge-api budget gate** — go test pass log (edge-api/internal/limits); 402 e2e capture (curl response body + headers).
       - **Invoices** — go test pass log (control-plane/internal/payments/invoices); PDF text-extract sample (BDT-only).
       - **Grants** — go test pass log (control-plane/internal/grants); single-tx rollback test transcript; immutability trigger transcript.
       - **Web-console** — tsc --noEmit exit 0; npm run build exit 0; vitest exit 0; Playwright pass log for: console-budgets, console-spend-alerts, console-invoices, admin-credit-grants, auth-shell (HANDOFF-13-01 closure), profile-completion (HANDOFF-13-02 closure).
       - **FX-leak guards** — `grep -RnE 'amount_usd|usd_|fx_|price_per_credit_usd|exchange_rate' apps/web-console/app/console/billing/{budgets,alerts,invoices}/ apps/web-console/app/console/admin/grants/ apps/web-console/components/{billing,admin}/ packages/openai-contract/spec/paths/{budgets,spend-alerts,invoices,grants}.yaml | grep -v PHASE-17-OWNER-ONLY` returns empty; `node packages/openai-contract/scripts/lint-no-customer-usd.mjs` exits 0.
       - **Strict TS** — `grep -RnE '\bas (any|unknown)\b|: any\b' apps/web-console/{app,components,lib} | grep -v _test.tsx` returns empty (or matches Phase 13 baseline if any pre-existing).
       - **math/big audit** — `grep -RnE '\bfloat64\b' apps/control-plane/internal/{budgets,grants,payments/invoices,platform}/ apps/edge-api/internal/limits/budget_gate.go | grep -v _test.go` returns empty.
       - **Phase 13 carry-forward closure** — table mapping HANDOFF-13-01/02/06 -> commit hash + spec name + status.
       - **Phase 17 hand-offs preserved** — HANDOFF-13-03 (Invoice.amount_usd at source), HANDOFF-13-04 (CheckoutOptions.price_per_credit_usd) re-listed as Phase 17 work; Phase 14 confirms NO modification to those response shapes.
       - **REQUIREMENTS.md update** — diff snippet showing PAY-14-01..12 rows added.
       - **Branch + ship-gate** — `git rev-parse --abbrev-ref HEAD` returns `a/phase-14-payments-budget-grant`; PR link placeholder.

    6. Run validator:
       ```
       bash /home/sakib/hive/scripts/verify-requirements-matrix.sh
       ```
       Must exit 0.

    7. Commits:
       - `feat(14): customer-USD lint primitive + tests` (FIX-14-30)
       - `chore(14): wire contract-lint script into docker compose` (FIX-14-31)
       - `docs(14): REQUIREMENTS.md PAY-14-01..12 + 14-VERIFICATION.md` (FIX-14-32)
  </action>
  <verify>
    <automated>node /home/sakib/hive/packages/openai-contract/scripts/lint-no-customer-usd.mjs && node --test /home/sakib/hive/packages/openai-contract/scripts/lint-no-customer-usd.test.mjs && bash /home/sakib/hive/scripts/verify-requirements-matrix.sh && test -f /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-VERIFICATION.md && grep -q 'PAY-14-01' /home/sakib/hive/.planning/REQUIREMENTS.md && grep -q 'PAY-14-12' /home/sakib/hive/.planning/REQUIREMENTS.md && grep -q 'HANDOFF-13-01' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-VERIFICATION.md && grep -q 'HANDOFF-13-02' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-VERIFICATION.md && grep -q 'HANDOFF-13-06' /home/sakib/hive/.planning/phases/14-payments-budget-grant/14-VERIFICATION.md && ! grep -RnE 'amount_usd|\busd_|\bfx_|price_per_credit_usd|exchange_rate' /home/sakib/hive/packages/openai-contract/spec/paths/budgets.yaml /home/sakib/hive/packages/openai-contract/spec/paths/spend-alerts.yaml /home/sakib/hive/packages/openai-contract/spec/paths/invoices.yaml /home/sakib/hive/packages/openai-contract/spec/paths/grants.yaml 2>/dev/null</automated>
  </verify>
  <done>
    Lint primitive ships green for the four Phase 14 path files; PAY-14-01..12 rows added to REQUIREMENTS.md with Satisfied + evidence link; verify-requirements-matrix.sh exits 0; 14-VERIFICATION.md records full closure including Phase 13 carry-forward (HANDOFF-13-01/02/06) and Phase 17 preserved hand-offs (HANDOFF-13-03/04); three atomic commits landed.
  </done>
</task>

</tasks>

<verification>
Phase-level checks (run after all tasks complete):

1. Audit-first: `git log --diff-filter=A .planning/phases/14-payments-budget-grant/14-AUDIT.md` precedes any commit touching apps/, supabase/, packages/openai-contract/scripts/.
2. Migration applied: `psql` shows budgets, spend_alerts, invoices, credit_grants tables present + immutability trigger active.
3. Owner gate: `grep -RnE 'platform\.IsWorkspaceOwner|platform\.IsPlatformAdmin' apps/control-plane/internal/{budgets,grants,payments/invoices}/http.go` shows every mutating handler invokes platform/role primitives.
4. credit_grants immutability: pg-test in role_test.go + grants/audit_test.go asserts UPDATE/DELETE raises Postgres error.
5. math/big: `grep -RnE '\bfloat64\b' apps/control-plane/internal/{budgets,grants,payments/invoices,platform}/ apps/edge-api/internal/limits/budget_gate.go | grep -v _test.go` returns empty.
6. Edge-api 402: integration test asserts hard-cap-exceeded request gets 402 with provider-blind body BEFORE provider call.
7. Cron idempotency: budgets/cron_test.go covers two-runs-single-fire (50/80/100 each).
8. Invoice PDF zero USD: pdf_test.go extracts text and asserts no $/USD tokens.
9. Grant Playwright: admin-credit-grants.spec.ts owner happy-path + non-owner negative + FX-leak regex all green.
10. FX lint: `node packages/openai-contract/scripts/lint-no-customer-usd.mjs` exits 0; `node --test packages/openai-contract/scripts/lint-no-customer-usd.test.mjs` green.
11. Strict TS: `npx tsc --noEmit` exits 0; grep guard returns empty on new files.
12. Phase 13 carry-forward: HANDOFF-13-01/02 closed via fixture extension + green specs; HANDOFF-13-06 closed via admin/grants pages.
13. No drift on Phase 17 territory: `git diff main -- apps/control-plane/internal/payments/{types,http,service,repository}.go apps/control-plane/internal/payments/checkout*.go` is empty (excluded files: only NEW sub-package payments/invoices/ + tax.go untouched).
14. Requirements: `bash scripts/verify-requirements-matrix.sh` exits 0; PAY-14-01..12 Satisfied.
15. Branch: `git rev-parse --abbrev-ref HEAD` returns `a/phase-14-payments-budget-grant`.
16. Suite green: full Playwright run on local docker stack; full Go test run on control-plane + edge-api; web-console build exits 0.
</verification>

<success_criteria>
Definition of Done — also serves as v1.1.0 ship-gate input for the payments + grants portion:

- [ ] 14-AUDIT.md committed BEFORE any source-code change; covers all four schema tables, owner-gate design, PDF lib decision, BD VAT note, FIX-14-NN list (~32 fixes), Phase 13 carry-forward absorption.
- [ ] Migration `20260428_01_budgets_alerts_invoices_grants.sql` applies cleanly; immutability trigger blocks UPDATE/DELETE on credit_grants.
- [ ] `apps/control-plane/internal/platform/role.go` exports `IsWorkspaceOwner` + `IsPlatformAdmin` — Phase 18 RBAC contract stub.
- [ ] Budgets soft+hard cap CRUD + spend-alert CRUD + cron evaluator (idempotent per period+threshold) implemented; email + webhook delivery green; HMAC webhook signature.
- [ ] Edge-api hard-cap budget gate enforces 402 with provider-blind body BEFORE provider dispatch; math/big.Cmp comparison; Redis-cached hard_cap with control-plane invalidation on update.
- [ ] Monthly invoice cron generates BDT-only PDF per workspace; gofpdf renderer; PDF text extraction asserts zero $/USD tokens; Supabase Storage `hive-files` bucket write.
- [ ] Discretionary credit grant API (`POST /v1/admin/grants`) owner-only; grantee resolution by email OR phone; single-transaction insert + ledger append; non-owner returns 403; admin (non-platform-admin) returns 403; unauth returns 401.
- [ ] Web-console admin pages (`/console/admin/grants*`, `/console/billing/{budgets,alerts,invoices}*`) ship strict-TS clean (zero `as`/`any`); owner-gated via owner-gate.ts.
- [ ] Playwright specs: console-budgets, console-spend-alerts, console-invoices, admin-credit-grants — all green; FX-leak regex zero matches on every page.
- [ ] Phase 13 carry-forward closed: HANDOFF-13-01 (workspace switcher fixture seed) + HANDOFF-13-02 (dashboard reminder ordering) + HANDOFF-13-06 (discretionary credit UI) — each tracked, each tested.
- [ ] Phase 17 territory untouched: `apps/control-plane/internal/payments/{types,http,service,repository}.go` and `payments/CheckoutOptions` response shape NOT modified; HANDOFF-13-03 / 04 preserved for Phase 17.
- [ ] FX/USD CI lint primitive (`lint-no-customer-usd.mjs`) ships against the four Phase 14 path files; tests green; ready for Phase 17 to extend repo-wide via `--all` flag.
- [ ] math/big enforced on every BDT subunit arithmetic path; float64 grep zero in flagged packages.
- [ ] REQUIREMENTS.md PAY-14-01..12 rows Satisfied; verify-requirements-matrix.sh exits 0.
- [ ] 14-VERIFICATION.md records all sections; CONSOLE Playwright pass; Go test pass; PDF text extraction sample; cron idempotency proof; immutability trigger transcript.
- [ ] Branch `a/phase-14-payments-budget-grant`; single PR opened against main; conventional commits 1:1 with FIX-14-NN ids.
- [ ] Zero chat-app changes (Track A only — Phase 21 consumes Phase 14's grant API later).

Ship-gate mapping: closes Phase 14 master-plan item; unblocks Phase 17 (FX audit inherits the lint primitive + clean Phase 14 surface), Phase 21 (chat-app credited-tier wallet calls `POST /v1/admin/grants`), Phase 25 (BD soft launch — billing flows live).
</success_criteria>

<blockers>
Discovered during planning (2026-04-27):

1. **Owner-gate semantics for grant API** — "owner of which workspace?" Resolved by introducing `is_platform_admin` flag on `accounts.users` (Task 2 + Task 5 update). Phase 18 RBAC will replace flag with tier-aware matrix — signature contract preserved.

2. **gofpdf vs html-to-pdf** — locked decision: gofpdf (pure Go, MIT, no CGO, no headless-browser dep). Documented Task 1.

3. **BD VAT format** — placeholder block ships v1.1.0; refinement post-launch via doc-only PR. NOT a launch blocker (locked decision).

4. **Existing `payments.Invoice` struct collision** — locked decision: NEW sub-package `payments/invoices/` with its own Invoice type. Existing `payments.Invoice` (checkout context) UNTOUCHED — Phase 17 territory.

5. **Hard-cap enforcement layer** — locked decision: edge-api hot path (NOT control-plane), Redis-cached, control-plane refresh-on-write. Latency budget mandates this.

6. **Alert idempotency** — locked decision: (workspace_id, period_start, threshold_pct) tuple stored on spend_alerts; cron compares last_fired_period.

7. **Phase 13 carry-forward absorbed** — HANDOFF-13-01/02/06 are explicit in-scope items. HANDOFF-13-03/04/05 explicitly OUT of scope (Phase 17/18).

8. **No public trial-credit endpoint** — feedback_chatapp_credits.md lock. Phase 21 chat-app calls `POST /v1/admin/grants` only; no auto-grant on signup.

9. **Strict TS** — feedback_strict_typescript.md. Zero `as any` / `as unknown` / ` as ` casts in new files. tsc --noEmit must pass.

10. **Branch discipline** — feedback_no_direct_main_push.md / feedback_branching.md. Work on `a/phase-14-payments-budget-grant`; PR against main.

11. **Local-first** — feedback_local_first.md. Full local docker-compose green BEFORE push. CI = regression net only.

12. **No human verification** — feedback_no_human_verification.md. Every check via Playwright/curl/go test/grep/pdf-parse. No "open browser to verify" anywhere.

13. **Supabase Storage only** — project_no_minio.md. Invoice PDF writes to `hive-files` bucket; no MinIO.

14. **Existing budgets package extension, NOT recreate** — `apps/control-plane/internal/budgets/` already exists. Task 3 EXTENDS, does not start fresh.

15. **Edge-api Redis** — cache key `budget:hard_cap:{ws}` lives on the same Redis instance as Phase 12 KEY-05 rate-limiter. No new Redis dep; reuse existing connection pool.

16. **Cron infrastructure** — confirm during Task 1 audit whether control-plane already has a cron primitive (likely `apps/control-plane/internal/platform/cron.go` or similar). If absent, add a minimal one in Task 3 (still in-scope since budgets cron depends on it). Decision documented in 14-AUDIT.md Section A.
</blockers>

<output>
After completion, create `.planning/phases/14-payments-budget-grant/14-01-SUMMARY.md` per the GSD summary template, recording:
- Files created (migration, three new Go packages: platform, grants, payments/invoices; web-console admin/grants pages, billing/{budgets,alerts,invoices} pages; four Playwright specs; lint primitive; AUDIT, VERIFICATION).
- Files modified (count by area: control-plane / edge-api / web-console / openai-contract).
- Schema tables added (4) + trigger (1).
- New endpoints added (count).
- FIX-14-NN ids closed (full list).
- FX/USD lint output (zero matches on Phase 14 path files).
- Strict-TS violation delta (pre vs post on new files only).
- Phase 13 carry-forward closure: HANDOFF-13-01/02/06 -> commit hashes + spec names.
- Phase 17 hand-offs preserved: HANDOFF-13-03/04 + new HANDOFF-14-* if any discovered.
- PAY-14-01..12 row status update in REQUIREMENTS.md.
- Ship-gate status update for v1.1.0 payments+grants portion.
- Branch + PR link.
</output>
