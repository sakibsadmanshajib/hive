# OpenWolf

Project use OpenWolf for context mgmt. Read + follow `.claude/rules/openwolf.md` every session. Check .wolf/cerebrum.md before gen code. Check .wolf/anatomy.md before read files. Check .wolf/decisions.md before any design, spec, plan, or implementation, and inject the relevant decisions into every subagent brief (detail lives in the vault, this is the terse index).

**.wolf/ state files.** Tracked and curated: `cerebrum.md`, `decisions.md`, `buglog.jsonl`, `GOAL.md`, `fleet.json`, `cost-ledger.md`, `hooks/*.js`. Untracked telemetry, hook-owned and gitignored: `anatomy.md`, `memory.md`, `token-ledger.json`, `hooks/_session.json`, `buglog.json`. Never hand-edit or commit telemetry and never `git add -f` it: the hooks rewrite it on every tool call, which blocks `git pull --ff-only` on the shared checkout and produces competing versions across parallel worktrees. Bug memory is appended to `.wolf/buglog.jsonl`, one JSON object per line. Logging every fixed bug, error, failed test or failed build is still mandatory, but the line never lands on a feature branch: carry it in the fix PR body under a "Buglog entry" heading, then append it to `main` in a separate buglog-only PR once the fix has merged, one such PR open at a time. `merge=union` in `.gitattributes` resolves concurrent appends locally but is ignored by GitHub's server-side merge, so branch appends conflict serially and an unmergeable PR gets no `pull_request` CI run at all (issue #873). Full protocol: `.claude/rules/openwolf.md`.

## Orchestrator Contract

The main agent is bound by `.claude/rules/orchestrator.md`. Read it at session start. It defines persona, delegation rules, communication protocol, agent fleet rules, and context hygiene for the CTO orchestrator role.

This repo also carries project-level skills under `.claude/skills/`, routed only here since the global skill router cannot enumerate every project's local skills. List that directory and check the front matter `description` of anything that looks relevant before reaching for a global equivalent. The list is deliberately not enumerated here: the enumeration that used to sit in this paragraph went stale in both directions, naming four skills that had been dead for months while omitting six that existed.


# Hive

OpenAI-compatible API gateway. v1.0 shipped as a full **Go rewrite** of a prior implementation, for efficiency and operational control (lean hot-path latency, precise `math/big` FX, full source-level control over routing, sanitization, billing).

One product, two modes: **Hive** (hosted SaaS, Bangladesh market first, prepaid credit billing on BDT payment rails via Stripe, bKash, and SSLCommerz) and **Hive Enterprise** (customer-hosted, data-sovereign posture for regulated buyers in finance, legal, healthcare, and government). Single org equals single tenant; departments via RBAC. Provider-agnostic routing to OpenRouter, Groq, and future providers, plus self-hosted inference for the Enterprise posture.

Surfaces: chat (Open WebUI), coding and browsing agents (agent-console sidecar plus agent-engine sandbox), RAG (`/v1/rag/chat`), voice (TTS through Groq; STT dispatches to self-hosted Parakeet and faster-whisper sidecars when `PARAKEET_BASE_URL` or `FASTER_WHISPER_BASE_URL` is set, and falls back to ordinary route selection otherwise), artifacts hosting, desktop app (Tauri, Windows/Linux), and the developer console for key and billing management.

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

If this checkout is a git worktree (anything under `.claude/worktrees/` or a
sibling directory, not the canonical `hive` checkout), run
`scripts/set-compose-project-name.sh` once now, before any `docker compose`
command below (both the Run and Testing sections use them). Compose keys
container identity on the project name, which defaults to `hive` for every
checkout, so the same documented command run from two worktrees can recreate
or crash each other's containers (issue #1242, PR #1249). The script is a
no-op on the canonical `hive` checkout (what the demo box and CI use), so it
never changes that project name.

Run it AFTER copying any `.env` into the worktree, never before. It writes
`COMPOSE_PROJECT_NAME` into both `deploy/docker/.env` and the worktree root
`.env`, and a later `cp .../.env .env` overwrites the second one, which is the
file `--env-file ../../.env` actually reads. The worktree then silently joins
project `hive` and recreates the canonical checkout's containers, which is the
exact collision the script exists to prevent.

### 2. Run

```bash
cd deploy/docker

# The four core services (edge-api, control-plane, litellm, markitdown) carry
# no `profiles:` key, so they start under every command below regardless of
# which profile is named. A profile only ever adds services on top of those.

# Local dev: core + in-stack Redis + Open WebUI + its Caddy fronts
# (open-webui, caddy-owui, caddy-artifacts are all in the `local` profile).
docker compose --env-file ../../.env --profile local up --build

# Full demo surface (agent subsystem), in ONE command. `chat` adds
# agent-console, web-console-prod and caddy-console on top of what `local`
# already started; `agent` adds the agent-engine build container; `local`
# supplies in-stack Redis. RAG query embeddings run serverless via LiteLLM
# (EMBEDDING_BASE_URL defaults to http://litellm:4000). Live agent-task sandbox
# launch additionally needs the host launcher running and
# HIVE_AGENT_ENGINE_SOCKET set (see section 4); the `agent` profile's
# agent-engine service is a build and smoke-test container only.
docker compose --env-file ../../.env --profile local --profile chat --profile agent up --build

# Hive Cloud (hosted SaaS): the four core services, expecting managed Upstash
# Redis. Set REDIS_URL=rediss://... in .env before running. No service declares
# a `cloud` profile, so the flag selects nothing by itself; it is a label for
# "core only, no in-stack Redis" and the command behaves identically without it.
docker compose --env-file ../../.env --profile cloud up --build

# Hive Cloud with chat front-end (Open WebUI, agent-console, web-console-prod
# and the Caddy fronts) on top of core, still with no in-stack Redis:
docker compose --env-file ../../.env --profile cloud --profile chat up --build

# Hive Enterprise (self-hosted single box): core + in-stack Redis + OWUI +
# agent-console + web-console-prod + the Caddy fronts + Ollama, plus the
# self-hosted Supabase data plane from the overlay file. The ollama service is
# in the `enterprise` profile, so it starts here unconditionally; wiring it into
# inference is the optional part. Set OLLAMA_BASE_URL=http://ollama:11434 in
# .env and append ollama entries to model_list in deploy/litellm/config.yaml
# (there is nothing to uncomment: the entries are deliberately absent, and
# `scripts/install.sh --with-ollama` appends them for you).
docker compose \
  -f docker-compose.yml \
  -f docker-compose.enterprise.yml \
  --env-file ../../.env --profile enterprise up --build

# Add monitoring to any profile (Prometheus, Grafana, Alertmanager):
docker compose --env-file ../../.env --profile local --profile monitoring up --build
```

### 3. Migrations

```bash
scripts/apply-migrations.sh --dry-run   # list what would be applied, touch nothing
scripts/apply-migrations.sh             # apply pending migrations
scripts/apply-migrations.sh --check     # validate the baseline file only, no database
```

`scripts/apply-migrations.sh` is the migration path. It applies pending
`supabase/migrations/*.sql` in order and records each one in
`public.hive_schema_migrations` so the next run skips it. Both the deploy
workflow (`.github/workflows/deploy-demo-box.yml`, `--check` at preflight and a
real run before the stack restarts) and CI's throwaway Postgres
(`scripts/ci-throwaway-db.sh`) go through it, so it is the only route that has
been exercised.

Connection settings come from libpq environment variables (`PGHOST`, `PGPORT`,
`PGUSER`, `PGDATABASE`, `PGPASSWORD`) that the caller exports; the script never
takes a DSN. Against the self-hosted data plane, which publishes no host port,
set `PSQL_BIN=scripts/stack-psql.sh` so the client runs inside the stack's
network. Full detail in the script's own header comment.

Do not apply migration files by hand through a SQL editor. Hand application is
exactly the drift this script exists to end: two correct migrations merged and
never ran, which answered HTTP 403 to every API-key call on the demo box and
silently discarded every `interrupted` usage event.

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

Optional, all defaulted. The first six are read by `buildAgentEngine` on the
in-process arm and by the host launcher; the last two only by the host launcher
(`serve.go`). `HIVE_AGENT_ENGINE_SESSION_API_KEY`,
`HIVE_QUOTA_TENANT_CONCURRENCY` (4), `HIVE_QUOTA_USER_CONCURRENCY` (2),
`HIVE_SANDBOX_MEMORY_LIMIT` (4G), `HIVE_SANDBOX_CPU_LIMIT` (2),
`HIVE_SANDBOX_PIDS_LIMIT` (512), `HIVE_AGENT_ENGINE_BROWSER_TOOLS` (off), and
`HIVE_AGENT_ENGINE_SYSTEM_MESSAGE_SUFFIX` (empty).

That last one is the only knob on the sandboxed agent's system prompt. Empty,
which is the default, sends no `agent_context` on the launch payload at all and
the vendored OpenHands preset produces the prompt unchanged; set, it is
appended to that prompt. It is read by the host launcher, not by any compose
service, so it belongs in the launcher's environment
(`scripts/install-agent-engine-host.sh`, or the repository variable the deploy
workflow passes) and setting it on a container does nothing. Both are ignored
on the in-process arm's `AgentProfileID` path, which carries its own profile.

`HIVE_AGENT_SIF_PATH` gates nothing. It survives only as the `-sif` flag default
for the standalone `agent-engine` binary
(`apps/agent-engine/cmd/agent-engine/main.go`) and for the compose smoke-test
service under `--profile agent`. Setting it does not make Cowork work. Full
detail: `deploy/apptainer/README.md`.

## Commands

### Testing (always use Docker)

Worktree checkout? See the worktree note under "1. Environment" above before
running these.

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
# it needs a stack already running. E2E_RUN_KEY is mandatory and has no
# default: seeding writes a password to whatever address it resolves, so the
# key is what keeps this run off the shared fixture accounts. The three
# credentials have no default either (README.md, docs/live-test-auth.md).
cd apps/web-console && E2E_RUN_KEY="$(whoami)-$(date +%s)" npx playwright test
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

Full runtime UAT results and phase closure notes live in the project vault (Obsidian), not in-repo. Resolved items stay listed for their regression guards. This list is not the open-issue tracker: open work lives in GitHub issues, and an item here is either a resolved entry kept for its guard or one carrying a pointer to the issue that owns it.

1. **`ensureCapabilityColumns` targets wrong table** — Resolved by Phase 16 (2026-04-25). Function removed from `apps/control-plane/internal/routing/repository.go`; schema lives in `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` (correctly targets `public.provider_capabilities`); regression guard `TestRoutingRepositoryDoesNotRunCapabilityDDL` enforces non-recurrence. Evidence recorded in the project vault.
2. **File storage wiring** — Resolved. File and media endpoints are wired to Supabase Storage, `edge-api` and `control-plane` both refuse to boot without the S3 variables (`requireStorageEnv` in `apps/edge-api/cmd/server/main.go`), and the demo box holds live rows in both `storage.objects` and `public.files`. The former pointer to "Phase 10 Plan 10-08" named a tracking device that no longer exists.
3. **`amount_usd` exposed in BD checkout** — No longer tracked as an issue. Phase 17 (PR #137, 2026-05-09) stripped FX/USD from customer-bound surfaces under what was believed to be a regulatory constraint; that constraint was revoked by owner decision on 2026-08-08 (see Regulatory Rules above, `.wolf/decisions.md` D-035). The stripping code itself is unchanged; the USD-absence test assertions that treated it as a hard requirement were removed.
4. **Batch settlement ignores the alias pricing mode** — open, tracked in issue #1473. The batch success path is **not** blocked: Phase 15's local executor bypasses LiteLLM's `/v1/files` and `/v1/batches` entirely and runs each line through `/v1/chat/completions`, `Submitter.shouldUseLocalExecutor` routes any non-OpenAI, non-Anthropic provider (so, `openrouter`) to it, `BATCH_EXECUTOR_KIND` defaults to `auto`, and the demo box logs `batch local executor ready (concurrency=8 kind=auto)` at boot today. Do not cite the old LiteLLM `purpose=batch` provider list as a reason the path cannot run. What is actually wrong: `DefaultCreditPolicy.Credits` (`apps/control-plane/internal/batchstore/executor/dispatcher.go`) charges one credit per token and never reads the alias's pricing mode or price columns, and `hive-auto`, the only batch-capable alias in the live catalog, is `upstream_actual`. Nothing has been mispriced yet only because no batch has completed. Treat this as an armed defect, not a latent one.
5. **Capability-based tool routing** — Resolved by Phase 20 wave 3 (PR #206, 2026-06-11). Custom providers are DB-managed (PR #199); `tools`/`tool_choice`/`response_format` route per-route on `tools_supported` in `provider_capabilities`. Tenant model visibility (PR #205) is enforced at catalog/model-listing level, not inside `SelectRoute` dispatch, which filters on `AllowedAliases`/`AllowedProviders`.

## Project State

- **v1.0 — developer-api-core**: shipped 2026-04-21. Phases 1-10 complete.
- **v1.1 — closed out late July 2026**; the phase-numbering frame (phases 12-20) is retired as a tracking device. Work now tracks through GitHub issues and pull requests.
- **Milestones**: four are open — `v1.2 agentic surface`, `v1.2.1 demo readiness hotfixes`, `Hive Enterprise edge-first v1`, `v1.3 device era`. Three older ones are closed. Issue counts are deliberately not written here because they rot within days; read them live with `gh api "repos/sakibsadmanshajib/hive/milestones?state=all"`. Roadmap board (private, so it 404s unauthenticated): https://github.com/users/sakibsadmanshajib/projects/3
- **Recent major work** (detail in `.wolf/decisions.md` D-031 to D-044 and the vault timeline):
  - Money path: credit unit per million tokens (D-031), one alias one price on `model_aliases` (D-032), fail-closed money path verified live (D-034); billing repaired 2026-08-03.
  - Chat front end: the Open WebUI fork IS the product shell; D-040 retires the LibreChat migration (D-028) permanently; frontend built from forked source with one Hive navigation (PR #938); OWUI reduced to a view over control-plane-owned state (D-044).
  - Data plane: self-hosted Supabase cutover on the demo box (PRs #982-#993, Aug 2026); CI decoupled from the live database with throwaway Postgres (PR #983).
  - Agent surface: sandbox token streaming (PR #920), Anthropic Messages compatibility fixes (PRs #954, #964), the agent surface moved into the composer as a mode rather than a separate destination (issue #944, closed).

Planning ground truth (milestone state, roadmap, requirements traceability, UAT results, deferred scope) lives in the project vault (Obsidian), not in-repo.
