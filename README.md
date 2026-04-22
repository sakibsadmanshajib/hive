# Hive

OpenAI-compatible API gateway for the Bangladesh market. v1.0 is a full Go rewrite of the prior stack, shipped for efficiency and operational control: lean hot-path latency, precise `math/big` FX, and source-level control over routing, sanitization, and billing.

- **Provider-agnostic routing** to OpenRouter / Groq (and future providers) via LiteLLM.
- **Prepaid credit billing** on BDT payment rails (Stripe, bKash, SSLCommerz).
- **Developer console** for API-key, billing, and analytics management.

## Architecture

| Path | Role | Language |
|------|------|----------|
| `apps/control-plane` | Accounts, billing, credits, API keys, payments, catalog, routing | Go 1.24 |
| `apps/edge-api` | Auth, rate limiting, inference dispatch, SSE streaming, file/media | Go 1.24 |
| `apps/web-console` | Developer console (billing, keys, analytics, catalog) | Next.js 15 / React 19 / TS 5.8 |
| `packages/openai-contract` | OpenAI spec + support matrix (single source of truth) | — |
| `packages/sdk-tests` | JS / Python / Java SDK integration tests (real OpenAI SDKs) | — |
| `supabase/migrations` | Postgres schema | SQL |
| `deploy/docker` | Compose + Dockerfiles for the stack | — |
| `deploy/litellm` | LiteLLM config (OpenRouter / Groq routing) | — |
| `deploy/{prometheus,grafana,alertmanager}` | Monitoring stack | — |

### Request flow (happy path)

```
client (OpenAI SDK)
   │
   ▼
edge-api  (auth, rate limit, key lookup, sanitize)
   │
   ▼
litellm   (provider routing)
   │
   ▼
OpenRouter / Groq / ...
```

Billing + catalog reads are served by **control-plane**, consumed by both **edge-api** (ledger writes, routing decisions) and **web-console** (server components render billing/account state).

## Tech Stack

| Component | Tech | Version |
|-----------|------|---------|
| control-plane, edge-api | Go | 1.24 |
| web-console | Next.js / React / TypeScript | 15 / 19 / 5.8 |
| Database | Postgres (Supabase-hosted) | — |
| Cache | Redis | 8.4 |
| Model routing | LiteLLM | latest-stable |
| Monitoring | Prometheus + Grafana + Alertmanager | — |
| Payments | Stripe, bKash, SSLCommerz | — |
| Object storage | Supabase Storage (S3-compatible) | — |

## Prerequisites

- Docker (with Compose v2) — everything runs in containers; no host Go/Node required.
- A Supabase project (URL, anon key, service-role key, DB URL).
- At least one LLM provider key (`OPENROUTER_API_KEY` or `GROQ_API_KEY`).
- Supabase Storage S3 protocol enabled; pre-create `hive-files` and `hive-images` buckets.

Payment rail keys (Stripe / bKash / SSLCommerz) are optional — services start without them.

## Getting Started

### 1. Configure environment

```bash
cp .env.example .env
# Fill in:
#   SUPABASE_URL, SUPABASE_ANON_KEY, SUPABASE_SERVICE_ROLE_KEY, SUPABASE_DB_URL
#   NEXT_PUBLIC_SUPABASE_URL, NEXT_PUBLIC_SUPABASE_ANON_KEY
#   S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY, S3_REGION, S3_BUCKET_FILES, S3_BUCKET_IMAGES
#   OPENROUTER_API_KEY or GROQ_API_KEY (at least one)
```

`edge-api` and `control-plane` **fail fast** if required S3 or Supabase vars are missing.

### 2. Apply database migrations

```bash
supabase db push                                # If Supabase CLI is linked
# Or apply supabase/migrations/* in order via the Supabase SQL editor
```

### 3. Start the stack

```bash
cd deploy/docker

# Core stack — edge-api + control-plane + redis + litellm + web-console
docker compose --env-file ../../.env up -d --build

# With monitoring (adds Prometheus, Grafana, Alertmanager)
docker compose --env-file ../../.env --profile monitoring up -d --build
```

### 4. Verify

| Service | URL | Healthcheck |
|---------|-----|-------------|
| Edge API | http://localhost:8080 | `GET /health` |
| Control Plane | http://localhost:8081 | `GET /health` |
| Web Console | http://localhost:3000 | — |
| LiteLLM | http://localhost:4000 | — |
| Prometheus | http://localhost:9090 | `--profile monitoring` |
| Grafana | http://localhost:3001 (`admin/admin`) | `--profile monitoring` |

### 5. Stop the stack

```bash
cd deploy/docker

docker compose down             # Stop services, keep volumes
docker compose down -v          # Stop + remove named volumes (DB / cache / images)
```

## Testing

All tests run through Docker — no host Go / Node required.

### Go unit tests

```bash
cd deploy/docker

docker compose --profile tools run --rm toolchain bash -c \
  "cd /workspace && go test ./apps/control-plane/... -count=1 -short"

docker compose --profile tools run --rm toolchain bash -c \
  "cd /workspace && go test ./apps/edge-api/... -count=1 -short"
```

> **Go workspace gotcha**: with `go.work`, Docker test commands must use full
> module-relative paths (`./apps/control-plane/internal/...`), not short
> `./internal/...` form.

### Frontend unit tests & build

```bash
cd deploy/docker

docker compose run --rm web-console npm run build
docker compose run --rm web-console npm run test:unit
```

### SDK integration tests (JS / Python / Java)

Requires the core stack to be healthy.

```bash
cd deploy/docker
docker compose --env-file ../../.env --profile test up --build
```

### Playwright E2E (web-console)

Web E2E needs the full stack running (web-console SSRs through control-plane
for billing/profile pages).

```bash
# Ensure core stack is up (from `deploy/docker`):
docker compose --env-file ../../.env up -d --build

# Run all E2E specs
cd apps/web-console
npx playwright test

# Specific file
npx playwright test tests/e2e/profile-completion.spec.ts

# Open the HTML report after failures
npx playwright show-report
```

E2E credentials: the fixture script `tests/e2e/support/e2e-auth-fixtures.mjs`
resets dedicated `e2e-*@hive-ci.test` accounts in staging Supabase before each
test. Values default to shared constants in
`tests/e2e/support/e2e-auth-creds.ts`; env overrides
(`E2E_VERIFIED_EMAIL`, `E2E_VERIFIED_PASSWORD`, `E2E_UNVERIFIED_EMAIL`,
`E2E_UNVERIFIED_PASSWORD`, `E2E_INVITATION_TOKEN`) are honored when they meet
minimum length checks (passwords ≥ 6, token ≥ 16); otherwise safe fallbacks are
used. Supabase admin env is still required: `SUPABASE_URL`,
`SUPABASE_SERVICE_ROLE_KEY`.

## Conventions

- **Immutability**: new objects, never mutate existing. Ledger is append-only.
- **Commits**: `<type>: <description>` — `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`.
- **No hardcoded secrets**: env vars only. Never commit `.env`.
- **Provider-blind errors**: sanitize at both control-plane and edge boundaries. Provider names never leak to customers.
- **`math/big` for FX**: all financial calcs use `math/big` to prevent `float64` corruption.
- **Storage**: Supabase Storage is the only object storage backend. `edge-api` and `control-plane` fail fast unless required S3 env vars are present and both `hive-files` + `hive-images` buckets exist at startup.

## Regulatory Rules

**Never show FX rates or currency-exchange language to BD customers.** Applies
to API responses, frontend UI, error messages, and any customer-visible
surface. Omit `amount_usd` from BD payment responses.

## Known Issues

See `.planning/UAT-REPORT.md` for full runtime UAT,
`.planning/phases/10-routing-storage-critical-fixes/10-UAT.md` for Phase 10 UAT
closure, and `.planning/v1.1-DEFERRED-SCOPE.md` for items deferred out of v1.0.

## Project State

- **v1.0 — developer-api-core**: ready to ship. Phases 1–10 complete. Covers
  chat-app + CLI-coding-agent integrators. See `.planning/STATE.md`.
- **v1.1 — deferred**: phases 11–14 plus batch success-path settlement,
  `ensureCapabilityColumns` table fix, `amount_usd` BD checkout. See
  `.planning/v1.1-DEFERRED-SCOPE.md`.

Planning artifacts live in `.planning/`:

- `STATE.md` — current milestone state
- `ROADMAP.md` — full phase breakdown
- `REQUIREMENTS.md` — requirement traceability
- `UAT-REPORT.md` — runtime test results
- `v1.0-MILESTONE-AUDIT.md` — earlier launch-readiness audit
- `v1.1-DEFERRED-SCOPE.md` — deferred scope for next milestone

## Repository Layout

```
apps/                       Go + Next.js services (see Architecture table)
packages/
  openai-contract/          Spec + support matrix (single source of truth)
  sdk-tests/                JS / Python / Java integration suites
supabase/migrations/        Postgres schema (14 migrations)
deploy/
  docker/                   Compose + Dockerfiles
  litellm/                  LiteLLM config
  prometheus/               Prometheus + alert rules
  grafana/                  Dashboards + provisioning
  alertmanager/             Alert routing
scripts/                    One-off operational scripts
docs/                       Hand-written docs + generated codemaps
```
