# OpenWolf

@.wolf/OPENWOLF.md

Project use OpenWolf for context mgmt. Read + follow .wolf/OPENWOLF.md every session. Check .wolf/cerebrum.md before gen code. Check .wolf/anatomy.md before read files. Check .wolf/decisions.md before any design, spec, plan, or implementation, and inject the relevant decisions into every subagent brief (detail lives in the vault, this is the terse index).

## Orchestrator Contract

The main agent is bound by `.claude/rules/orchestrator.md`. Read it at session start. It defines persona, delegation rules, communication protocol, agent fleet rules, and context hygiene for the CTO orchestrator role.


# Hive

OpenAI-compatible API gateway. v1.0 shipped as a full **Go rewrite** of a prior implementation, for efficiency and operational control (lean hot-path latency, precise `math/big` FX, full source-level control over routing, sanitization, billing).

One product, two modes: **Hive** (hosted SaaS, Bangladesh market first, prepaid credit billing on BDT payment rails via Stripe, bKash, and SSLCommerz) and **Hive Enterprise** (customer-hosted, data-sovereign posture for regulated buyers in finance, legal, healthcare, and government). Single org equals single tenant; departments via RBAC. Provider-agnostic routing to OpenRouter, Groq, and future providers, plus self-hosted inference for the Enterprise posture.

Surfaces: chat (Open WebUI), coding and browsing agents (agent-console sidecar plus agent-engine sandbox), RAG (`/v1/rag/chat`), voice (Groq STT/TTS), artifacts hosting, desktop app (Tauri, Windows/Linux), and the developer console for key and billing management.

## Tech Stack

| Component | Tech | Version |
|-----------|------|---------|
| **control-plane** | Go | 1.24 |
| **edge-api** | Go | 1.24 |
| **web-console** | Next.js / React / TypeScript | 15 / 19 / 5.8 |
| **web-console hosting** | Cloudflare Workers via `@opennextjs/cloudflare` | latest stable |
| **Database** | Postgres (Supabase-hosted) | — |
| **Cache** | Redis | 8.4 |
| **Model routing** | LiteLLM | latest-stable |
| **Monitoring** | Prometheus + Grafana + Alertmanager | — |
| **Payments** | Stripe, bKash, SSLCommerz | — |
| **Storage** | Supabase Storage (S3-compatible) | — |

## Architecture

```
apps/control-plane     Go: accounts, billing, credits, API keys, payments, catalog, routing
apps/edge-api          Go: auth, rate limiting, inference dispatch, SSE streaming, file/media
apps/agent-console     Next.js: agent chat sidecar, served under Caddy alongside the rest of the chat surface
apps/agent-engine      Go: agent sandbox service, launches each agent session inside an Apptainer sandbox
apps/desktop           Tauri (Rust + TypeScript): desktop shell, Windows and Linux
apps/desktop-sandbox   Rust: desktop-side sandbox process (bwrap/process hardening, vendored from Codex)
apps/web-console       Next.js: developer console (billing, keys, analytics, catalog)
packages/openai-contract   OpenAI spec + support matrix (single source of truth)
packages/sdk-tests     JS/Python/Java SDK integration tests (real OpenAI SDKs)
packages/storage       Shared Supabase Storage (S3) client helpers
packages/audit-canonical   Canonical audit-event schema and writer shared across services
packages/embedmodel    Dimension-agnostic RAG embedding model registry and admin config
vendor/openhands       Patched vendored OpenHands SDK consumed by apps/agent-engine
supabase/migrations    Postgres schema
deploy/docker          Docker Compose + Dockerfiles
deploy/litellm         LiteLLM config (OpenRouter/Groq routing)
deploy/prometheus      Prometheus + alert rules
deploy/grafana         Dashboards + provisioning
deploy/alertmanager    Alert routing
website/               Marketing sites (sovereign + BD, geo-split, Astro)
tools/                 Repo policy lints (tenancy/audit guards) plus SOC2 coverage scripts
```

## Getting Started

All runs through Docker. No host-installed Go or Node required.

### 1. Environment

```bash
cp .env.example .env
# Fill in: SUPABASE_URL, SUPABASE_ANON_KEY, SUPABASE_SERVICE_ROLE_KEY, SUPABASE_DB_URL
# Fill in: NEXT_PUBLIC_SUPABASE_URL, NEXT_PUBLIC_SUPABASE_ANON_KEY
# Fill in: S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY, S3_REGION, S3_BUCKET_FILES, S3_BUCKET_IMAGES
# Fill in at least one LLM provider: OPENROUTER_API_KEY or GROQ_API_KEY
```

See `.env.example` for all vars with inline comments. Payment rail keys (Stripe, bKash, SSLCommerz) optional — services start without them.
Supabase Storage only object storage backend. Enable S3 protocol in Supabase Storage, pre-create `hive-files` + `hive-images` buckets, provide all S3 vars before start `edge-api` or `control-plane`.

### 2. Run

```bash
cd deploy/docker

# Local dev: core stack with in-stack Redis.
docker compose --env-file ../../.env --profile local up --build

# Full demo surface (agent subsystem): core + agent-console sidecar (chat) +
# agent-engine (agent) + in-stack Redis (local), in ONE command. RAG query
# embeddings run serverless via LiteLLM (EMBEDDING_BASE_URL default). Live
# agent-task sandbox launch additionally needs a built Apptainer runtime image
# (HIVE_AGENT_SIF_PATH); build it from deploy/apptainer/agent-engine.def on a
# host with apptainer (unavailable on WSL2), then set HIVE_AGENT_SIF_PATH in .env.
docker compose --env-file ../../.env --profile local --profile chat --profile agent up --build

# Hive Cloud (hosted SaaS): core services expecting managed Upstash Redis.
# Set REDIS_URL=rediss://... in .env before running.
docker compose --env-file ../../.env --profile cloud up --build

# Hive Cloud with chat front-end (Open WebUI + Caddy on top of cloud):
docker compose --env-file ../../.env --profile cloud --profile chat up --build

# Hive Enterprise (self-hosted single box): core + in-stack Redis + OWUI + Caddy.
# Optional Ollama: set OLLAMA_BASE_URL=http://ollama:11434 in .env and
# uncomment the ollama model entries in deploy/litellm/config.yaml.
docker compose \
  -f docker-compose.yml \
  -f docker-compose.enterprise.yml \
  --env-file ../../.env --profile enterprise up --build

# Add monitoring to any profile (Prometheus, Grafana, Alertmanager):
docker compose --env-file ../../.env --profile local --profile monitoring up --build
```

### 3. Verify

| Service | URL | Healthcheck |
|---------|-----|-------------|
| Edge API | http://localhost:8080 | `GET /health` |
| Control Plane | http://localhost:8081 | `GET /health` |
| Web Console | http://localhost:3000 | — |
| LiteLLM | http://localhost:4000 | — |
| Open WebUI | http://localhost:3003 | `--profile chat` |
| Caddy (OWUI proxy) | http://localhost:8090 | `--profile chat` |
| Prometheus | http://localhost:9090 | monitoring profile |
| Grafana | http://localhost:3001 | monitoring profile, admin/admin |

### 4. Migrations

```bash
supabase db push                    # If Supabase CLI is linked
# Or apply supabase/migrations/ files in order via SQL editor
```

### 5. Agent-engine runtime image (Apptainer)

The agent-engine service launches each agent session inside an Apptainer
sandbox built from `deploy/apptainer/agent-engine.def`. It needs a prebuilt
`.sif` and reads its path from `HIVE_AGENT_SIF_PATH`. The image is `linux/amd64`
only and cannot be built on the WSL2 dev box.

```bash
# Demo/prod host with apptainer installed:
make agent-sif                       # -> deploy/apptainer/agent-engine.sif
export HIVE_AGENT_SIF_PATH=$(pwd)/deploy/apptainer/agent-engine.sif

# No local apptainer: download the CI-built image instead.
gh workflow run "agent-engine SIF"   # or use the latest successful run
gh run download -n agent-engine-sif -D /opt/hive
export HIVE_AGENT_SIF_PATH=/opt/hive/agent-engine.sif
```

The `agent-engine SIF` workflow builds the `.sif` in CI (which also validates
the def) and uploads it as the `agent-engine-sif` artifact. Full detail:
`deploy/apptainer/README.md`.

## Commands

### Testing (always use Docker)

```bash
# Go unit tests. The toolchain image is Alpine with ENTRYPOINT ["/bin/sh","-c"],
# so pass the command string directly. Wrapping it in `bash -c` or `sh -c`
# double-wraps and silently runs nothing (exits 0 with no output).
cd deploy/docker && docker compose --profile tools run toolchain \
  "cd /workspace && go test ./apps/control-plane/... -count=1 -short"
cd deploy/docker && docker compose --profile tools run toolchain \
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

- **Immutability**: New objects, never mutate existing. Ledger append-only.
- **Commits**: `<type>: <description>` — types: feat, fix, refactor, docs, test, chore, perf, ci
- **No hardcoded secrets**: Env vars only. Never commit `.env`.
- **Merge policy** (`main`, enforced by GitHub branch protection incl. admins): a PR is **not mergeable** while it has any failed/missing required test or any unresolved review comment. See `.github/MERGE-POLICY.md`; config in `.github/branch-protection-main.json`. Always resolve every review thread before merging.
- **Provider-blind errors**: Sanitize at both control-plane + edge boundaries. Provider names never leak to customers.
- **math/big for FX**: All financial calcs use `math/big` to prevent float64 corruption.
- **Storage backend**: Supabase Storage only object storage backend. `edge-api` + `control-plane` fail fast unless required S3 env vars present, and `hive-files` + `hive-images` must exist before startup.

## Regulatory Rules

**NEVER show FX rates or currency exchange language to BD customers.** Applies to API responses, frontend UI, error messages, any customer-visible surface. Omit `amount_usd` from BD payment responses.

## Known Issues

Full runtime UAT results, phase closure notes, and v1.1 deferred scope live in the project vault (Obsidian), not in-repo. Resolved items stay listed for their regression guards; open items are deferred to v1.1 because the core developer API path is unaffected in practice.

1. **`ensureCapabilityColumns` targets wrong table** — Resolved by Phase 16 (2026-04-25). Function removed from `apps/control-plane/internal/routing/repository.go`; schema lives in `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` (correctly targets `public.provider_capabilities`); regression guard `TestRoutingRepositoryDoesNotRunCapabilityDDL` enforces non-recurrence. Evidence recorded in the project vault.
2. **File storage wiring under final verification** — Phase 10 now wires file + media endpoints to Supabase Storage. Final live smoke verification tracked in Phase 10 Plan 10-08.
3. **`amount_usd` exposed in BD checkout** — Resolved by Phase 17 (PR #137, 2026-05-09). FX/USD stripped from all customer-bound surfaces. The follow-up lint script and dedicated FX guard test files were removed by owner decision on 2026-07-19; USD-absence assertions remain inside the broader billing functional tests.
4. **Batch success-path blocked by upstream provider capability** — `/v1/batches` success-path (`status=completed`) not exercisable with current provider mix. LiteLLM's managed file upload (`POST /v1/files` with `purpose=batch`) only supports `openai`, `azure`, `vertex_ai`, `manus`, `anthropic`. OpenRouter + Groq (our only configured providers) have no native batch API. Submitter + failure-path terminal settlement work correctly (reservation release + attribution verified live). Phase 15 shipped a local batch executor in control-plane. Full write-up in the project vault.
5. **Capability-based tool routing** — Resolved by Phase 20 wave 3 (PR #206, 2026-06-11). Custom providers are DB-managed (PR #199); `tools`/`tool_choice`/`response_format` route per-route on `tools_supported` in `provider_capabilities`. Tenant model visibility (PR #205) is enforced at catalog/model-listing level, not inside `SelectRoute` dispatch, which filters on `AllowedAliases`/`AllowedProviders`.

## Project State

- **v1.0 — developer-api-core**: shipped 2026-04-21. Phases 1-10 complete. Covers chat-app + CLI-coding-agent integrators.
- **v1.1 — in progress**: Phase 20 (Provider Catalog) waves 1-3 complete (PRs 197, 199, 204, 205, 206), wave 4 pending. Phases 12-19 complete.
- **Roadmap board**: https://github.com/users/sakibsadmanshajib/projects/3

Planning ground truth (milestone state, roadmap, requirements traceability, UAT results, deferred scope) lives in the project vault (Obsidian), not in-repo.

---

## Claude Code Tooling

Project use multi-layer Claude Code setup. Each plugin owns domain — don't mix.

### GSD (Project Lifecycle)

GSD manages phases, planning, execution. Planning ground truth lives in the project vault (Obsidian), not in-repo.

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

Enforces TDD, structured debugging, code review, planning workflows.

- **Before coding**: `superpowers:brainstorming` (creative work), `superpowers:writing-plans` (multi-step tasks)
- **Writing code**: `superpowers:test-driven-development` (always write tests first), `superpowers:executing-plans`
- **After coding**: `superpowers:requesting-code-review`, `superpowers:verification-before-completion`
- **Debugging**: `superpowers:systematic-debugging`
- **Shipping**: `superpowers:finishing-a-development-branch`

### Everything Claude Code (Language Agents)

ECC provides language-specific review + build agents. Use right agent for language:

- **Go code**: `go-reviewer` agent, `go-build` skill for build errors
- **TypeScript/JS**: `typescript-reviewer` agent, `build-error-resolver` agent
- **Database/SQL**: `database-reviewer` agent
- **Security**: `security-reviewer` agent

### context-mode (Context Window Protection)

Hooks enforce context-mode automatically. Rules:

- **Bash** only for git/mkdir/rm/mv/navigation commands
- **Large output** (>20 lines): Use `ctx_batch_execute` instead of Bash
- **File analysis**: Use `ctx_execute_file` instead of Read (Read correct only when editing)
- **Web fetches**: Use `ctx_fetch_and_index` instead of WebFetch
- Check savings: `/ctx-stats`

### Auto-memory (Persistent Memory)

Native Claude Code auto-memory replaced claude-mem (retired 2026-06-12). MEMORY.md and topic files load at session start; record durable learnings there. Historical claude-mem archive: `~/.claude-mem/claude-mem.db` (read-only sqlite).

### Supabase MCP

Direct DB interaction via Supabase MCP server:

- **Run SQL**: `execute_sql` — query or mutate DB directly
- **Apply migrations**: `apply_migration` — apply new schema changes
- **List tables**: `list_tables` — inspect current schema
- **Generate types**: `generate_typescript_types` — TypeScript type gen from schema
- **Get logs**: `get_logs` — check Supabase service logs

### Context7 (Documentation Lookup)

Before recall any SDK/API/framework signature from memory, verify with Context7:

```
resolve-library-id → query-docs
```

### Playwright (Browser Testing)

Browser automation for E2E + UAT testing via Playwright MCP. Use for testing web-console flows.
