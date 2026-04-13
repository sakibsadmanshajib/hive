# Hive — OpenAI-Compatible API Platform

## What This Is

Hive is an OpenAI-compatible API gateway targeting the Bangladesh market. It proxies LLM requests through provider-agnostic routing, handles prepaid credit billing with BDT payment rails, and exposes a developer console for key/billing management.

## Architecture

```
apps/control-plane    Go backend — accounts, billing, credits, API keys, payments, catalog, routing
apps/edge-api         Go edge proxy — auth, rate limiting, inference dispatch, SSE streaming, file/media
apps/web-console      Next.js frontend — developer console (billing, keys, analytics, catalog)
packages/openai-contract  OpenAI spec + support matrix (single source of truth)
packages/sdk-tests    JS/Python/Java SDK integration tests (use real OpenAI SDKs)
supabase/migrations   Postgres schema (Supabase-hosted)
deploy/docker         Docker Compose stack + Dockerfiles
deploy/litellm        LiteLLM config (model routing to OpenRouter/Groq)
deploy/prometheus     Prometheus + alert rules
deploy/grafana        Dashboards + provisioning
deploy/alertmanager   Alert routing
```

## Starting the Stack

### Prerequisites
- Docker + Docker Compose
- A `.env` file (copy from `.env.example` and fill in real keys)

### Required Environment Variables

```bash
# Supabase (REQUIRED — the DB backend)
SUPABASE_URL=https://your-project.supabase.co
SUPABASE_ANON_KEY=your-anon-key
SUPABASE_SERVICE_ROLE_KEY=your-service-role-key
SUPABASE_DB_URL=postgresql://postgres:password@db.your-project.supabase.co:5432/postgres

# Web console (REQUIRED for frontend)
NEXT_PUBLIC_SUPABASE_URL=https://your-project.supabase.co
NEXT_PUBLIC_SUPABASE_ANON_KEY=your-anon-key

# LLM providers (at least one REQUIRED for inference)
OPENROUTER_API_KEY=your-key
GROQ_API_KEY=your-key
OPENROUTER_DEFAULT_MODEL=anthropic/claude-sonnet-4
OPENROUTER_AUTO_MODEL=openrouter/auto
OPENROUTER_FAST_FALLBACK_MODEL=anthropic/claude-haiku-4
GROQ_FAST_MODEL=llama-3.3-70b-versatile

# Payment rails (OPTIONAL — service starts without them, enables rails as configured)
STRIPE_SECRET_KEY=sk_test_...
STRIPE_WEBHOOK_SECRET=whsec_...
BKASH_APP_KEY=...
BKASH_APP_SECRET=...
BKASH_USERNAME=...
BKASH_PASSWORD=...
BKASH_BASE_URL=https://tokenized.sandbox.bka.sh/v1.2.0-beta
SSLCOMMERZ_STORE_ID=...
SSLCOMMERZ_STORE_PASSWD=...
SSLCOMMERZ_BASE_URL=https://sandbox.sslcommerz.com
XE_ACCOUNT_ID=...
XE_API_KEY=...
CONTROL_PLANE_PUBLIC_URL=http://localhost:8081
```

### Running Services

```bash
cd deploy/docker

# Core stack: edge-api + control-plane + redis + litellm + web-console
docker compose up --build

# With monitoring (Prometheus + Grafana + Alertmanager)
docker compose --profile monitoring up --build

# Run SDK integration tests (requires core stack healthy first)
docker compose --profile test up --build

# Just the toolchain container (for go vet, formatting, etc.)
docker compose --profile tools run toolchain bash
```

### Service Endpoints (local)

| Service | URL |
|---------|-----|
| Edge API (OpenAI-compatible) | http://localhost:8080 |
| Control Plane | http://localhost:8081 |
| Web Console | http://localhost:3000 |
| LiteLLM proxy | http://localhost:4000 |
| Prometheus | http://localhost:9090 (monitoring profile) |
| Grafana | http://localhost:3001 (admin/admin, monitoring profile) |

### Applying Migrations

Migrations are in `supabase/migrations/` and must be applied to your Supabase project:

```bash
# Using Supabase CLI (if linked to your project)
supabase db push

# Or manually apply in order via Supabase SQL editor
```

Note: The `filestore` package (files, uploads, batches tables) uses Go auto-schema (`ensureSchema`) rather than Supabase migrations. These tables are created automatically when the control-plane starts.

## Known Issues Found During v1.0 Audit

### Bugs

1. **`ensureCapabilityColumns` targets wrong table** (Phase 07 regression)
   - File: `apps/control-plane/internal/routing/repository.go`
   - Bug: ALTER TABLE targets `route_capabilities` but actual table is `provider_capabilities`
   - Impact: 5 media capability columns (image_generation, image_edit, tts, stt, batch) are never added. ListRouteCandidates queries these columns, causing runtime SQL errors for media routing.
   - Fix: Change table name to `provider_capabilities`, or better: add a proper migration.

2. **BD regulatory: `amount_usd` exposed in API response** (Phase 08)
   - File: `apps/control-plane/internal/payments/http.go` lines 105-115
   - Bug: `initiateResponse` returns both `amount_usd` and `amount_local` in JSON. A BD customer inspecting network traffic can derive the FX rate.
   - Impact: Regulatory risk — BD customers must never see FX rates or currency exchange language.
   - Fix: Omit `amount_usd` from response when rail is bkash/sslcommerz, or always omit since frontend never uses it.

3. **File storage not wired to Supabase Storage** (Phase 07 / UAT)
   - File/media endpoints use Supabase Storage buckets (`hive-files`, `hive-images`).
   - Edge-api currently degrades gracefully (file/media endpoints disabled, server still starts).
   - Fix: Phase 10 replaces minio-go client with Supabase Storage REST API.

### Cleanup Items

4. **Generated OpenAPI contract branding** (Phase 01)
   - `packages/openai-contract/generated/hive-openapi.yaml` still has `info.title: "OpenAI API"` and description referencing `platform.openai.com`. Per-operation `x-oaiMeta` blocks (296 occurrences) contain OpenAI doc links.
   - Fix: Override title/description in `sync_hive_contract.py` and strip all `x-oaiMeta`.

4. **Dead code: `NeedCompletions` flag** (Phase 06)
   - `apps/edge-api/internal/inference/types.go` — `NeedCompletions` field exists in `NeedFlags` but is never read. Completions route through `NeedChatCompletions`.

5. **Mixed logging styles** (Phase 09)
   - `main.go` uses `log.Printf`/`log.Fatalf`; newer packages use `log/slog`. Should standardize on slog.

6. **Filestore auto-schema divergence** (Phase 07)
   - Filestore tables created via Go `ensureSchema` instead of Supabase migrations. Diverges from the rest of the project.

7. **.env.example missing payment/FX keys**
   - `STRIPE_*`, `BKASH_*`, `SSLCOMMERZ_*`, `XE_*`, `SUPABASE_DB_URL`, `CONTROL_PLANE_PUBLIC_URL` are used in docker-compose but not listed in `.env.example`.

## Lessons Learned (Self-Audit)

### Architecture Patterns Worth Keeping

- **Immutable append-only ledger**: Balance derived from SUM of deltas, not a mutable counter. Eliminates race conditions.
- **Single-snapshot catalog**: Control-plane is sole source of truth; edge projects from it. Prevents catalog drift.
- **Provider-blind errors**: Sanitization at both control-plane and edge boundaries. Provider names never leak.
- **Reserve/finalize/release accounting**: Applied uniformly across all inference and media endpoints. Defer-based cleanup prevents leaked reservations.
- **PaymentRail interface**: Adding new payment rails requires zero changes to service.go.
- **FXCache interface over Redis**: Enables full unit testing without external dependencies.
- **math/big for FX arithmetic**: Prevents float64 corruption in financial calculations.
- **CompareAndSetStatus with RETURNING**: Race-safe state machine transitions without distributed locks.
- **Custom Prometheus registry per service**: Avoids polluting /metrics with Go runtime noise.
- **UUID normalization in metrics middleware**: Prevents cardinality explosion.
- **Adapter pattern for infrastructure**: Isolates handler logic from auth/routing/accounting dependencies.

### Go Workspace Gotcha
When a `go.work` has multiple modules, Docker test commands must use the full module-relative path (`./apps/control-plane/internal/...`) when running from the workspace root, not the short `./internal/...` form.

### Mistakes to Avoid

- **Runtime ALTER TABLE instead of migrations**: `ensureCapabilityColumns` used wrong table name and errors were silently swallowed. Always use proper SQL migrations for schema changes.
- **Silent error swallowing**: `_ = err` on schema operations hid the wrong-table-name bug. Never swallow errors on data-definition operations.
- **Endpoint count inaccuracies in summaries**: 07-01 claims 15 endpoints but actual count is 14. Self-check counts in summaries should be machine-verified, not hand-counted.
- **Returning unused data in API responses**: `amount_usd` in BD checkout responses creates regulatory risk even though the frontend ignores it. API responses should only include data the consumer needs.
- **Incomplete .env.example**: Missing 13+ env vars that docker-compose uses. The example file should be the authoritative list of all configuration.
- **Branding leakage in generated artifacts**: OpenAPI contract still branded as "OpenAI API". Generated files need post-generation validation for branding.
- **Fatal crashes on optional dependencies**: `log.Fatalf` on S3 init failure killed edge-api. Optional infrastructure should degrade gracefully (`log.Printf` + skip routes), not crash the server.
- **API field names drift from plans**: Plans said `name` for API key creation, but actual handler requires `nickname`. Verify field names against handler code, not just plan documents.
- **Docker Compose .env path**: Running from `deploy/docker/` requires `--env-file ../../.env`. Document exact commands with all flags.

## Regulatory Rules

- **NEVER show FX rates or currency exchange language to BD customers.** This applies to API responses, frontend UI, error messages, and any customer-visible surface. The frontend enforces this; the backend must too.

## Testing

### Unit Tests (Go)
```bash
cd apps/control-plane && go test ./... -count=1 -short
cd apps/edge-api && go test ./... -count=1 -short
```

### SDK Integration Tests (require running stack)
```bash
cd deploy/docker && docker compose --profile test up --build
```

### Frontend
```bash
cd apps/web-console && npm run build   # Type check + build
cd apps/web-console && npm test        # Unit tests
```

### Runtime UAT Results (2026-04-13)
Full report: `.planning/UAT-REPORT.md`

- [x] Docker Compose stack starts and all healthchecks pass (8 services, no MinIO)
- [x] Health endpoints respond on edge-api and control-plane
- [x] Supabase auth works (sign-up, email confirm, sign-in, JWT validation)
- [x] Auto-workspace provisioning on first login
- [x] API key lifecycle (create, list, policy, revoke, edge auth, revocation enforcement)
- [x] Model catalog returns 3 aliases, provider-blind
- [x] OpenAI-compatible error format on all error paths
- [x] Compat headers (x-request-id, openai-version, openai-processing-ms)
- [x] Support matrix rejects unsupported endpoints with correct error types
- [x] Credits balance and ledger functional
- [x] Payment rails listing (Stripe active)
- [x] Prometheus metrics on both services
- [x] Grafana + Alertmanager healthy
- [x] Web console auth pages serve HTML
- [ ] **BLOCKED: Inference (502 routing error)** — `ensureCapabilityColumns` wrong table name
- [ ] File/image/audio/batch endpoints — S3 not connected (minio-go path issue)
- [ ] `go build ./...` / `go test ./...` in Docker toolchain
- [ ] `npm run build` for web-console
- [ ] SDK integration tests end-to-end
- [ ] Payment checkout flow with Stripe test mode
- [ ] Web console authenticated pages (needs Playwright)
