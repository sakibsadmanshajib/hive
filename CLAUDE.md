# OpenWolf

Project use OpenWolf for context mgmt. Read + follow `.claude/rules/openwolf.md` every session. Check .wolf/cerebrum.md before gen code. Check .wolf/anatomy.md before read files. Check .wolf/decisions.md before any design, spec, plan, or implementation, and inject the relevant decisions into every subagent brief (detail lives in the vault, this is the terse index).

**.wolf/ state files.** Tracked and curated: `cerebrum.md`, `decisions.md`, `buglog.jsonl`, `GOAL.md`, `fleet.json`, `cost-ledger.md`, `hooks/*.js`. Untracked telemetry, hook-owned and gitignored: `anatomy.md`, `memory.md`, `token-ledger.json`, `hooks/_session.json`, `buglog.json`. Never hand-edit or commit telemetry and never `git add -f` it: the hooks rewrite it on every tool call, which blocks `git pull --ff-only` on the shared checkout and produces competing versions across parallel worktrees. Bug memory is appended to `.wolf/buglog.jsonl`, one JSON object per line. Logging every fixed bug, error, failed test or failed build is still mandatory, but the line never lands on a feature branch: carry it in the fix PR body under a "Buglog entry" heading, then append it to `main` in a separate buglog-only PR once the fix has merged, one such PR open at a time. `merge=union` in `.gitattributes` resolves concurrent appends locally but is ignored by GitHub's server-side merge, so branch appends conflict serially and an unmergeable PR gets no `pull_request` CI run at all (issue #873). Full protocol: `.claude/rules/openwolf.md`.

## Orchestrator Contract

The main agent is bound by `.claude/rules/orchestrator.md`. Read it at session start. It defines persona, delegation rules, communication protocol, agent fleet rules, and context hygiene for the CTO orchestrator role.

This repo also carries project-level skills under `.claude/skills/` (`memory-tools`, `review-changes`, `refactor-safely`, `debug-issue`, `explore-codebase`), routed only here since the global skill router cannot enumerate every project's local skills. Check that directory for a match before reaching for a global equivalent.


# Hive

OpenAI-compatible API gateway. v1.0 shipped as a full **Go rewrite** of a prior implementation, for efficiency and operational control (lean hot-path latency, precise `math/big` FX, full source-level control over routing, sanitization, billing).

One product, two modes: **Hive** (hosted SaaS, Bangladesh market first, prepaid credit billing on BDT payment rails via Stripe, bKash, and SSLCommerz) and **Hive Enterprise** (customer-hosted, data-sovereign posture for regulated buyers in finance, legal, healthcare, and government). Single org equals single tenant; departments via RBAC. Provider-agnostic routing to OpenRouter, Groq, and future providers, plus self-hosted inference for the Enterprise posture.

Surfaces: chat (Open WebUI), coding and browsing agents (agent-console sidecar plus agent-engine sandbox), RAG (`/v1/rag/chat`), voice (Groq STT/TTS), artifacts hosting, desktop app (Tauri, Windows/Linux), and the developer console for key and billing management.

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
# agent-task sandbox launch additionally needs the host launcher running and
# HIVE_AGENT_ENGINE_SOCKET set (see section 4); the `agent` profile's
# agent-engine service is a build and smoke-test container only.
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

### 3. Migrations

```bash
supabase db push                    # If Supabase CLI is linked
# Or apply supabase/migrations/ files in order via SQL editor
```

### 4. Agent-engine runtime (Apptainer sandbox)

Each agent session runs inside an Apptainer sandbox built from
`deploy/apptainer/agent-engine.def`. The image is `linux/amd64` only and cannot
be built on the WSL2 dev box, so take it from CI (`gh run download -n
agent-engine-sif -D /opt/hive`) or build it on an apptainer host with
`make agent-sif`.

What actually gates a task launch is in `buildAgentEngine`
(`apps/control-plane/cmd/server/main.go`), and it has two arms, checked in this
order.

**Socket arm, which is what the demo box runs.** If `HIVE_AGENT_ENGINE_SOCKET`
is set, control-plane hands every launch to the unprivileged host launcher over
that Unix socket, authenticating with `CONTROL_PLANE_INTERNAL_TOKEN`, and none
of the path variables below are read at all. Control-plane cannot exec Apptainer
itself (Alpine base, no `/dev/fuse`, no `CAP_SYS_ADMIN`), and granting it that
privilege was refused deliberately, so this is the only arm that runs a real
task on any deployment this repo ships. Stand the launcher up with
`scripts/install-agent-engine-host.sh`; it reads its own variable set in
`apps/agent-engine/cmd/agent-engine/serve.go`, including the four paths and
`HIVE_AGENT_ENGINE_LLM_MODEL`, `HIVE_AGENT_ENGINE_LLM_BASE_URL` and
`HIVE_AGENT_ENGINE_LLM_API_KEY`.

**In-process arm.** With no socket set, control-plane tries to exec Apptainer
itself. That needs a non-nil egress service (so, a live DB pool) plus five
variables, all of them: `HIVE_AGENT_ENGINE_SIF_PATH`,
`HIVE_AGENT_ENGINE_PACKS_DIR`, `HIVE_AGENT_ENGINE_WORKSPACE_ROOT`,
`HIVE_AGENT_ENGINE_RUN_DIR`, `HIVE_AGENT_ENGINE_PROFILE_ID`. Miss any one and
the engine falls back to `NotConfiguredEngine`, every submitted task fails
immediately, and a boot WARN names what was missing
(`docker compose logs control-plane | grep "agent engine not configured"`).
Under docker compose this arm cannot succeed regardless of the variables.

Optional, all defaulted: `HIVE_AGENT_ENGINE_SESSION_API_KEY`,
`HIVE_QUOTA_TENANT_CONCURRENCY` (4), `HIVE_QUOTA_USER_CONCURRENCY` (2),
`HIVE_SANDBOX_MEMORY_LIMIT` (4G), `HIVE_SANDBOX_CPU_LIMIT` (2),
`HIVE_SANDBOX_PIDS_LIMIT` (512).

`HIVE_AGENT_SIF_PATH` gates nothing. It survives only as the `-sif` flag default
for the standalone `agent-engine` binary
(`apps/agent-engine/cmd/agent-engine/main.go`) and for the compose smoke-test
service under `--profile agent`. Setting it does not make Cowork work. Full
detail: `deploy/apptainer/README.md`.

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

# Frontend type check + build. Unlike toolchain, the web-console service mounts
# no volume and Dockerfile.web-console COPYs the source in at image build time,
# so without `--build` the run silently exercises whatever `hive-web-console:ci`
# was last built and reports a green result for stale code.
cd deploy/docker && docker compose run --build web-console npm run build

# Frontend unit tests
cd deploy/docker && docker compose run --build web-console npm run test:unit

# SDK integration tests (requires healthy core stack)
cd deploy/docker && docker compose --env-file ../../.env --profile test up --build

# E2E tests. Host-run against the working tree, so no image staleness here, but
# it needs a stack already running.
cd apps/web-console && npx playwright test
```

### Live sessions for tests

A run that must be signed in against a deployed environment uses
`apps/web-console/tests/e2e/support/live-auth.mjs` (CLI and storage-state
producer) or `live-auth.ts` (spec-side). It mints a session through the admin
one-time-token flow: no password needed, and none changed.

**Never set, reset or rotate a shared test account's password to obtain a
session.** That account is shared mutable state and the control-plane resolves
every bearer against GoTrue per request, so a rotation invalidates every
concurrent run; it broke three agents at once on 2026-08-08. Full protocol and
the forbidden-shortcut list: `docs/live-test-auth.md`.

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
- **Chat front-end is a fork**: Open WebUI is forked and heavily modified. There is **no no-fork rule**; the one that existed (`.wolf/decisions.md` D-013) was revoked by owner decision on 2026-08-11 (D-036). Target architecture in the owner's words: one web, one shell, two embedded panels. The product is not keeping stock Open WebUI, and the bar is a polished rebuild rather than something assembled in one night. The static-hook pair (`deploy/docker/owui-static/custom.css`, `loader.js`) and the exact-literal bundle rewrites under `deploy/docker/owui-patches/` are the old ceiling, not a constraint: never cite them, or any comment near them, to refuse fork work.

## Regulatory Rules

None currently. The prior rule here (never show FX rates, currency-exchange language, or USD equivalents to BD customers; omit `amount_usd` from BD payment responses) was revoked by owner decision on 2026-08-08: it was never an actual regulatory requirement, and the owner is fine with a USD amount reaching the customer. Currency presentation to BD customers is an unconstrained product decision, to be revisited if the product team wants to display it. See `.wolf/decisions.md` D-035. Existing code that strips `amount_usd` from customer-facing responses was left in place (no behavior change); relaxing it is a separate, future product decision.

## Known Issues

Full runtime UAT results, phase closure notes, and v1.1 deferred scope live in the project vault (Obsidian), not in-repo. Resolved items stay listed for their regression guards; open items are deferred to v1.1 because the core developer API path is unaffected in practice.

1. **`ensureCapabilityColumns` targets wrong table** — Resolved by Phase 16 (2026-04-25). Function removed from `apps/control-plane/internal/routing/repository.go`; schema lives in `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` (correctly targets `public.provider_capabilities`); regression guard `TestRoutingRepositoryDoesNotRunCapabilityDDL` enforces non-recurrence. Evidence recorded in the project vault.
2. **File storage wiring under final verification** — Phase 10 now wires file + media endpoints to Supabase Storage. Final live smoke verification tracked in Phase 10 Plan 10-08.
3. **`amount_usd` exposed in BD checkout** — No longer tracked as an issue. Phase 17 (PR #137, 2026-05-09) stripped FX/USD from customer-bound surfaces under what was believed to be a regulatory constraint; that constraint was revoked by owner decision on 2026-08-08 (see Regulatory Rules above, `.wolf/decisions.md` D-035). The stripping code itself is unchanged; the USD-absence test assertions that treated it as a hard requirement were removed.
4. **Batch success-path blocked by upstream provider capability** — `/v1/batches` success-path (`status=completed`) not exercisable with current provider mix. LiteLLM's managed file upload (`POST /v1/files` with `purpose=batch`) only supports `openai`, `azure`, `vertex_ai`, `manus`, `anthropic`. OpenRouter + Groq (our only configured providers) have no native batch API. Submitter + failure-path terminal settlement work correctly (reservation release + attribution verified live). Phase 15 shipped a local batch executor in control-plane. Full write-up in the project vault.
5. **Capability-based tool routing** — Resolved by Phase 20 wave 3 (PR #206, 2026-06-11). Custom providers are DB-managed (PR #199); `tools`/`tool_choice`/`response_format` route per-route on `tools_supported` in `provider_capabilities`. Tenant model visibility (PR #205) is enforced at catalog/model-listing level, not inside `SelectRoute` dispatch, which filters on `AllowedAliases`/`AllowedProviders`.

## Project State

- **v1.0 — developer-api-core**: shipped 2026-04-21. Phases 1-10 complete. Covers chat-app + CLI-coding-agent integrators.
- **v1.1 — in progress**: Phase 20 (Provider Catalog) waves 1-3 complete (PRs 197, 199, 204, 205, 206), wave 4 pending. Phases 12-19 complete.
- **Roadmap board**: https://github.com/users/sakibsadmanshajib/projects/3

Planning ground truth (milestone state, roadmap, requirements traceability, UAT results, deferred scope) lives in the project vault (Obsidian), not in-repo.
