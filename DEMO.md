# Hive Enterprise Demo Guide

Operational guide for bringing up and demonstrating **Hive Enterprise**: the customer-hosted, data-sovereign, OpenAI-compatible AI gateway (Go control-plane + edge-api, Next.js web-console, Open WebUI chat, agent-engine Apptainer sandbox, Tauri desktop). One org equals one tenant, departments via RBAC.

## Bring-up (enterprise profile)

The enterprise profile is the self-hosted single box: core services, in-stack Redis, Open WebUI, Caddy for OWUI, and Caddy for artifacts.

```bash
# 1. Latest + env
git checkout main && git pull origin main
cp .env.example .env
# Fill: SUPABASE_* + NEXT_PUBLIC_SUPABASE_*, S3_* (buckets hive-files + hive-images
# must exist), and at least one of OPENROUTER_API_KEY / GROQ_API_KEY.
# RAG's embedding model and dimension are admin-selectable (packages/embedmodel)
# and drive dynamic vector-column provisioning. .env.example already ships a
# working demo default (qwen3-embedding-8b, MRL-reduced to 1024-dim), and RAG
# works out of the box once OPENROUTER_API_KEY and LITELLM_MASTER_KEY are set.

# 2. Migrations
supabase db push        # or apply supabase/migrations/ in order

# 3. Bring up
cd deploy/docker
docker compose \
  -f docker-compose.yml \
  -f docker-compose.enterprise.yml \
  --env-file ../../.env --profile enterprise up --build
```

Optional local inference: set `OLLAMA_BASE_URL=http://ollama:11434` in `.env` and uncomment the ollama entries in `deploy/litellm/config.yaml`.

### Agent-engine SIF (required for agent/coding surfaces)

The agent-engine runs each session inside an Apptainer SIF built from `deploy/apptainer/agent-engine.def`. It is `linux/amd64` only and cannot be built on WSL2.

```bash
# On an apptainer host:
make agent-sif          # -> deploy/apptainer/agent-engine.sif
# Or pull the CI-built image:
gh run download -n agent-engine-sif -D /opt/hive
```

Set `HIVE_AGENT_SIF_PATH` in `.env` to the resulting `.sif` path.

### Verify

| Service | URL | Check |
|---|---|---|
| Edge API | http://localhost:8080/health | 200 |
| Control Plane | http://localhost:8081/health | 200 |
| Web Console | http://localhost:3000 | loads |
| Open WebUI | http://localhost:3003 | loads |
| Caddy (OWUI proxy) | http://localhost:8090 | loads |
| Artifacts | via caddy-artifacts | static served |

Do not present until all are green.

## Demo walkthrough (per surface)

For each surface: what to show, what it proves, and the current limit to narrate around.

- **Control panel** (`/console`): analytics, api-keys (create one live), billing + invoice PDF, catalog + provider manager, feature-gates, MCP marketplace, members + RBAC, budget/spend-alerts. Proves a full on-box operator surface. All wired.
- **Chat** (Open WebUI, `:3003` or Caddy `:8090`): send a message routing through edge-api to `/v1/*`. Proves chat on the sovereign gateway. Limit: OWUI OIDC login not fully built (#269); use the seeded user.
- **Cowork / Agents** (agent-console UI or `POST /internal/agent-tasks`): task launches inside the Apptainer sandbox. Proves on-box autonomous agents. Limit: pack routing not wired, always default pack (#311); no public agent API (#382).
- **Coding agent** (coding-pack via OpenHands): coding task edits/runs code in the sandbox. Proves on-box coding. Limit: no CLI, no GitHub-native tooling (#389).
- **Desktop app** (Tauri): show the Linux shell launching a sandbox. Proves sandbox hardening (Linux: bwrap + Landlock + seccomp + egress-proxy). **Limit (do not hit live): Linux runs a placeholder `/bin/echo`, not a real agent runtime; Windows launch disabled; authed-session/license handoff incomplete (#310).** Present as "hardening proven, runtime integration in progress."
- **Connectors / MCP**: local admin-curated marketplace becomes OpenHands `mcpServers` JSON bind-mounted into the SIF. Proves operator-curated tools. Limit: remote/OAuth MCP out of scope (#309); no one-click install (#390).
- **Policies / RBAC / Settings**: role assignment, policy toggles, sovereign posture. Proves departmental separation in one tenant. Limit: no SSO admin config (#388), no SCIM (#385).
- **RAG** (`/v1/rag/chat`): ingest a doc, ask a grounded question. Proves retrieval over the customer's own docs. Embedding model and dimension are admin-selectable (`packages/embedmodel`), and the vector column plus HNSW index are provisioned dynamically to match. Demo default: qwen3-embedding-8b MRL-reduced to 1024-dim (`vector(1024)`, HNSW cosine), a native Matryoshka reduction, not truncation. Works with the `.env.example` defaults; no separate setup beyond the standard `OPENROUTER_API_KEY` / `LITELLM_MASTER_KEY` config.
- **Voice** (`/v1/audio`): transcription via the OpenAI-Whisper-compatible API (parakeet en, faster-whisper bangla). Proves on-box speech-to-text. Limit: confirm input is not garbled with a clean sample first.
- **Artifacts** (`/v1/artifacts` + isolated caddy-artifacts): publish a static artifact, open it on the isolated host. Proves isolated static hosting. Limit: no persistent/API/live-data artifacts (#381).

Not built, do not demo: Projects (#380), cross-chat Memory (#172), preset Environments.

## Known limitations

| Area | State | Issue |
|---|---|---|
| Projects | not built | #380 |
| Artifacts (persistent / API / live-data) | static only | #381 |
| Managed Agents API (public) | not built | #382 |
| Background / async agents + resume | not built | #383 |
| Destructive / prompt-injection classifier | not built | #384 |
| SCIM provisioning | not built | #385 |
| Audit SIEM / OTEL export | not built | #386 |
| Compliance API | not built | #387 |
| SSO admin config | enum-keys only | #388 |
| GitHub-native coding tools | not built | #389 |
| One-click MCP install | not built | #390 |
| OWUI OIDC login | not fully built | #269 |
| Desktop runtime (real agent) | placeholder/echo, Windows disabled | #310, #312, #319 |
| Agent pack routing | always default pack | #311 |
| Remote / OAuth MCP | out of scope | #309 |

Business/compliance certifications (SOC2, ISO, GDPR, HIPAA) are a separate sovereign track (#219-227).
