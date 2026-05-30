# Security P0/P1 Backlog — Triage & Progress (#106–#120)

Started 2026-05-29 after Phase 19 (PR #146) merged. User directive: tackle the
P0/P1 security backlog before continuing the GSD roadmap (Phase 20+), because
these block any real launch of the metered-inference reselling product.

## Status

| Issue | Title | Status | PR / Note |
|-------|-------|--------|-----------|
| #109 | Hardcoded LiteLLM master key fallback | ✅ fixed | PR #150 |
| #110 | edge-api no HTTP timeouts / body limit | ✅ fixed | PR #150 |
| #114 | FX math/big cast to float64 | ✅ fixed | PR #150 |
| #115 | amount_usd leaked in BD checkout | ✅ already fixed (Phase 17) | closed, no code change |
| #117 | No email-verify gate on API key creation | ✅ already fixed (Phase 18 RBAC) | closed, no code change |
| #119 | getSession() server-side auth (3 sites) | ✅ fixed | PR #151 |
| #120 | Email-verify only in layout, not middleware | ✅ fixed | PR #151 |
| #106 | Credit reservation TOCTOU double-spend | ✅ merged | PR #154 |
| #107 | No RLS on tenant tables | ✅ merged | PR #155 — `20260529_01_rls_tenant_tables.sql`; live anon→0 verify at deploy |
| #108 | /internal/* control-plane endpoints unauth | ✅ merged | PR #156 — X-Internal-Token shared-secret middleware (fails closed) |
| #111 | CONTROL_PLANE_BASE_URL leaked into HTML | ✅ FIXED | PR open — server-side Route Handler proxy; form action now relative |
| #112 | SUPABASE_SERVICE_ROLE_KEY silent failure on edge | ⏳ TODO | cross-service (move admin write to control-plane) |
| #113 | Auth snapshot 1hr cache lets revoked keys work | ⏳ TODO | see below |
| #116 | Free-tier abuse (CAPTCHA / IP limit / disposable email) | ⏳ TODO | needs Turnstile infra |

## Remaining — implementation notes (pre-investigated)

### #106 Credit TOCTOU (HIGHEST severity — financial) — ✅ FIXED
- File: `apps/control-plane/internal/accounting/service.go` `CreateReservation` + `ExpandReservation` (both were vulnerable).
- Bug: `GetBalance` and the `ReserveCredits` hold ran in separate txns; N concurrent requests read the same balance, all pass `enforcePolicy` → N× over-reserve.
- Fix shipped: new `AccountLocker` abstraction (`lock.go`, `pglock.go`). The balance-read → policy → reservation-hold critical section now runs inside `WithAccountLock` for both `CreateReservation` and `ExpandReservation`. Production wiring (`cmd/server/main.go`) installs `PgxAccountLocker` = `pg_advisory_lock(hashtext(account_id)::int8)` on a dedicated pooled conn, held across the section, always released — cross-process safe. `NewService` defaults to an in-process locker for single-instance/tests.
- **Deliberately NOT added** the ledger non-negative CHECK/trigger the issue suggested: `PolicyModeTemporaryOverage` intentionally lets available balance go negative within a buffer, so a hard DB constraint would break the overage policy. Policy stays in Go where the buffer logic lives; the advisory lock is the correctness mechanism.
- Tests: `service_concurrency_test.go` — `TestCreateReservationSerializesConcurrentReservations` (acceptance: balance=1000, reserve=50, 100 concurrent → ≤20 succeed, balance never negative), `TestCreateReservationAcquiresAccountLock` (deterministic: lock taken exactly once, keyed on account), `TestNoopLockerOverReserves` (discrimination). Verified: `go build` + `go vet` + `go test -race` (full control-plane suite) → exit 0.
- Follow-up (v1.1 hardening, optional): live ~100-RPS smoke against a real DB to exercise `PgxAccountLocker` end-to-end (unit suite covers the serialization logic via the in-process locker).

### #107 RLS on tenant tables (largest surface) — ✅ PR #155
- Migration `supabase/migrations/20260529_01_rls_tenant_tables.sql`.
- **Schema is bifurcated**: Phase 19 `tenant_*` already had RLS (20260518_04). This migration covers the LEGACY `account_*` family — 34 tables incl `accounts`, `account_*`, `api_key*`, `credit_*`, `payment_*`, `invoices`, `budgets`, `spend_alerts`, `request_attempts`, `usage_events`, `fx_snapshots`, `files`/`uploads`/`upload_parts`/`batches`/`batch_lines`.
- **Mechanism (final, after review)**: each table gets `ENABLE`+`FORCE` RLS + `CREATE POLICY <t>_service_role_all FOR ALL TO hive_app USING(true) WITH CHECK(true)` — mirrors the Phase 19 audit RLS. The app connects as the **non-BYPASSRLS `hive_app`** role (documented in 20260518_04), so the explicit hive_app policy is REQUIRED; a postgres pooler role would also bypass. NO `authenticated` SELECT policy (web-console has zero direct PostgREST reads; a member SELECT would leak `api_keys.token_hash` since RLS is row- not column-level). ⇒ anon/authenticated read 0 rows on every tenant table.
- Acceptance SQL (run post-deploy as anon): `select count(*) from public.credit_ledger_entries;` → 0; control-plane (hive_app) reads/writes unaffected.

### #108 /internal/* shared-secret auth — ✅ PR open
- Control-plane: new `RequireInternalToken` middleware (`internal/platform/http/internalauth.go`, `crypto/subtle` constant-time compare of `X-Internal-Token`). Wired in `router.go` over every `/internal/*` route (catalog/routing/accounting/usage/apikeys) and in `filestore.RegisterRoutes` (files/uploads/batches — now takes a `gate` wrapper). Config: `CONTROL_PLANE_INTERNAL_TOKEN` (`config.go`); `main.go` logs a loud warning when unset.
- Edge-api: new `internal/cpauth` helper (`SetHeader`) attaches `X-Internal-Token` from `CONTROL_PLANE_INTERNAL_TOKEN`; applied to ALL 7 control-plane callers (authz client + resolver, routing, accounting, catalog, files×3, batches×2).
- Fail-mode: token unset ⇒ pass-through + startup warning (no CD breakage during rollout); set on both apps ⇒ enforced. `.env.example` + dev/staging compose updated (passthrough); **ops action: seed `CONTROL_PLANE_INTERNAL_TOKEN` in `/opt/hive/.env` to activate enforcement in staging/prod**.
- Tests: `internalauth_test.go` (4 cases), `cpauth_test.go` (2 cases). build+vet+test both apps → exit 0.

### #113 revoked-key cache invalidation
- Edge caches API-key snapshot in Redis ~1h; revoke doesn't invalidate.
- Fix: versioned cache key `apikey:{hash}:v{gen}` bumped on revoke, OR control-plane Redis pub/sub invalidation consumed by edge, OR TTL ≤60s. Acceptance: revoked key → 401 within ≤60s across instances.
- May touch edge auth path; check overlap with #150 before branching.

### #112 service-role key on Cloudflare Worker edge route
- `apps/web-console/app/auth/callback/route.ts:61-78` — guarded admin write silently skips on Workers when binding missing → users stuck unverified.
- Fix: move admin write to control-plane `POST /internal/users/finalize-signup` (depends on #108 internal-auth being in place); edge only forwards session. Fail loud on control-plane if service-role key absent.

### #111 CONTROL_PLANE_BASE_URL in HTML — ✅ FIXED (PR #157)
- Was: `apps/web-console/app/console/members/page.tsx` invite `<form action="${CONTROL_PLANE_BASE_URL}/...">` inlined the internal URL into rendered HTML and POSTed cross-origin without the session bearer.
- Fixed by server-side Route Handler `app/api/console/members/route.ts` (auth-check → `createInvitation()` helper attaches bearer + base URL server-only → 303 redirect). Form action is now relative `/api/console/members`. Errors map to generic status-class messages (no raw upstream text in URL); redirect resolves against canonical origin (`lib/http/origin.ts`).
- Follow-up (separate): no invite mailer exists in repo — the acceptance token returned by the control-plane is not yet delivered to invitees. Tracked outside #111 (security scope).

### #116 free-tier abuse
- Cloudflare Turnstile on sign-up/sign-in (CLOUDFLARE_API_TOKEN already present), Supabase per-IP signup limit, disposable-domain blocklist, gmail +tag normalization, cap free credits per verified identity.
- Larger, multi-surface; needs product input on free-credit policy.

## Recommended next order
1. ✅ Merge #150 + #151 + #152 + #153 (done).
2. ✅ #106 (TOCTOU) — MERGED (PR #154).
3. ✅ #107 (RLS) — PR #155 open (CI green, threads resolved).
4. ✅ #108 (internal auth) — PR open (`fix/108-internal-endpoint-auth`).
5. ✅ #111 (URL leak) — FIXED (server-side Route Handler proxy `app/api/console/members/route.ts`).
6. #112 (service-role) — after #108 (now merged). **NEXT.**
7. #113 (revoked cache), #116 (abuse).
8. NEW: #51 (P0) — Redis rate-limit fail-open bypass — not in original #106–#120 sweep; triage with this batch.
