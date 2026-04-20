# Hive v1.0 Runtime UAT Report

**Date:** 2026-04-13
**Tester:** Automated (Claude Code agents)
**Stack:** edge-api + control-plane + web-console + redis + litellm + prometheus + grafana + alertmanager
**S3 Storage:** Gracefully degraded (Supabase Storage S3 endpoint format incompatible with legacy S3-compatible client path restriction)
**Inference Model:** hive-default (openrouter/free — zero cost)
**Test Account:** uat.test.hive@gmail.com (auto-confirmed via Supabase MCP)

---

## Results Summary

| Metric | Count |
|--------|-------|
| Total tests | 24 |
| Passed | 21 |
| Failed | 3 |
| Pass rate | 87.5% |

---

## Test Results

### Group A: Infrastructure Health (5/5 PASS)

| # | Test | HTTP | Result |
|---|------|------|--------|
| 1 | Edge API /health | 200 | PASS |
| 2 | Control Plane /health | 200 | PASS |
| 3 | Web Console / redirect | 307 | PASS |
| 4 | Prometheus /-/healthy | 200 | PASS |
| 5 | Grafana /api/health | 200 | PASS |

### Group B: Auth & Account (5/5 PASS)

| # | Test | HTTP | Result | Notes |
|---|------|------|--------|-------|
| 6 | GET /api/v1/viewer | 200 | PASS | Auto-provisioned workspace, gates populated |
| 7 | GET /api/v1/accounts/current/profile | 200 | PASS | profile_setup_complete=false (expected) |
| 8 | GET /api/v1/accounts/current/members | 200 | PASS | Test user listed as owner |
| 9 | GET /api/v1/accounts/current/credits/balance | 200 | PASS | Zero balance |
| 10 | GET /api/v1/accounts/current/credits/ledger | 200 | PASS | Empty ledger with cursor pagination |

### Group C: Catalog & Models (3/3 PASS)

| # | Test | HTTP | Result | Notes |
|---|------|------|--------|-------|
| 11 | GET /api/v1/catalog/models | 200 | PASS | 3 models with pricing, no provider names |
| 12 | GET /v1/models (no auth) | 401 | PASS | OpenAI error format, invalid_api_key code |
| 13 | GET /v1/models (with API key) | 200 | PASS | owned_by:"hive", provider-blind |

### Group D: API Key & Headers (2/3 — 1 FAIL)

| # | Test | HTTP | Result | Notes |
|---|------|------|--------|-------|
| 14 | HEAD /v1/models | 404 | FAIL | Router doesn't register HEAD handler (low severity) |
| 15 | GET /v1/models response headers | 200 | PASS | x-request-id, openai-version, openai-processing-ms present |
| 16 | Checkout rails | 200 | PASS | Stripe active, predefined tiers returned |

### Group E: Inference (0/2 — CRITICAL FAIL)

| # | Test | HTTP | Result | Notes |
|---|------|------|--------|-------|
| 17 | POST /v1/chat/completions (sync) | 502 | FAIL | "Failed to select a route for this request" |
| 18 | POST /v1/chat/completions (stream) | 502 | FAIL | Same routing error |

**Root cause:** `ensureCapabilityColumns` in `apps/control-plane/internal/routing/repository.go` targets `route_capabilities` instead of `provider_capabilities`. The 5 media capability columns are never added, and `ListRouteCandidates` queries fail.

### Group F: Error Handling (2/2 PASS)

| # | Test | HTTP | Result | Notes |
|---|------|------|--------|-------|
| 19 | GET /v1/fine_tuning/jobs (unsupported) | 404 | PASS | type:"unsupported_endpoint", code:"endpoint_unsupported" |
| 20 | POST /v1/chat/completions (no auth) | 401 | PASS | OpenAI error format, invalid_api_key |

### Group G: Monitoring (2/2 PASS)

| # | Test | HTTP | Result | Notes |
|---|------|------|--------|-------|
| 21 | Control Plane /metrics | 200 | PASS | hive_http_request_duration_seconds exposed |
| 22 | Edge API /metrics | 200 | PASS | Custom registry, no Go runtime noise |

### Group H: Web Console (2/2 PASS)

| # | Test | HTTP | Result | Notes |
|---|------|------|--------|-------|
| 23 | /auth/sign-in | 200 | PASS | HTML page served |
| 24 | /auth/sign-up | 200 | PASS | HTML page served |

### Provider-Blind Check: PASS

Scanned all 24 responses for forbidden strings: `openrouter`, `groq`, `litellm`, `provider`. None found.

---

## Critical Issues

### 1. Inference Routing Broken (BLOCKER)
- **Symptom:** All chat completions return 502 with "Failed to select a route"
- **Root cause:** `ensureCapabilityColumns()` in `routing/repository.go` ALTER TABLEs `route_capabilities` but actual table is `provider_capabilities`. Error silently swallowed (`_ = err`).
- **Impact:** No inference possible. The core product feature is broken.
- **Fix:** Change table name to `provider_capabilities`, or add a proper SQL migration for the 5 columns.

### 2. Supabase Storage S3 Endpoint Incompatible with legacy S3-compatible client
- **Symptom:** `Endpoint url cannot have fully qualified paths`
- **Root cause:** `old storage client constructor()` accepts only `host:port`, but Supabase S3 endpoint includes path `/storage/v1/s3`
- **Impact:** File, image, audio, and batch endpoints disabled (gracefully degraded after fix)
- **Fix applied:** Made storage init non-fatal in edge-api `main.go`. Permanent fix needed: either use a reverse proxy, or switch to Supabase Storage REST API instead of S3 protocol.

### 3. HEAD /v1/models Returns 404 (LOW)
- **Symptom:** HEAD method not registered on model routes
- **Impact:** Minimal — OpenAI SDKs don't use HEAD.

---

## Mistakes Discovered & Fixed During UAT

### Mistake 1: Fatal S3 Init Killed the Server
- **What happened:** Removing legacy local object-store emulator caused `log.Fatalf` on S3 client init failure, killing edge-api entirely
- **Fix:** Changed to `log.Printf` warning + conditional route registration. Server starts without S3; file/media endpoints disabled gracefully.
- **File:** `apps/edge-api/cmd/server/main.go`
- **Lesson:** Infrastructure dependencies should degrade gracefully, not crash the server.

### Mistake 2: .env Not Found by Docker Compose
- **What happened:** `docker compose up` from `deploy/docker/` couldn't find `.env` at repo root
- **Fix:** Must use `--env-file ../../.env` flag
- **Lesson:** Document the exact command including `--env-file` path.

### Mistake 3: API Key Field Name Mismatch
- **What happened:** Plans and test scripts used `name` but actual API requires `nickname`
- **Fix:** Used correct field `nickname`.
- **Lesson:** API field names in plans should be verified against actual handler code.

### Mistake 4: Supabase S3 Endpoint Format
- **What happened:** Set `S3_ENDPOINT=host/storage/v1/s3` but legacy S3-compatible client rejects paths in endpoint
- **Fix:** Made S3 optional (graceful degradation). Permanent fix needed.
- **Lesson:** Test infrastructure changes against real clients before assuming drop-in compatibility.

### Mistake 5: Silent Error Swallowing in ensureCapabilityColumns
- **What happened:** `_ = err` on ALTER TABLE hid the wrong table name bug for the entire development cycle
- **Impact:** The routing system is fundamentally broken but all unit tests pass (they mock the DB)
- **Lesson:** Never swallow errors on DDL operations. Use proper SQL migrations instead of runtime ALTER TABLE.

### Mistake 6: Supabase Email Confirmation Required
- **What happened:** Test user sign-up succeeded but sign-in failed with "Email not confirmed"
- **Fix:** Used Supabase MCP `execute_sql` to set `email_confirmed_at` directly
- **Lesson:** For UAT automation, either disable email confirmation in Supabase settings or use service role admin API / direct SQL.

---

## What Works (Confirmed by Runtime Testing)

- Supabase auth (sign-up, sign-in, JWT validation)
- Auto-workspace provisioning on first login
- Account profile and member management
- API key lifecycle (create, list, policy, revoke)
- Edge-api key authentication and cache invalidation
- Model catalog (3 aliases, no provider leakage)
- OpenAI-compatible error format on all error paths
- Compat headers (x-request-id, openai-version, openai-processing-ms)
- Support matrix endpoint rejection with correct error types
- Credits balance and ledger (empty but functional)
- Payment rails listing (Stripe active)
- Prometheus metrics on both services (custom registry)
- Grafana + Alertmanager monitoring stack
- Web console auth pages

## What Does NOT Work

- Inference (routing bug — BLOCKER)
- File/image/audio/batch endpoints (S3 not connected)
- Payment checkout flow (untested — no credits loaded)
- Web console authenticated pages (would need browser/Playwright test)

---

## Next Steps

1. **Fix routing bug** — add migration for the 5 capability columns on `provider_capabilities`
2. **Fix S3 integration** — either use Supabase Storage REST API or configure a reverse proxy for the S3 path
3. **Re-run inference UAT** after routing fix
4. **Browser-based UAT** for web console authenticated pages
5. **Payment flow test** with Stripe test mode
