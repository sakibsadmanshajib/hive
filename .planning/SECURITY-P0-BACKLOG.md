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
| #112 | SUPABASE_SERVICE_ROLE_KEY silent failure on edge | ✅ FIXED | PR open — authed control-plane `POST /api/v1/.../email-verification/finalize`; edge forwards session bearer only, service-role key off the edge |
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

### #113 revoked-key cache invalidation — ✅ FIXED (PR open)
- Was stated as "revoke doesn't invalidate", but recon found active invalidation ALREADY wired and correct: control-plane `apikeys.Service` calls `invalidateSnapshot(tokenHash)` on revoke/disable/rotate, deleting `snapshotRedisKey = "auth:key:{<hash>}"` — byte-identical to the edge cache key (`authz/client.go:91`) — on the shared Redis (`main.go:257` `NewRedisSnapshotCache(redisClient)`). So revoke cutoff is already ~immediate.
- Remaining gap = the **backstop**: edge set the snapshot TTL to **1h**, so if the active DELETE is ever missed (transient Redis error, instance divergence) a revoked key could authorize for up to an hour. Fixed by lowering the edge snapshot TTL to **60s** (`const snapshotTTL = 60 * time.Second`, `authz/client.go`). Acceptance (revoked → 401 within ≤60s across instances) now holds even in the worst case.
- Verified: edge authz unit test asserts the cached-snapshot Set TTL is a positive ≤60s value. (Go verification is via CI "Go tests (edge-api)" — local toolchain output is unobservable in this env.)

### #112 service-role key on Cloudflare Worker edge route — ✅ FIXED (PR open)
- Was: `apps/web-console/app/auth/callback/route.ts` guarded admin write (`if process.env.SUPABASE_SERVICE_ROLE_KEY` + `.catch(()=>undefined)`) silently skipped on Workers when the key was missing → users stuck unverified, plus a god-key in a public edge bundle.
- Fixed by an **authenticated** control-plane endpoint `POST /api/v1/accounts/current/email-verification/finalize` (`internal/identity`). Edge forwards only the user session bearer (no service-role key, no internal token on the edge). The control-plane flips `hive_email_verified` via its service-role DB pool and **only when `email_confirmed_at IS NOT NULL`** (Supabase already confirmed) — a caller cannot self-verify an unconfirmed address. Write errors are loud 500 (never a silent no-op); 0 rows → 409.
- Deviation from original plan (internal `finalize-signup` endpoint): chose an authed `/api/v1` endpoint instead, so the edge holds neither the service-role key nor the #108 internal token — strictly less privilege on the public edge. The session bearer is itself the proof of email ownership (issued post code-exchange).
- Ops: remove `SUPABASE_SERVICE_ROLE_KEY` from the web-console/Cloudflare env; ensure `CONTROL_PLANE_BASE_URL` points at the public control-plane URL there.

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
6. ✅ #112 (service-role) — FIXED (authed control-plane finalize endpoint; service-role off the edge).
7. ✅ #113 (revoked cache) — FIXED (active invalidation already wired; lowered edge backstop TTL 1h→60s). Then #51 (rate-limit fail-open) **NEXT**, #116 (abuse).
8. NEW: #51 (P0) — Redis rate-limit fail-open bypass — not in original #106–#120 sweep; triage with this batch.
