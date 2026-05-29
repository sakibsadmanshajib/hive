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
| #106 | Credit reservation TOCTOU double-spend | ⏳ TODO | see below |
| #107 | No RLS on tenant tables | ⏳ TODO | see below |
| #108 | /internal/* control-plane endpoints unauth | ⏳ TODO | see below |
| #111 | CONTROL_PLANE_BASE_URL leaked into HTML | ⏳ TODO | conflicts w/ #151 members/page — do after #151 merges |
| #112 | SUPABASE_SERVICE_ROLE_KEY silent failure on edge | ⏳ TODO | cross-service (move admin write to control-plane) |
| #113 | Auth snapshot 1hr cache lets revoked keys work | ⏳ TODO | see below |
| #116 | Free-tier abuse (CAPTCHA / IP limit / disposable email) | ⏳ TODO | needs Turnstile infra |

## Remaining — implementation notes (pre-investigated)

### #106 Credit TOCTOU (HIGHEST severity — financial)
- File: `apps/control-plane/internal/accounting/service.go` `CreateReservation` (L50–110).
- Bug: `GetBalance` (L63) and `repo.CreateReservation` (L85) run in separate txns; N concurrent requests read same balance, all pass `enforcePolicy` (L426) → N× over-reserve.
- Fix: single txn with `pg_advisory_xact_lock(hashtext(account_id::text))` (or `SELECT ... FOR UPDATE` on an account lock row) around balance-read + policy + insert. Add a ledger CHECK / partial-sum constraint as defence in depth.
- Needs: repo transactional path (check `repository.go` for tx support), and a `-race` + ~100-RPS single-account smoke test (acceptance: balance=1000, reserve=50 → ≤20 succeed).
- Do on its own branch; no file conflicts with #150/#151.

### #107 RLS on tenant tables (largest surface)
- No `ENABLE ROW LEVEL SECURITY` on any customer table across 21 migrations.
- Tables: accounts, account_profiles, account_memberships, api_keys, api_key_policies, credits_ledger, credit_reservations, payment_intents, invoices, budget_thresholds, usage_*, files, uploads, upload_parts, batches.
- Pattern: enable+force RLS; policy `auth.uid()` joined through `account_memberships`; service-role bypass for control-plane writes. Wrap `auth.uid()`/`auth.jwt()` in `(SELECT ...)` per-row-perf (learned in Phase 19 M5).
- Acceptance: published anon key `select * from credits_ledger` → 0 rows unauth; control-plane writes still succeed.
- New migration `supabase/migrations/<date>_rls_tenant_tables.sql`. No code conflicts.

### #108 /internal/* shared-secret auth
- Routes (`POST /internal/apikeys/resolve`, `/internal/accounting/reservations`, etc.) have no middleware.
- Fix: `X-Internal-Token` header middleware on all `/internal/*`; new `CONTROL_PLANE_INTERNAL_TOKEN` env. Edge-api sets it on outgoing calls (`apps/edge-api/internal/authz/client.go`, `authz.go`).
- ⚠️ Edge-api `cmd/server/main.go` wiring CONFLICTS with PR #150 — do after #150 merges.

### #113 revoked-key cache invalidation
- Edge caches API-key snapshot in Redis ~1h; revoke doesn't invalidate.
- Fix: versioned cache key `apikey:{hash}:v{gen}` bumped on revoke, OR control-plane Redis pub/sub invalidation consumed by edge, OR TTL ≤60s. Acceptance: revoked key → 401 within ≤60s across instances.
- May touch edge auth path; check overlap with #150 before branching.

### #112 service-role key on Cloudflare Worker edge route
- `apps/web-console/app/auth/callback/route.ts:61-78` — guarded admin write silently skips on Workers when binding missing → users stuck unverified.
- Fix: move admin write to control-plane `POST /internal/users/finalize-signup` (depends on #108 internal-auth being in place); edge only forwards session. Fail loud on control-plane if service-role key absent.

### #111 CONTROL_PLANE_BASE_URL in HTML
- `apps/web-console/app/console/members/page.tsx:126` — invite `<form action="${CONTROL_PLANE_BASE_URL}/...">` inlines internal URL.
- Fix: Next.js Route Handler `app/api/console/members/route.ts` proxying server-side (mirrors other mutations). ⚠️ same file as PR #151 — do after #151 merges.

### #116 free-tier abuse
- Cloudflare Turnstile on sign-up/sign-in (CLOUDFLARE_API_TOKEN already present), Supabase per-IP signup limit, disposable-domain blocklist, gmail +tag normalization, cap free credits per verified identity.
- Larger, multi-surface; needs product input on free-credit policy.

## Recommended next order
1. Merge #150 + #151.
2. #106 (TOCTOU) — own branch, financial-critical, needs DB race test.
3. #107 (RLS) — own branch, big migration.
4. #108 (internal auth) — after #150 merged (main.go).
5. #111 (URL leak) — after #151 merged (members/page.tsx).
6. #112 (service-role) — after #108.
7. #113 (revoked cache), #116 (abuse).
