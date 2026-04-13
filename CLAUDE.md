# Hive

OpenAI-compatible API gateway for the Bangladesh market. Proxies LLM requests through provider-agnostic routing, handles prepaid credit billing with BDT payment rails, and exposes a developer console for key/billing management.

## Tech Stack

| Component | Tech | Version |
|-----------|------|---------|
| **control-plane** | Go | 1.24 |
| **edge-api** | Go | 1.24 |
| **web-console** | Next.js / React / TypeScript | 15 / 19 / 5.8 |
| **Database** | Postgres (Supabase-hosted) | — |
| **Cache** | Redis | 8.4 |
| **Model routing** | LiteLLM | latest-stable |
| **Monitoring** | Prometheus + Grafana + Alertmanager | — |
| **Payments** | Stripe, bKash, SSLCommerz | — |
| **Storage** | Supabase Storage (S3-compatible) | — |

## Architecture

```
apps/control-plane    Go — accounts, billing, credits, API keys, payments, catalog, routing
apps/edge-api         Go — auth, rate limiting, inference dispatch, SSE streaming, file/media
apps/web-console      Next.js — developer console (billing, keys, analytics, catalog)
packages/openai-contract  OpenAI spec + support matrix (single source of truth)
packages/sdk-tests    JS/Python/Java SDK integration tests (real OpenAI SDKs)
supabase/migrations   Postgres schema (14 migrations)
deploy/docker         Docker Compose + Dockerfiles
deploy/litellm        LiteLLM config (OpenRouter/Groq routing)
deploy/prometheus     Prometheus + alert rules
deploy/grafana        Dashboards + provisioning
deploy/alertmanager   Alert routing
```

## Getting Started

Everything runs through Docker. No host-installed Go or Node required.

### 1. Environment

```bash
cp .env.example .env
# Fill in: SUPABASE_URL, SUPABASE_ANON_KEY, SUPABASE_SERVICE_ROLE_KEY, SUPABASE_DB_URL
# Fill in: NEXT_PUBLIC_SUPABASE_URL, NEXT_PUBLIC_SUPABASE_ANON_KEY
# Fill in at least one LLM provider: OPENROUTER_API_KEY or GROQ_API_KEY
```

See `.env.example` for all variables with inline comments. Payment rail keys (Stripe, bKash, SSLCommerz) are optional — services start without them.

### 2. Run

```bash
cd deploy/docker

# Core stack (edge-api + control-plane + redis + litellm + web-console)
docker compose --env-file ../../.env up --build

# With monitoring (adds Prometheus, Grafana, Alertmanager)
docker compose --env-file ../../.env --profile monitoring up --build
```

### 3. Verify

| Service | URL | Healthcheck |
|---------|-----|-------------|
| Edge API | http://localhost:8080 | `GET /health` |
| Control Plane | http://localhost:8081 | `GET /health` |
| Web Console | http://localhost:3000 | — |
| LiteLLM | http://localhost:4000 | — |
| Prometheus | http://localhost:9090 | monitoring profile |
| Grafana | http://localhost:3001 | monitoring profile, admin/admin |

### 4. Migrations

```bash
supabase db push                    # If Supabase CLI is linked
# Or apply supabase/migrations/ files in order via SQL editor
```

## Commands

### Testing (always use Docker)

```bash
# Go unit tests
cd deploy/docker && docker compose --profile tools run toolchain bash -c \
  "cd /workspace && go test ./apps/control-plane/... -count=1 -short"
cd deploy/docker && docker compose --profile tools run toolchain bash -c \
  "cd /workspace && go test ./apps/edge-api/... -count=1 -short"

# Frontend type check + build
cd deploy/docker && docker compose run web-console npm run build

# Frontend unit tests
cd deploy/docker && docker compose run web-console npm run test:unit

# SDK integration tests (requires healthy core stack)
cd deploy/docker && docker compose --env-file ../../.env --profile test up --build

# E2E tests
cd apps/web-console && npx playwright test
```

### Go workspace gotcha

With `go.work`, Docker test commands must use full module-relative paths (`./apps/control-plane/internal/...`), not short `./internal/...` form.

## Conventions

- **Immutability**: New objects, never mutate existing ones. Ledger is append-only.
- **Commits**: `<type>: <description>` — types: feat, fix, refactor, docs, test, chore, perf, ci
- **No hardcoded secrets**: Environment variables only. Never commit `.env`.
- **Provider-blind errors**: Sanitize at both control-plane and edge boundaries. Provider names never leak to customers.
- **math/big for FX**: All financial calculations use `math/big` to prevent float64 corruption.
- **No MinIO**: Use Supabase Storage for all file/object storage. Zero MinIO references in codebase.

## Regulatory Rules

**NEVER show FX rates or currency exchange language to BD customers.** This applies to API responses, frontend UI, error messages, and any customer-visible surface. Omit `amount_usd` from BD payment responses.

## Known Issues

See `.planning/UAT-REPORT.md` for full runtime UAT results. Key blockers:

1. **`ensureCapabilityColumns` targets wrong table** — `apps/control-plane/internal/routing/repository.go` targets `route_capabilities` instead of `provider_capabilities`. Blocks all inference routing.
2. **File storage not wired** — Edge-api degrades gracefully (file/media endpoints disabled). Phase 10 replaces minio-go with Supabase Storage REST API.
3. **`amount_usd` exposed in BD checkout** — `apps/control-plane/internal/payments/http.go:105-115`. Regulatory risk.

## Project State

Planning artifacts live in `.planning/`. Current milestone progress, phase plans, and requirements are tracked there:

- `.planning/ROADMAP.md` — Phase breakdown and progress
- `.planning/REQUIREMENTS.md` — Requirement traceability
- `.planning/UAT-REPORT.md` — Runtime test results
- `.planning/v1.0-MILESTONE-AUDIT.md` — Launch readiness audit

---

## Claude Code Tooling

This project uses a multi-layer Claude Code setup. Each plugin owns a domain — don't mix them.

### GSD (Project Lifecycle)

GSD manages phases, planning, and execution. All project state lives in `.planning/`.

| Action | Command |
|--------|---------|
| Check progress | `/gsd:progress` |
| Plan a phase | `/gsd:plan-phase` |
| Execute a phase | `/gsd:execute-phase` |
| Verify work | `/gsd:verify-work` |
| Ship (PR) | `/gsd:ship` |
| Debug | `/gsd:debug` |
| Quick task | `/gsd:quick` |
| All commands | `/gsd:help` |

### Superpowers (Engineering Discipline)

Enforces TDD, structured debugging, code review, and planning workflows.

- **Before coding**: `superpowers:brainstorming` (creative work), `superpowers:writing-plans` (multi-step tasks)
- **Writing code**: `superpowers:test-driven-development` (always write tests first), `superpowers:executing-plans`
- **After coding**: `superpowers:requesting-code-review`, `superpowers:verification-before-completion`
- **Debugging**: `superpowers:systematic-debugging`
- **Shipping**: `superpowers:finishing-a-development-branch`

### Everything Claude Code (Language Agents)

ECC provides language-specific review and build agents. Use the right agent for the language:

- **Go code**: `go-reviewer` agent, `go-build` skill for build errors
- **TypeScript/JS**: `typescript-reviewer` agent, `build-error-resolver` agent
- **Database/SQL**: `database-reviewer` agent
- **Security**: `security-reviewer` agent

### context-mode (Context Window Protection)

Hooks enforce context-mode automatically. Rules:

- **Bash** only for git/mkdir/rm/mv/navigation commands
- **Large output** (>20 lines): Use `ctx_batch_execute` instead of Bash
- **File analysis**: Use `ctx_execute_file` instead of Read (Read is correct only when editing)
- **Web fetches**: Use `ctx_fetch_and_index` instead of WebFetch
- Check savings: `/ctx-stats`

### claude-mem (Persistent Memory)

Cross-session memory stored via `claude-mem` MCP. Survives context resets.

- **Search memory**: `mem-search` skill or `get_observations([IDs])`
- **Timeline**: `timeline` tool for chronological view
- Observations are auto-recorded during work. Use `smart_search` for semantic queries.

### Supabase MCP

Direct database interaction via the Supabase MCP server:

- **Run SQL**: `execute_sql` — query or mutate the database directly
- **Apply migrations**: `apply_migration` — apply new schema changes
- **List tables**: `list_tables` — inspect current schema
- **Generate types**: `generate_typescript_types` — TypeScript type generation from schema
- **Get logs**: `get_logs` — check Supabase service logs

### Context7 (Documentation Lookup)

Before recalling any SDK/API/framework signature from memory, verify with Context7:

```
resolve-library-id → query-docs
```

### Playwright (Browser Testing)

Browser automation for E2E and UAT testing via Playwright MCP. Use for testing web-console flows.
