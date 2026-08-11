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

### Agent-engine runtime (required for agent/coding surfaces)

The agent-engine runs each session inside an Apptainer SIF built from `deploy/apptainer/agent-engine.def`. It is `linux/amd64` only and cannot be built on WSL2.

Control-plane never execs Apptainer itself. It hands each launch to an unprivileged host launcher over a Unix socket, so the SIF has to sit on the host beside that launcher, not inside a container.

```bash
# On the host, once per deploy. Fetches the CI-built .sif if the host has none,
# builds the launcher, and restarts its systemd user unit.
HIVE_AGENT_ENGINE_LLM_MODEL=openai/hive-default \
HIVE_AGENT_ENGINE_LLM_BASE_URL=https://api-hive.scubed.co/v1 \
HIVE_AGENT_ENGINE_LLM_API_KEY=<gateway key> \
CONTROL_PLANE_INTERNAL_TOKEN=<internal token> \
  bash scripts/install-agent-engine-host.sh
```

Then point control-plane at the socket in `.env`:

```dotenv
HIVE_AGENT_ENGINE_SOCKET_DIR=/home/<user>/agent-runtime/run
HIVE_AGENT_ENGINE_SOCKET=/run/hive-agent/engine.sock
```

`deploy-demo-box.yml` already does both on every deploy. `HIVE_AGENT_SIF_PATH` is a different variable that gates nothing here: it only feeds the standalone binary's `-sif` default and the compose smoke-test service. Full detail, including the in-process fallback arm and its five variables: `deploy/apptainer/README.md`.

### Verify

| Service | URL | Check |
|---|---|---|
| Edge API | http://localhost:8080/health | 200 |
| Control Plane | http://localhost:8081/health | 200 |
| Web Console | http://localhost:3000 | loads |
| Open WebUI | http://localhost:3003 | loads |
| Caddy (OWUI proxy) | http://localhost:8090 | loads |
| Artifacts | via caddy-artifacts | static served |

RAG needs more than a health check, because its failure modes are silent
(a document lands with `status=error`, or search answers 503, while every
service still reports healthy). Prove the whole path instead:

```bash
export EDGE_API_URL=http://localhost:8080   # or the deployed edge origin
python3 scripts/verify-rag-roundtrip.py     # needs the SUPABASE_* vars from .env
```

It uploads a document with a unique marker, waits for embedding, then requires
the marker back out of vector search and out of the grounded answer. Prints
PASS or the first step that could not be proven. Note that the serverless
embedding route is slow and uneven, so the script retries each step a few
times and prints every attempt.

The chat account you will present from needs one more check, because Open WebUI
keeps "Enter Key Behavior" as a per-user setting and a shared account carries
whatever the last automated run left on it. With that preference on, Enter
inserts a newline instead of sending, the send arrow still works, and there is
no error anywhere on screen (issue #855, found on the demo account on
2026-08-11).

```bash
cd apps/web-console
node scripts/demo-chat-settings.mjs <demo account email>            # exit 1 on drift
node scripts/demo-chat-settings.mjs <demo account email> --repair   # correct it
```

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
- **Knowledge** (chat, Workspace > Knowledge): create a collection, upload a document, then attach it to a chat with the `+` menu > Attach Knowledge and ask something only that document answers. Proves grounded answers over the customer's own documents in the chat surface itself, and the answer carries a source citation. This is Open WebUI's own store (pgvector `document_chunk`), which is a different store from the `/v1/rag/chat` API below. Limit: the answer takes roughly 30 seconds because a single gateway embedding call currently costs 7 to 17 seconds (#865), so narrate over the wait rather than hiding it. The empty create form is refused by the browser's own `required` validation, which is correct behaviour and not a broken button (#832).
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
