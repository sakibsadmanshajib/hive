# Hive

OpenAI-compatible and Anthropic-compatible API gateway. v1.0 is a full Go rewrite, shipped for lean hot-path latency, precise `math/big` financial calculations, and source-level control over routing, sanitization, and billing.

**One product, two modes:**
- **Hive** (cloud SaaS, Bangladesh market, prepaid credit billing on BDT payment rails via Stripe, bKash, SSLCommerz)
- **Hive Enterprise** (customer-hosted, data-sovereign, for regulated sectors: finance, legal, healthcare, government)

**Core surfaces:**
- Chat (forked and heavily modified Open WebUI)
- Coding agents (agent-console sidecar plus agent-engine Apptainer sandbox)
- RAG (`/v1/rag/chat` with admin-selectable embedding model and dimension)
- Voice (Groq STT/TTS)
- Artifacts hosting
- Desktop app (Tauri, Windows/Linux, in development)
- Developer console (API keys, billing, model catalog)

**API surfaces:**
- OpenAI-compatible (`/v1/chat/completions`, `/v1/models`, etc.)
- Anthropic-compatible (`/v1/messages`, read `docs/anthropic-sdk-integration.md`)
- Provider-agnostic routing (OpenRouter, Groq, and future providers)

## Getting Started

All services run in Docker. No host Go or Node required.

### 1. Environment

```bash
cp .env.example .env
# Fill in:
#   SUPABASE_URL, SUPABASE_ANON_KEY, SUPABASE_SERVICE_ROLE_KEY, SUPABASE_DB_URL
#   NEXT_PUBLIC_SUPABASE_URL, NEXT_PUBLIC_SUPABASE_ANON_KEY
#   S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY, S3_REGION, S3_BUCKET_FILES, S3_BUCKET_IMAGES
#   OPENROUTER_API_KEY or GROQ_API_KEY (at least one)
```

`edge-api` and `control-plane` fail fast if required S3 or Supabase vars are missing. Supabase Storage is the only object storage backend. Enable S3 protocol in Supabase Storage and pre-create `hive-files` and `hive-images` buckets before starting the stack.

Payment rail keys (Stripe, bKash, SSLCommerz) are optional; services start without them.

### 2. Apply migrations

```bash
supabase db push                     # If Supabase CLI is linked
# Or apply supabase/migrations/* in order via the Supabase SQL editor
```

### 3. Start the stack

```bash
cd deploy/docker

# Local dev: core stack with in-stack Redis and Open WebUI
docker compose --env-file ../../.env --profile local up --build

# Cloud: core services expecting managed Upstash Redis (set REDIS_URL=rediss://... in .env)
docker compose --env-file ../../.env --profile cloud up --build

# Cloud with chat frontend
docker compose --env-file ../../.env --profile cloud --profile chat up --build

# Enterprise: core + in-stack Redis + Open WebUI + Caddy (self-hosted single box)
# Optional: add Ollama for local inference (set OLLAMA_BASE_URL and uncomment entries in deploy/litellm/config.yaml)
docker compose \
  -f docker-compose.yml \
  -f docker-compose.enterprise.yml \
  --env-file ../../.env --profile enterprise up --build

# Add monitoring: Prometheus, Grafana, Alertmanager to any profile
docker compose --env-file ../../.env --profile local --profile monitoring up --build
```

### 4. Verify services

| Service | URL | Healthcheck |
|---------|-----|-------------|
| Edge API | http://localhost:8080 | `GET /health` |
| Control Plane | http://localhost:8081 | `GET /health` |
| Web Console | http://localhost:3000 | — |
| LiteLLM | http://localhost:4000 | — |
| Open WebUI | http://localhost:3002 | `--profile local` |
| Prometheus | http://localhost:9090 | `--profile monitoring` |
| Grafana | http://localhost:3001 (`admin/admin`) | `--profile monitoring` |

### 5. Stop the stack

```bash
cd deploy/docker
docker compose down              # Stop services, keep volumes
docker compose down -v           # Stop and remove volumes (DB/cache/images)
```

## Using the APIs

### OpenAI-compatible SDK

```bash
export OPENAI_API_KEY="<API_KEY>"
export OPENAI_BASE_URL="https://api-hive.scubed.co/v1"

# Python
pip install openai
python -c "
from openai import OpenAI
client = OpenAI()
msg = client.chat.completions.create(
    model='hive-fast',
    messages=[{'role': 'user', 'content': 'Hello'}]
)
print(msg.choices[0].message.content)
"

# JavaScript / TypeScript
npm install openai
node -e "
const OpenAI = require('openai');
const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: 'https://api-hive.scubed.co/v1'
});
client.chat.completions.create({
  model: 'hive-fast',
  messages: [{role: 'user', content: 'Hello'}]
}).then(msg => console.log(msg.choices[0].message.content));
"
```

### Anthropic-compatible SDK

See [`docs/anthropic-sdk-integration.md`](docs/anthropic-sdk-integration.md) for the exact integration path, base URL, and tested model aliases.

```bash
# Python example
from anthropic import Anthropic

client = Anthropic(
    base_url="https://api-hive.scubed.co",
    api_key="<API_KEY>",
)

message = client.messages.create(
    model="hive-fast",
    max_tokens=256,
    messages=[{"role": "user", "content": "Hello"}],
)
print(message.content[0].text)
```

## Testing

All tests run through Docker. No host Go or Node required.

### Go unit tests

The toolchain image has `ENTRYPOINT ["/bin/sh","-c"]`, so pass the command string directly. Wrapping it in `bash -c` silently runs nothing.

```bash
cd deploy/docker

docker compose --profile tools run toolchain \
  "cd /workspace && go test ./apps/control-plane/... -count=1 -short"

docker compose --profile tools run toolchain \
  "cd /workspace && go test ./apps/edge-api/... -count=1 -short"
```

### Frontend

Build and unit tests. **Important:** without `--build`, `docker compose run` exercises the last-built stale image and reports a false green.

```bash
cd deploy/docker

docker compose run --build web-console npm run build
docker compose run --build web-console npm run test:unit
```

### SDK integration tests

Requires the core stack to be healthy.

```bash
cd deploy/docker
docker compose --env-file ../../.env --profile test up --build
```

### Playwright E2E (web-console)

Web E2E needs the full stack running (web-console SSRs through control-plane for billing and profile pages).

```bash
# Ensure core stack is running (from deploy/docker):
docker compose --env-file ../../.env up --build

# Run all E2E specs
cd apps/web-console
npx playwright test

# Specific test file
npx playwright test tests/e2e/profile-completion.spec.ts

# View HTML report after failures
npx playwright show-report
```

E2E requires three environment variables (no defaults, since defaults are credentials committed to a public repo that the seeder writes to a live account):

```bash
export E2E_RUN_KEY="$(whoami)-$(date +%s)"           # unique per run
export E2E_VERIFIED_PASSWORD="<at least 6 chars>"
export E2E_UNVERIFIED_PASSWORD="<at least 6 chars>"
export E2E_INVITATION_TOKEN="<at least 16 chars>"
```

See `docs/live-test-auth.md` for the full authentication protocol.

## Architecture

| Component | Role | Language |
|-----------|------|----------|
| `apps/control-plane` | Accounts, billing, credits, API keys, payments, routing, catalog | Go 1.24 |
| `apps/edge-api` | Auth, rate limiting, dispatch, streaming, file/media | Go 1.24 |
| `apps/web-console` | Developer console (billing, keys, analytics, catalog) | Next.js 15 / React 19 / TS 5.8 |
| `apps/agent-engine` | Agent task sandbox (Apptainer rootless) | Go + Apptainer |
| `vendor/open-webui` | Chat frontend (vendored fork, heavily modified) | Svelte + Node |
| `packages/openai-contract` | OpenAI spec and support matrix | — |
| `packages/sdk-tests` | JS / Python / Java SDK integration tests | — |
| `deploy/docker` | Compose + Dockerfiles | — |
| `deploy/litellm` | LiteLLM config and routing rules | — |
| `supabase/migrations` | Postgres schema (40+ migrations) | SQL |

## Tech Stack

| Component | Tech | Version |
|-----------|------|---------|
| APIs | Go | 1.24 |
| Web console | Next.js / React / TypeScript | 15 / 19 / 5.8 |
| Chat frontend | Svelte / Node | vendored fork |
| Database | Postgres (Supabase or self-hosted) | — |
| Cache | Redis | 8.4 |
| Model routing | LiteLLM | stable |
| Agent sandbox | Apptainer | rootless |
| Monitoring | Prometheus / Grafana / Alertmanager | — |

## Request flow

```
client (OpenAI or Anthropic SDK)
   │
   ▼
edge-api (auth, rate limit, key lookup, sanitize)
   │
   ▼
litellm (provider routing)
   │
   ▼
OpenRouter / Groq / (self-hosted inference on Enterprise)
```

Billing and catalog reads are served by control-plane, consumed by edge-api (ledger writes, routing decisions) and web-console (SSR of billing/account state).

## Standards & Conventions

- **Immutability**: Create new objects, never mutate existing. Ledger is append-only.
- **Commits**: `<type>: <description>` — types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`. No dash punctuation between clauses.
- **Secrets**: Environment variables only. Never commit `.env`.
- **Provider-blind errors**: Sanitize at both control-plane and edge boundaries. Provider names never leak to customers.
- **`math/big` for money**: All financial calculations use `math/big.Decimal` to prevent float64 corruption.
- **Storage**: Supabase Storage (S3-compatible) is the only object storage backend. Both `edge-api` and `control-plane` fail fast if required S3 env vars are missing.

## Repository Layout

```
apps/                           Go services + web-console
  control-plane/                Billing, routing, catalog, auth
  edge-api/                      Request dispatch, rate limits
  web-console/                   Developer console (Next.js)
  agent-engine/                  Agent task sandbox
  
vendor/                          Forked dependencies
  open-webui/                    Chat frontend (heavily modified)
  openhands/                     (included, not used in core path)
  
packages/
  openai-contract/              OpenAI spec + support matrix
  sdk-tests/                     Integration tests (JS/Python/Java)
  
supabase/migrations/            Postgres schema
deploy/
  docker/                        Compose + Dockerfiles
  litellm/                       LiteLLM config
  prometheus/ grafana/           Monitoring stacks
  apptainer/                     Agent sandbox image definition
  cloudflare/                    Tunnel and DNS config
  
docs/                            User-facing guides + architecture
scripts/                         Operational utilities
```

## Project State

- **v1.0 — Core developer API** (shipped 2026-04-21): OpenAI-compatible surface, provider routing, chat frontend, developer console
- **v1.1 — In progress**: Anthropic API, provider catalog, coding agents, RAG, enterprise features
- **Roadmap**: https://github.com/users/sakibsadmanshajib/projects/3

Planning ground truth, UAT results, and deferred scope live in the project vault (Obsidian), not in-repo. See `CLAUDE.md` and `.claude/rules/openwolf.md` for operational and development standards.

## Key Documents

- **Integration guide** — `docs/anthropic-sdk-integration.md` for Anthropic SDK setup
- **Testing** — `docs/live-test-auth.md` for E2E authentication protocol
- **Agent runtime** — `deploy/apptainer/README.md` for sandbox image and configuration
- **Operational** — `CLAUDE.md` for testing gotchas, agent engine setup, and regulatory stance
- **Development** — `.claude/rules/orchestrator.md` for coding pipeline and merge policy
- **Chat fork** — The Open WebUI frontend is a vendored fork, heavily modified; see `CLAUDE.md` for licensing and architecture notes

## Known Limitations

- **Open WebUI licence carve-out**: The branding modification (removing upstream's "(Open WebUI)" suffix) is permitted under the Open WebUI licence only while deployments stay at or under 50 end users per rolling 30 days, or hold written vendor permission, or hold an enterprise licence. This constraint is tracked but not gated; current deployments are within the threshold.
- **Extended thinking, prompt caching, and server-side tools**: Anthropic-specific features like `thinking`, `cache_control`, `container`, `inference_geo` are not implemented. Neither OpenRouter nor Groq (the configured providers) support Anthropic's native versions, so this is a provider-capability gap, not an oversight.
- **Batch API success path**: The `/v1/batches` endpoint processes submissions and failures correctly, but the success path (`status=completed`) is not exercisable with the current provider mix. LiteLLM's managed file upload (`POST /v1/files` with `purpose=batch`) only supports OpenAI, Azure, Vertex AI, and Anthropic; OpenRouter and Groq have no native batch API.

## Support

For issues, questions, or contributions, open a GitHub issue or see `.github/MERGE-POLICY.md` for the PR workflow and merge gate requirements.
