# Hive

![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white) ![Next.js](https://img.shields.io/badge/Next.js-15-000000?logo=nextdotjs&logoColor=white) ![License](https://img.shields.io/badge/License-MIT-blue.svg) [![Live demo](https://img.shields.io/badge/Live_demo-chat--hive.scubed.co-blue)](https://chat-hive.scubed.co)

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
| `deploy/{prometheus,grafana}` | Monitoring stack. Alert routing is inline in `deploy/docker/docker-compose.yml`, because Alertmanager cannot read environment variables from a config file | — |

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

## Quick Start

```bash
# Install the Hive CLI (PR #202 install method)
curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash
```

## Prerequisites

- Docker (with Compose v2) — everything runs in containers; no host Go/Node required.
- A Supabase project (URL, anon key, service-role key, DB URL).
- At least one LLM provider key (`OPENROUTER_API_KEY` or `GROQ_API_KEY`).
- Supabase Storage S3 protocol enabled; pre-create `hive-files` and `hive-images` buckets.

Payment rail keys (Stripe / bKash / SSLCommerz) are optional — services start without them.

## EnterpriseEdge: One-Line Install

Deploy a full self-hosted Hive box on any Ubuntu/Debian server (x86_64 or arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash
```

With local Ollama inference:

```sh
curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash -s -- --with-ollama
```

The installer handles Docker setup, repo clone/update, `.env` configuration wizard, and health-checked stack launch. See [`scripts/README.md`](scripts/README.md) for flags, non-interactive usage, and uninstall instructions.

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

# local: core stack + in-stack Redis + Open WebUI + Caddy (default for local dev).
# Open WebUI requires its own shim-key configuration; see deploy/docker/docker-compose.yml.
docker compose --env-file ../../.env --profile local up -d --build

# cloud: core services expecting managed Upstash Redis (set REDIS_URL=rediss://... in .env)
docker compose --env-file ../../.env --profile cloud up -d --build

# enterprise: core + in-stack Redis + Open WebUI + Caddy (self-hosted single box)
docker compose --env-file ../../.env --profile enterprise up -d --build

# chat: add Open WebUI + Caddy on top of cloud profile
docker compose --env-file ../../.env --profile cloud --profile chat up -d --build

# monitoring: add Prometheus, Grafana, Alertmanager to any profile
docker compose --env-file ../../.env --profile local --profile monitoring up -d --build
```

### 4. Verify

| Service | URL | Healthcheck |
|---------|-----|-------------|
| Edge API | http://localhost:8080 | `GET /health` |
| Control Plane | http://localhost:8081 | `GET /health` |
| Web Console | http://localhost:3000 | — |
| LiteLLM | http://localhost:4000 | — |
| Open WebUI (direct) | http://localhost:3002 | `--profile local` |
| Caddy (OWUI proxy) | http://localhost:3003 | `--profile local` |
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
seeds dedicated fixture accounts in Supabase before each test. Three values are
required and have no default, because a default here is a credential committed
to a public repository that the seeder then writes onto a live account:

```bash
export E2E_RUN_KEY="$(whoami)-$(date +%s)"   # any value unique to this run
export E2E_VERIFIED_PASSWORD=...             # at least 6 characters
export E2E_UNVERIFIED_PASSWORD=...           # at least 6 characters
export E2E_INVITATION_TOKEN=...              # at least 16 characters
```

A missing one fails the run and names itself. `E2E_RUN_KEY` is required as
well, and for a stronger reason than the rest: seeding writes a password to
whatever address it is given, so every fixture address is namespaced with the
run key. Without one, a local run would overwrite the password of a shared
live account and revoke the sessions of every other run. The two addresses
(`E2E_VERIFIED_EMAIL`, `E2E_UNVERIFIED_EMAIL`) do have defaults, in
`tests/e2e/support/e2e-auth-defaults.json`, but they are bases to derive this
run's address from, never targets.
Supabase admin env is required too: `SUPABASE_URL`,
`SUPABASE_SERVICE_ROLE_KEY`. See `docs/live-test-auth.md`.

## Conventions

- **Immutability**: new objects, never mutate existing. Ledger is append-only.
- **Commits**: `<type>: <description>` — `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`.
- **No hardcoded secrets**: env vars only. Never commit `.env`.
- **Provider-blind errors**: sanitize at both control-plane and edge boundaries. Provider names never leak to customers.
- **`math/big` for FX**: all financial calcs use `math/big` to prevent `float64` corruption.
- **Storage**: Supabase Storage is the only object storage backend. `edge-api` and `control-plane` fail fast unless required S3 env vars are present and both `hive-files` + `hive-images` buckets exist at startup.

## Regulatory Rules

None currently. The prior rule here (never show FX rates or currency-exchange
language to BD customers; omit `amount_usd` from BD payment responses) was
revoked by owner decision on 2026-08-08. Currency presentation to BD
customers is an unconstrained product decision, to be revisited.

## Known Issues

Known issues, UAT results, and deferred scope are tracked in the project
vault (Obsidian), not in-repo. See the roadmap board below for current
status.

## Demo & live surfaces

| Surface | URL |
|---------|-----|
| Chat | https://chat-hive.scubed.co |
| Developer console | https://console-hive.scubed.co |
| API base (OpenAI-compatible) | https://api-hive.scubed.co |
| Control plane | https://control-hive.scubed.co |

The demo runs continuous deployment from `main`; data resets are possible
during active development.

## Project State

- **v1.0 — developer-api-core**: shipped 2026-04-21.
- **v1.1**: closed late July 2026 (the phase frame is retired).

Active milestones:

- [v1.2.1 demo readiness](https://github.com/sakibsadmanshajib/hive/milestone/8)
- [v1.2 agentic surface](https://github.com/sakibsadmanshajib/hive/milestone/5)
- [Enterprise edge-first v1](https://github.com/sakibsadmanshajib/hive/milestone/7)
- [v1.3 device era](https://github.com/sakibsadmanshajib/hive/milestone/6)

**Roadmap board**: https://github.com/users/sakibsadmanshajib/projects/3

Planning ground truth (state, roadmap, requirements, UAT, deferred scope)
lives in the project vault (Obsidian), not in-repo.

## Repository Layout

```
apps/                       Go + Next.js services (see Architecture table)
packages/
  openai-contract/          Spec + support matrix (single source of truth)
  sdk-tests/                JS / Python / Java integration suites
supabase/migrations/        Postgres schema (41 migrations)
deploy/
  docker/                   Compose + Dockerfiles
  litellm/                  LiteLLM config
  prometheus/               Prometheus + alert rules
  grafana/                  Dashboards + provisioning
scripts/                    One-off operational scripts
docs/                       Hand-written docs + generated codemaps
```
