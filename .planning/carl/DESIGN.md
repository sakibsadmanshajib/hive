# Carl.sh — Sovereign Workspace Design Document

Author: CTO (architect synthesis), 2026-06-25. Owner retains veto on every decision.

Scope and authority: this document closes the gaps named in `.planning/carl/CHARTER.md`
against the existing `fundmoreai/hive` repository. It is gap-close, not greenfield. Every
technology choice below was validated against a live source on 2026-06-25; citations are inline.
The enterprise edge box is the primary product. The cloud co-work workspace is a later demo.

## 0. Validated context and ground truth

### 0.1 What already exists (verified in repo)

| Surface | Location | Status |
|---------|----------|--------|
| Inference dispatch core | `apps/edge-api/internal/inference/handler.go` (`Handler.ServeHTTP`) | Live. Path-dispatched. |
| Chat orchestration | `apps/edge-api/internal/chat/dispatch.go` (`chat.NewDispatch`) | Live. Forwards to LiteLLM, SSE passthrough. |
| Embeddings handler | `apps/edge-api/internal/inference/embeddings.go` (`handleEmbeddings`) | Live. Upstream only (OpenRouter). |
| Auth selector | `apps/edge-api/internal/auth/selector.go` (`auth.Selector`), `middleware.go`, `jwt_supabase.go` | Live. `Bearer hk_*` to API-key path, else Supabase JWT. Context via `auth.UserFrom(ctx)`. |
| Audio handler | `apps/edge-api/internal/audio/handler.go` | Routes `/v1/audio/speech`, `/v1/audio/transcriptions`, `/v1/audio/translations` exist; resolve through `RoutingInterface.SelectRoute(RouteInput{NeedSTT,NeedTTS})`. No STT backend wired. |
| Files handler | `apps/edge-api/internal/files/handler.go` (`StorageBackend` interface, Supabase S3) | Live. Multipart upload to `hive-files` bucket; metadata registered via control-plane `filestore`. |
| Control-plane modules | `apps/control-plane/internal/{catalog,routing,providers,accounting,apikeys,audit,filestore,payments,identity,budgets}` | Live. |
| LiteLLM routing | `deploy/litellm/config.yaml` | Live. OpenRouter + Groq fanout, embedding routes, Ollama stubs (commented, installer uncomments). |
| Installer | `scripts/install.sh` | Live. Docker bootstrap, `.env` wizard, hardware advisor, `--with-ollama`, `--uninstall`, profiles. |
| Compose profiles | `deploy/docker/docker-compose.yml` | Live. `local`/`cloud`/`chat`/`enterprise`; services: edge-api, control-plane, litellm, redis, web-console, open-webui, caddy-owui, ollama, monitoring. |
| Migrations | `supabase/migrations/` | Live. **pgvector NOT enabled. No vector column anywhere.** Embeddings referenced by `alias_id` only. |

The single most important seam: adding a new dialect is one new `case` in
`inference/handler.go` `ServeHTTP` plus one translator. It reuses the existing auth wrapper,
the existing LiteLLM forward, and the existing SSE plumbing. We never rebuild dispatch.

### 0.2 Validated external facts (sources)

- Anthropic Messages spec: content-block model, `tool_use`/`tool_result`, `stop_reason` enum
  (`end_turn`, `max_tokens`, `stop_sequence`, `tool_use`, `pause_turn`, `refusal`), `usage`
  with `input_tokens`/`output_tokens`, and the streaming event sequence. Source: Anthropic SDK
  type definitions via Context7 (`/anthropics/anthropic-sdk-python` — `message.py`,
  `helpers.md`, `examples/tools.py`) and the Anthropic streaming docs.
- pgvector: `vector` up to 2,000 dims, `halfvec` up to 4,000 dims; HNSW and IVFFlat indexes;
  operators `<->` (L2), `<#>` (neg inner product), `<=>` (cosine); `CREATE EXTENSION vector`.
  HNSW needs no training step and can be built on an empty table. Source:
  `github.com/pgvector/pgvector` README (fetched 2026-06-25).
- Parakeet self-host, two paths both speaking OpenAI Whisper API natively:
  (a) `achetronic/parakeet` — Go + ONNX Runtime 1.25.x, exposes `/v1/audio/transcriptions`
  on port 5092, `Bearer` auth, `response_format`, `stream=true`, ffmpeg for non-WAV, CPU-capable.
  (b) NVIDIA Riva/Speech NIM — exposes `/v1/audio/transcriptions` on port 9000, GPU.
  Source: `achetronic/parakeet` README, NVIDIA NIM Speech docs (fetched 2026-06-25).
- Parakeet `parakeet-tdt-0.6b-v3`: 0.6B FastConformer-TDT, 25 languages, **24 evaluated, all
  European; Bangla/Bengali NOT in the set.** Source: `huggingface.co/nvidia/parakeet-tdt-0.6b-v3`.
- LiteLLM audio: `model_info.mode: audio_transcription` registers a transcription model; the
  generic OpenAI-compatible adapter (`model: openai/<id>` + `api_base`) points LiteLLM at any
  self-hosted Whisper-compatible server. Source: `docs.litellm.ai/docs/audio_transcription`.
- LiteLLM Ollama: `ollama_chat/<model>` + `api_base` for chat; native provider. Source:
  `docs.litellm.ai/docs/providers/ollama`.
- Headscale: open-source self-hosted Tailscale control server. Ships an **embedded DERP relay**
  (enable in `config.yaml` `derp.server.enabled: true`), uses STUN udp/3478 plus HTTPS tcp/443
  for relay, gives port-less NAT traversal under your own control. Source: `headscale.net/stable`
  and `/ref/derp/`. Raw WireGuard has no out-of-band coordinator, so it cannot hole-punch behind
  symmetric/CGNAT without manual port-forward. Source: Tailscale NAT-traversal docs and comparison.

---

## 1. Gap 1 — Anthropic-compatible `/v1/messages` (issue #168)

### 1.1 Decision

Add a thin translation layer that accepts Anthropic Messages requests, lowers them to the
internal OpenAI chat shape, drives the existing dispatch core, and lifts the OpenAI response (and
SSE stream) back into Anthropic shape. We never call real Anthropic. The model field maps to a
local or open model alias via the existing catalog. This is a pure adapter: zero new inference,
zero new provider integration.

### 1.2 Why a translator, not a parallel path

The chat dispatch core already forwards to LiteLLM and streams SSE. Anthropic and OpenAI differ
only in request and response envelope shape, not in the underlying token generation. A translator
keeps one inference path, one billing hook, one audit trail. Building a second native path would
double the surface that the security and billing reviewers must cover.

### 1.3 Request mapping (Anthropic to OpenAI)

| Anthropic Messages field | OpenAI chat field | Notes |
|--------------------------|-------------------|-------|
| `model` | `model` | Resolved through catalog alias to a local/open model. |
| `system` (string or `TextBlock[]`) | prepend a `{"role":"system"}` message | Concatenate text blocks. |
| `messages[].role` (`user`/`assistant`) | `messages[].role` | Direct. |
| `messages[].content` string | `content` string | Direct. |
| content block `{"type":"text"}` | text part | Direct. |
| content block `{"type":"image","source":{base64}}` | `image_url` with `data:` URI | Vision passthrough. |
| content block `{"type":"tool_use","id","name","input"}` | assistant `tool_calls[]` (`id`, `function.name`, `function.arguments` as JSON string) | `input` object becomes stringified `arguments`. |
| content block `{"type":"tool_result","tool_use_id","content"}` | `{"role":"tool","tool_call_id","content"}` | Map `tool_use_id` to `tool_call_id`. |
| `tools[]` (`name`,`description`,`input_schema`) | `tools[]` (`type:"function"`, `function.parameters`) | `input_schema` becomes `function.parameters`. |
| `tool_choice` (`auto`/`any`/`tool`) | `tool_choice` (`auto`/`required`/named) | `any` to `required`; `{type:"tool",name}` to named function. |
| `max_tokens` (required) | `max_tokens` | Anthropic requires it; default-guard if omitted. |
| `stop_sequences` | `stop` | Direct. |
| `temperature`,`top_p` | same | Direct. |
| `stream` | `stream` | Direct. |

### 1.4 Response mapping (OpenAI to Anthropic, non-streaming)

OpenAI `choices[0].message` becomes an Anthropic `message`:
- `id` to `id` (prefix `msg_`), `role:"assistant"`, `model` echoed.
- `message.content` text becomes a single `{"type":"text"}` block.
- each `tool_calls[]` becomes a `{"type":"tool_use","id","name","input"}` block (`arguments`
  JSON-parsed back into `input`).
- `finish_reason` to `stop_reason`: `stop` to `end_turn`, `length` to `max_tokens`,
  `tool_calls` to `tool_use`, `content_filter` to `refusal`. Validated against the Anthropic
  `stop_reason` enum.
- `usage.prompt_tokens`/`completion_tokens` to `usage.input_tokens`/`output_tokens`.

### 1.5 Streaming SSE event mapping

OpenAI streams `chat.completion.chunk` deltas. Anthropic streams a structured event sequence.
The translator is a stateful SSE re-emitter sitting in front of the existing SSE passthrough:

1. On first upstream chunk: emit `message_start` (with the message envelope, `usage.input_tokens`,
   `stop_reason: null`), then `content_block_start` (index 0, `text` block).
2. For each text delta: emit `content_block_delta` with `{"type":"text_delta","text":...}`.
3. When the upstream emits a `tool_calls` delta: close any open text block with
   `content_block_stop`, open a new `content_block_start` (`tool_use`, with `id`,`name`), then
   emit `content_block_delta` with `{"type":"input_json_delta","partial_json":...}` for the
   streamed `arguments` fragments.
4. On stream end: `content_block_stop` for the open block, then `message_delta` carrying the
   final `stop_reason` and `usage.output_tokens`, then `message_stop`.
5. Periodic `ping` events are permitted and ignored by clients.

This sequence matches the documented Anthropic order: `message_start`, `content_block_start`,
repeated `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`.

### 1.6 Files and handlers to add

| File (new) | Role |
|------------|------|
| `apps/edge-api/internal/anthropic/types.go` | Anthropic request/response/event structs (no `any`; structurally typed unions for content blocks via a tagged decoder). |
| `apps/edge-api/internal/anthropic/translate_request.go` | `ToOpenAIChat(req MessagesRequest) (chat.Request, error)`. |
| `apps/edge-api/internal/anthropic/translate_response.go` | `FromOpenAIChat(resp chat.Response) MessagesResponse`. |
| `apps/edge-api/internal/anthropic/stream.go` | `NewSSETranslator(w http.ResponseWriter) *SSETranslator` — the stateful re-emitter. |
| `apps/edge-api/internal/anthropic/handler.go` | `Handler` that decodes, translates, calls the shared chat dispatch, and writes the Anthropic envelope or stream. |

Wiring: one line in `apps/edge-api/cmd/server/main.go` alongside the existing chat route:
`mux.Handle("/v1/messages", auth.Selector(anthropicJWTHandler, anthropicAPIKeyHandler))`,
reusing the same auth wrappers and the same `chat.Dispatch` instance. Also map
`/v1/messages/count_tokens` to a local estimator (tiktoken-equivalent) returning `{input_tokens}`,
since the Anthropic SDK probes it.

### 1.7 Edge cases and risks

- Interleaved text and multiple tool_use blocks in one assistant turn: the SSE translator must
  track block indices. Covered by step 3 above.
- `tool_result` content can be a string or a block array (text or image): normalize to OpenAI
  tool message content. Image-in-tool-result is rare; pass through as `image_url`.
- `pause_turn` and `refusal` stop reasons have no OpenAI equivalent inbound; only emitted outbound
  on `content_filter`. Document as best-effort.
- The Anthropic SDK sends `anthropic-version` header; accept and ignore (we are version-agnostic).

---

## 2. Gap 2 — RAG: upload, chunk, embed, store, search, ground

### 2.1 Decision

Add document upload, chunking, local embedding (routed through LiteLLM), PGVector storage, a
vector-search endpoint, and RAG-grounded chat. Reuse the existing files upload path and the
existing embeddings handler; add the vector layer underneath. Local embeddings ship via Ollama
(an embedding model) so the edge box has zero external dependency; OpenRouter stays a test/cloud
convenience only.

### 2.2 Postgres schema (validated against pgvector)

Default embedding dimension is **768** (a common open-model dimension, e.g. `nomic-embed-text`
on Ollama). pgvector `vector` supports up to 2,000 dims, so 768 and 1024 are both safe as native
`vector`. We make the dimension a deploy-time constant so the schema and the configured embedding
model agree.

```sql
-- supabase/migrations/<date>_carl_rag.sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE public.rag_documents (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL,
    owner_sub    text NOT NULL,
    file_id      text,                       -- links to existing filestore record
    title        text NOT NULL,
    mime_type    text NOT NULL,
    status       text NOT NULL DEFAULT 'pending',  -- pending|chunking|embedded|failed
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.rag_chunks (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id  uuid NOT NULL REFERENCES public.rag_documents(id) ON DELETE CASCADE,
    tenant_id    uuid NOT NULL,
    chunk_index  int  NOT NULL,
    content      text NOT NULL,
    token_count  int  NOT NULL,
    embedding    vector(768) NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- HNSW: better speed-recall than IVFFlat, builds on empty table (no training step).
-- Cosine distance matches normalized open-model embeddings.
CREATE INDEX rag_chunks_embedding_hnsw
    ON public.rag_chunks USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX rag_chunks_tenant_doc ON public.rag_chunks (tenant_id, document_id);

-- Row-level security keyed on tenant_id, consistent with existing identity model.
ALTER TABLE public.rag_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.rag_chunks    ENABLE ROW LEVEL SECURITY;
```

Index choice rationale (validated): HNSW gives the better speed-recall tradeoff and needs no
training step, so it works from an empty table; IVFFlat would need data first and a tuned `lists`.
Cosine (`vector_cosine_ops`, `<=>`) is correct for normalized sentence embeddings. If a future
embedding model exceeds 2,000 dims, switch the column to `halfvec(<=4000)` with `halfvec_cosine_ops`.

### 2.3 Pipeline

1. Upload: reuse `files` handler to land the raw document in the `hive-files` bucket and register
   filestore metadata. RAG ingestion takes the `file_id`.
2. Extract + chunk: control-plane worker pulls the file, extracts text (txt/md/pdf/docx),
   chunks by ~512 tokens with ~64-token overlap, writes `rag_documents` + `rag_chunks` rows with
   `embedding` left null and `status=chunking`.
3. Embed: for each chunk, call the edge embeddings path (LiteLLM `route-local-embedding`, an
   Ollama embed model on the edge box; OpenRouter fallback only in cloud/test). Store the vector,
   set `status=embedded`.
4. Search: `POST /v1/rag/search {query, top_k, document_ids?}` embeds the query and runs
   `ORDER BY embedding <=> $queryvec LIMIT top_k`, RLS-scoped to the caller's tenant.
5. Ground: RAG-grounded chat is a server-side option (`{"rag":{"enabled":true,"top_k":N}}` on the
   chat/messages request, or a dedicated `/v1/rag/chat`) that runs search, injects the retrieved
   chunks as system context with citations, then calls the normal dispatch core.

### 2.4 Endpoints and wiring

| Endpoint (new) | Layer | Role |
|----------------|-------|------|
| `POST /v1/rag/documents` | edge-api `internal/rag/handler.go` | Register a file for ingestion (or accept inline upload, delegating to `files`). |
| `GET /v1/rag/documents`, `GET /v1/rag/documents/{id}` | edge-api | List/status. |
| `DELETE /v1/rag/documents/{id}` | edge-api | Cascade delete chunks. |
| `POST /v1/rag/search` | edge-api `internal/rag/search.go` | Vector search, returns chunks + scores. |
| `POST /v1/rag/chat` (or `rag` flag on chat) | edge-api | Grounded generation. |
| ingestion worker | control-plane `internal/rag/` | Chunk + embed + write, async. |
| embedding route | `deploy/litellm/config.yaml` add `route-local-embedding` (Ollama embed model) | Local-first embeddings. |

New files: `apps/edge-api/internal/rag/{handler.go,search.go,types.go,repository.go}`,
`apps/control-plane/internal/rag/{ingest.go,chunk.go,repository.go}`, the migration above, and a
LiteLLM `route-local-embedding` entry. Database work owned by `database-reviewer`; vector queries
parameterized (no string interpolation of vectors).

---

## 3. Gap 3 — Voice / STT with NVIDIA Parakeet, self-hosted

### 3.1 Decision

Serve Parakeet as a sidecar container that already speaks the OpenAI Whisper API, then point
LiteLLM at it and wire the existing `/v1/audio/transcriptions` route through to it. Default serving
path is the **`achetronic/parakeet` Go + ONNX server** because it is CPU-capable, single-binary,
ships ffmpeg in its image, and exposes `/v1/audio/transcriptions` natively. On GPU edge boxes,
NVIDIA Riva/Speech NIM is the optional higher-throughput path; both speak the same endpoint so the
wiring is identical.

### 3.2 Why this serving path (validated tradeoff)

- `achetronic/parakeet`: Go + ONNX Runtime 1.25.x, port 5092, `Bearer` auth, `response_format`,
  `stream=true`, ffmpeg for MP3/OGG/etc. CPU-only inference is supported, so it runs on the
  no-GPU edge tier. Model files downloaded separately (owner provides host or download per charter).
- NVIDIA Riva/Speech NIM: port 9000, GPU, higher throughput, official. Heavier footprint, requires
  NGC access and a GPU. Reserve for the GPU edge tier.
- NeMo directly: a training/research toolkit, not a server. Rejected as the default serving path;
  it is the source the ONNX models are exported from.

Decision: ship the ONNX Go server as the default sidecar; allow swapping in Riva NIM by env var
because the endpoint contract is identical.

### 3.3 Wiring

1. Compose: add a `parakeet` service to `docker-compose.yml` (new profile tag `voice`, composable
   with `enterprise`). Mount the model directory the owner provides; expose 5092 on the internal
   network only.
2. LiteLLM: add a transcription model entry pointing at the sidecar via the generic adapter:
   ```yaml
   - model_name: route-local-transcription
     litellm_params:
       model: openai/parakeet                  # generic OpenAI-compatible adapter
       api_base: http://parakeet:5092/v1
       api_key: os.environ/PARAKEET_API_KEY
     model_info:
       mode: audio_transcription
   ```
3. edge-api: the `/v1/audio/transcriptions` route already exists in `internal/audio/handler.go`
   and resolves through `RoutingInterface.SelectRoute(RouteInput{NeedSTT:true})`. Add a catalog
   alias + provider_route so STT resolves to `route-local-transcription`. No new edge handler;
   only the routing seed and the LiteLLM entry.
4. Result: customers call OpenAI-standard `POST /v1/audio/transcriptions` on the Hive edge; the
   audio never leaves the box.

### 3.4 Bangla relevance (issue #178)

`parakeet-tdt-0.6b-v3` covers 25 languages, all European; **Bangla is not in the set** (verified
on the model card). Consequences and plan:

- STT (speech to text) in Bangla is NOT delivered by Parakeet v3. Do not claim Bangla ASR via
  Parakeet. Options for Bangla STT, deferred and tracked under #178: a Bangla-capable Whisper
  variant served through the same OpenAI-compatible sidecar contract (the wiring is identical), or
  a future Bangla Parakeet/NeMo checkpoint.
- Bangla TEXT generation already has a path: the installer advisor flags `qwen3:8b` as
  Bangla-capable on the 8 GB tier, so RAG and chat in Bangla work via Ollama today. The sidecar
  contract makes adding Bangla STT later a config change, not a rebuild.

---

## 4. Gap 4 — Self-hosted relay for port-less edge remote access

### 4.1 Decision

Default is LAN serve via the box's own Caddy (already in the chat/enterprise profiles). For remote
access without opening firewall ports, ship an **optional Headscale** coordination server with its
**embedded DERP relay enabled**, and have edge nodes plus clients join via the standard Tailscale
client against our own Headscale. No Tailscale SaaS, no external relay keys. Raw WireGuard is the
fallback for the simple case where the operator can do a static port-forward.

### 4.2 Why Headscale over raw WireGuard (validated)

- Raw WireGuard has no out-of-band coordinator, so two peers behind symmetric NAT or CGNAT cannot
  hole-punch; the operator must port-forward or set a static public endpoint. That violates the
  "port-less" goal on consumer/enterprise NAT.
- Headscale is the open-source Tailscale control server and ships an **embedded DERP relay**
  (`derp.server.enabled: true`), using STUN udp/3478 for discovery and HTTPS tcp/443 for relayed
  packets. DERP over 443 is effectively unblockable and needs no inbound port-forward on the edge.
  All relays run on the customer's own hardware. This delivers port-less remote access while
  staying fully self-hosted.
- Tradeoff: relayed mode (the ~5% of connections that cannot go direct) drops throughput (tens of
  Mbps) and adds latency due to TCP head-of-line blocking; most connections still go direct
  WireGuard. Acceptable for a control/console channel and light inference; document it.

### 4.3 How Carl.sh sets it up (optional, seamless)

- New installer flag `--with-relay`. When set, the installer:
  1. Adds a `headscale` service to compose (new profile `relay`).
  2. Generates a Headscale `config.yaml` with `derp.server.enabled: true` and the box's public
     IPs (detected or prompted), all secrets auto-generated, nothing external.
  3. Creates a Headscale user and a preauthkey, registers the edge node, and prints a single
     join command plus a QR for client devices.
  4. Leaves the Tailscale client install to the user's devices (one command), against our
     Headscale URL.
- When `--with-relay` is absent, nothing changes: LAN-only via Caddy, zero new surface.
- Security note: Headscale ACLs restrict which client devices can reach the edge services; the
  security reviewer owns the default-deny ACL and the preauthkey TTL.

---

## 5. Gap 5 — Containerized co-work workspace (demo, after edge core)

### 5.1 What "co-work ChatGPT" means here

A multi-user shared AI workspace on the edge box: several authenticated users in one tenant share
(a) a common chat surface, (b) a shared RAG corpus (the tenant's documents), and (c) shared or
visible sessions/threads, all grounded on the tenant's own documents and served by the local model.
It is the team-facing front end over the same edge APIs, not a new inference engine.

### 5.2 Decision: self-contained isolated container first

Build it as one isolated container component layered on the existing chat front end (Open WebUI is
already in the `chat` profile behind Caddy). Concretely:

- Reuse Open WebUI as the multi-user shell (it already does users, auth, chat threads), pointed at
  the Hive edge `/v1` (and `/v1/messages`) instead of any external API.
- Add the RAG surface (Gap 2) as the shared corpus: documents uploaded by a tenant are searchable
  by every user in that tenant via RLS scoping on `tenant_id`.
- Sessions: shared threads are a tenant-scoped table; "co-work" = thread visibility within a tenant.
- Everything stays in one compose component set so the demo is `docker compose --profile cowork up`.

Online sandbox services (Daytona or similar) are explicitly **later**: they would host
per-user ephemeral dev environments, which is out of scope until the edge core is solid. The
OpenCode coding agent is deferred and is not the target.

### 5.3 Why container-first

The edge product's whole promise is "runs entirely on the customer server." A self-contained
container honors that and keeps the demo reproducible. Cloud sandboxing is a hosting optimization
that can come after the sovereign edge story is proven.

---

## 6. The Carl.sh seamless installer plan

### 6.1 Principle

`scripts/install.sh` is the base and already does the hard parts (Docker bootstrap, `.env` wizard,
hardware-aware model advisor, profile selection, Ollama enablement, uninstall). Carl.sh is the same
installer extended with the new deltas behind opt-in flags, adding **no unnecessary complexity**:
one command brings up Docker, all deps, the model pull, and the hardware advisor.

### 6.2 Extensions (additive, all opt-in)

| Flag | Effect |
|------|--------|
| `--with-ollama` (exists) | Local inference + hardware advisor + model pull. |
| `--with-rag` (new) | Applies the RAG migration (`CREATE EXTENSION vector` + tables), seeds `route-local-embedding`, pulls the embed model. |
| `--with-voice` (new) | Adds the `parakeet` sidecar, seeds `route-local-transcription`, mounts the model dir the owner provides. |
| `--with-relay` (new) | Adds Headscale + embedded DERP, generates config + preauthkey, prints join command. |
| `--cowork` (new, demo) | Brings up the co-work profile (Open WebUI shell + shared RAG). |

Default `curl ... | bash` with no flags: the existing enterprise edge stack serving OpenAI and
(after Gap 1 lands) Anthropic dialects against a hardware-advised local model. Each flag is a
self-contained step appended to `main()` in the existing piping-safe structure. The advisor already
recommends Bangla-capable `qwen3:8b` on the 8 GB tier, so the edge speaks Bangla text out of the box.

### 6.3 Carl.sh naming

`Carl.sh` is the public entrypoint name for this installer. Implementation stays in
`scripts/install.sh`; `Carl.sh` is the curl target alias and brand. No second installer is created.

---

## 7. Build sequence, dependency graph, and parallelization

### 7.1 Dependency graph

```
                 [Repo baseline: dispatch core, auth, files, LiteLLM, installer]
                        |                 |                 |              |
        +---------------+        +--------+         +-------+        +-----+
        v                        v                  v                v
  (A) Anthropic /v1/messages  (B) RAG vector   (C) Parakeet STT  (D) Relay (Headscale)
   - types/translate/stream     - migration       - sidecar+compose  - compose+config
   - reuses chat.Dispatch       - rag endpoints    - LiteLLM entry    - installer flag
   - reuses auth.Selector       - ingest worker    - routing seed     - ACLs
        |                        - LiteLLM embed    - (audio route     (fully independent)
        |                        - installer flag      already exists)
        |                              |                  |
        +-----------+------------------+------------------+
                    v
        (E) Installer flags wiring (--with-rag/--with-voice/--with-relay)
                    |
                    v
        (F) Co-work workspace demo (needs B for shared RAG, A for Anthropic in shell)
                    |
                    v
        (G) E2E: installer to dual-dialect + RAG + voice + relay green
```

### 7.2 What can run in parallel (separate builder teams)

**Wave 1 (fully independent, four teams concurrently):**
- Team A — Anthropic `/v1/messages` translator (Go, edge-api `internal/anthropic/`). Depends only
  on the existing dispatch core. Reviewer: `go-reviewer` + `security-reviewer` (input boundary).
- Team B — RAG: migration + `internal/rag` (edge + control-plane) + LiteLLM embed route. Reviewer:
  `database-reviewer` (schema, vector queries, RLS) + `go-reviewer`.
- Team C — Parakeet sidecar + LiteLLM transcription entry + routing seed. Mostly compose/config;
  small Go for the catalog seed. Reviewer: `go-reviewer` + `security-reviewer` (no audio egress).
- Team D — Headscale relay: compose service + config generator + installer `--with-relay`.
  Independent of A/B/C. Reviewer: `security-reviewer` (ACLs, key TTL) + `go-reviewer` for installer.

**Wave 2 (after its inputs):**
- Team E — installer flag wiring for `--with-rag`/`--with-voice`/`--with-relay`. Needs B, C, D
  artifacts to exist (the migration, the sidecar, the Headscale config). Small, sequential after
  Wave 1 merges.

**Wave 3:**
- Team F — co-work workspace demo. Needs B (shared RAG) and A (Anthropic in the shell). After E.

**Wave 4:**
- Team G — `e2e-runner` full-path verification: install via Carl.sh, hit `/v1/chat/completions`,
  `/v1/messages`, `/v1/rag/search`, `/v1/audio/transcriptions`, and a relayed remote call.

Critical path: A and B are the long poles (most code + review). C and D are short and should
finish first, de-risking the installer wiring early. F is gated on A+B.

### 7.3 Per-team guardrails (from the orchestrator contract)

Each builder works only in its own worktree, verifies `git status -sb` after checkout, pushes
`git push origin HEAD:<branch>`, confirms the remote ref, and never touches the shared checkout.
Builder self-reports are not verification; an independent reviewer reads each pushed diff. Merge
gate: all checks green plus zero unresolved threads, then squash merge with branch deletion.

---

## 8. Test strategy (Grok and free local models as test-only)

### 8.1 Principle

Grok (xAI) and free OpenAI-compatible local models are **test-time conveniences, never shipped
runtime dependencies**. They exist to exercise the dialect translators and the RAG/voice paths
without burning a real provider, and they are wired only behind the `test` profile and test env.

### 8.2 Layers

- **Unit (Go):** table-driven tests for the Anthropic translator (request lowering, response
  lifting, the full SSE event sequence including interleaved tool_use), and for RAG chunking +
  vector query construction. No network. Run via the Docker toolchain per CLAUDE.md.
- **Contract:** drive `/v1/messages` with the **real Anthropic SDK** (the SDK is already a dev
  dep pattern in `packages/sdk-tests`) pointed at the Hive edge `base_url`, asserting the SDK
  parses our responses and streams. Pointed at a local free model as the backing engine, or Grok
  as the upstream behind LiteLLM for a richer model during the test only.
- **RAG E2E:** upload a fixture doc, assert chunks + embeddings land, assert `/v1/rag/search`
  returns the right chunk, assert grounded chat cites it. Embeddings via the local embed model.
- **Voice E2E:** post a fixture WAV to `/v1/audio/transcriptions`, assert transcript. Parakeet
  sidecar with the owner-provided model in the test profile.
- **Relay E2E:** bring up Headscale + DERP in the test profile, register two nodes, assert a
  relayed request to the edge succeeds with no inbound port-forward.
- **Installer E2E:** run Carl.sh in a clean container with each flag, assert the health endpoints
  and the seeded routes. Owned by `e2e-runner`.

### 8.3 Test config isolation

Grok and free-model keys live only in `.env.test` and the `test`/`tools` compose profiles. The
shipped `enterprise` profile references neither. A guard test asserts no test-only provider key is
read on the enterprise path, protecting the "zero external keys at the edge" guarantee.

---

## 9. Top technical risks

1. **Anthropic SSE fidelity.** The streaming event sequence (block indices, interleaved text and
   multiple tool_use, `input_json_delta`) is the highest-bug-density area; a malformed sequence
   breaks the real Anthropic SDK silently. Mitigation: contract test against the real SDK in CI.
2. **Embedding dimension and model lock-in.** The `vector(768)` column must match the configured
   embed model forever; changing the model orphans stored vectors. Mitigation: dimension is a
   deploy constant, a re-embed migration path is documented, and the column can move to `halfvec`
   if a bigger model is ever chosen.
3. **Parakeet has no Bangla.** Marketing or #178 could over-promise Bangla voice. Mitigation: this
   doc states Bangla ASR is not delivered by Parakeet v3; only the sidecar contract is reusable for
   a future Bangla STT model. Bangla text generation is the only Bangla claim today.
4. **Relay throughput and operability.** DERP-relayed connections are slow (TCP HOL blocking) and
   Headscale is operationally heavier than a port-forward; a misconfigured ACL could expose edge
   services. Mitigation: default-deny ACL owned by security review, relay strictly opt-in, document
   it as control/light-inference channel not bulk transport.
5. **Self-hosted Supabase/pgvector at the edge.** The repo assumes Supabase-hosted Postgres, but
   the sovereign edge must run Postgres+pgvector locally with zero external SaaS. Mitigation:
   confirm the enterprise profile ships a local Postgres with the `vector` extension and that the
   filestore S3 backend has a local/MinIO-free equivalent, or this breaks the zero-SaaS promise.
   (Flagged for the owner in Section 11.)

---

## 10. Recommended parallel build sequence (summary)

- **Now, in parallel:** Team A (Anthropic), Team B (RAG), Team C (Parakeet), Team D (Relay).
- **Then:** Team E (installer flag wiring) once B/C/D artifacts exist.
- **Then:** Team F (co-work demo) once A+B merged.
- **Finally:** Team G (full E2E via Carl.sh).
- Short poles C and D land first to de-risk the installer; A and B are the critical path.

---

## 11. Decisions that genuinely need the owner

1. **Edge data plane sovereignty (highest priority).** The repo today depends on Supabase-hosted
   Postgres and S3-protocol storage. The sovereign edge promise is "zero external SaaS." Confirm
   the plan: ship a **local Postgres (with pgvector) and local object storage** inside the
   enterprise box, with Supabase reserved for the cloud demo only. Without this, RAG vectors and
   uploaded documents would leave the customer's server, contradicting the core pitch.
2. **Embedding model + dimension lock.** Approve the default local embed model and its dimension
   (proposed `nomic-embed-text`, 768) so the migration and the LiteLLM route are fixed. This is a
   one-way door once vectors are stored.
3. **Bangla voice expectation (#178).** Confirm it is acceptable that v1 ships Bangla **text**
   only (via Ollama) and that Bangla **STT** is deferred behind the same sidecar contract, since
   Parakeet v3 has no Bangla.
4. **Parakeet default serving tier.** Approve the CPU-capable `achetronic/parakeet` ONNX Go server
   as the default sidecar, with NVIDIA Riva NIM as the GPU upgrade, given both speak the identical
   endpoint. (CTO default: yes.)
