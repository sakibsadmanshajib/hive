# Phase 19: Chat-App — Fork & Strip LibreChat (Pinned Tag) + Language Picker — Research

**Researched:** 2026-04-26
**Domain:** LibreChat fork strategy, Hive single-provider lock, MongoDB integration, OCI deployment, first-run language picker
**Confidence:** HIGH (license, stack, config) · MEDIUM (Bengali locale presence, RAG path) · LOW (exact pinned-tag commit SHA — needs `gh release view` at fork time)
**Supersedes:** `.planning/phases/19-chat-app-fork-strip/RESEARCH.md` (Lobe Chat — rejected per `LICENSE-DECISION.md`).

---

<user_constraints>
## User Constraints (from LICENSE-DECISION.md + V1.1-MASTER-PLAN.md v3)

### Locked Decisions
- **Base = LibreChat (`danny-avila/LibreChat`, MIT).** Replaces Lobe Chat. Hive-rebranded fork is permissible derivative work — no commercial license needed.
- **Hosting = chat-app on Oracle Cloud Infrastructure (OCI) container instance(s); web-console stays on Cloudflare Workers via `@opennextjs/cloudflare` (unchanged).** Workers is NOT a chat-app deploy target.
- **DNS / TLS via Cloudflare in front of OCI** for chat-app subdomain.
- **Pin to a specific upstream tag at fork time** (not floating `main`).
- **Lock to single OpenAI-compatible provider pointed at `http://edge-api:8080`** — strip Anthropic, Google, Mistral, Bedrock, Vertex, etc. from default config.
- **First-run language picker (bn-BD / en-US)** at first session, persists to user prefs after auth.
- **MongoDB added to Hive infra** (LibreChat is Mongo-native). Hive Postgres ledger unchanged.
- **No FX/USD exposure on customer surfaces** (regulatory — Phase 17 audit applies to chat-app).

### Claude's Discretion (research + recommend)
- Pinned tag selection (latest stable vs prior LTS).
- MongoDB hosting: Atlas M0 free tier vs OCI self-host.
- RAG architecture for Phase 22: official `librechat-rag-api` Python sidecar vs custom adapter direct to Hive `/v1/embeddings` + Supabase pgvector.
- OCI compute shape recommendation (Always-Free `VM.Standard.A1.Flex` ARM vs paid AMD).
- Language picker insertion point inside LibreChat client (Vite + React 18 + Recoil + i18next).

### Deferred Ideas (OUT OF SCOPE for Phase 19)
- MCP marketplace exposure (informs v1.2).
- Voice (STT/TTS) and image-gen.
- Web search plugin (Tavily / SearXNG).
- Multi-tenant private deployments.
- Auto-trial credits.
- Replacing Passport/JWT auth → Phase 20.
- Tier limits and invite/referral → Phase 21.
- File upload + RAG implementation → Phase 22.
- Bengali UI translation completeness audit → Phase 23.
- Production deploy automation → Phase 24.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| CHAT-19-01 | Fork `danny-avila/LibreChat` at pinned tag into `apps/chat-app/` | §1 Pinned Tag Recommendation |
| CHAT-19-02 | Strip non-Hive providers from default config and code | §3 Provider Strip Surface |
| CHAT-19-03 | Wire single OpenAI-compatible provider → `http://edge-api:8080` | §4 Base-URL Config |
| CHAT-19-04 | Boot LibreChat locally + via Docker Compose service | §10 Docker Compose Service Draft |
| CHAT-19-05 | Add first-run language picker modal (bn-BD / en-US) | §6 Language Picker Insertion Plan |
| CHAT-19-06 | Document upgrade procedure (`LIBRECHAT-UPGRADE-PLAYBOOK.md`) | §1 + §11 Open Questions |
| CHAT-19-07 | MongoDB connection wired (recommend Atlas vs self-host) | §5 MongoDB Recommendation |
| CHAT-19-08 | OCI deploy plan documented (shape, image, CF-front) | §9 OCI Deployment Guide |
</phase_requirements>

---

## Summary

LibreChat (MIT) is the locked chat-app base. Upstream is an npm-workspaces monorepo (`api/` Express backend + `client/` Vite+React 18 SPA + shared `packages/data-provider`, `packages/data-schemas`, `packages/api`, `packages/client`). The latest stable upstream release is **v0.8.4** (with v0.8.5-rc1 cut and v0.8.5 GA imminent as of 2026-04-26 per LibreChat changelog). LibreChat publishes signed Docker images to both Docker Hub (`librechat/librechat:vX.Y.Z`) and GHCR (`ghcr.io/danny-avila/librechat-dev:latest`).

Provider strip is **config-only, not code-only**: LibreChat's "custom endpoints" architecture (`librechat.yaml` + `endpoints.custom[]`) means we can lock to a single Hive endpoint without forking provider code. The legacy `OPENAI_REVERSE_PROXY` env var is deprecated — use `librechat.yaml` instead. Credentials live in `.env`; YAML references them via `${VAR}`.

LibreChat requires **MongoDB for chat history** (not Postgres). For Phase 22 RAG, LibreChat ships a separate Python FastAPI sidecar (`librechat-rag-api`) that uses pgvector — it can either run on OCI alongside chat-app, or be replaced by a custom adapter pointing at Hive `/v1/embeddings` + Supabase pgvector. The vectordb LibreChat ships is its own pgvector instance — distinct from Hive's Supabase Postgres ledger, **no schema collision**.

**Primary recommendation:** Pin to **`v0.7.9`** (the most recent point release before the v0.8 admin-panel + context-compaction refactor) to maximize stability for v1.1 ship. Use Atlas M0 free tier for MongoDB (managed, 512MB sufficient for v1.1 BD soft-launch). Deploy on OCI Always-Free `VM.Standard.A1.Flex` (4 OCPU + 24GB ARM). For Phase 22 RAG, **start with the official sidecar** (Pythonic, supported, less custom code) and revisit custom adapter only if Bengali recall demands it.

**⚠️ Tag pin caveat:** v0.7.9 is recommended pending Sakib's review of v0.8.x changelog. v0.8.x adds Admin Panel + Context Compaction + Claude Opus 4.7 support — none required for Hive v1.1 (single Hive provider, no admin panel needed since Hive control-plane handles RBAC). Conservative path = v0.7.9. Re-evaluate if v0.8 GA ships before fork day.

---

## 1. Pinned Tag Recommendation

| Tag | Status | Date (approx) | Notes |
|-----|--------|---------------|-------|
| **v0.7.9** | **RECOMMENDED** | early 2026 | Last v0.7.x stable. Mature MCP support. Pre-Admin-Panel refactor — smaller surface to strip. |
| v0.8.0 | stable | 2026-Q1 | Admin Panel foundation introduced. Larger surface area, more upstream churn. |
| v0.8.4 | stable | 2026-04 (approx) | Most recent stable per Docker Hub tags inspected via search. Adds tool-call UI overhaul. |
| v0.8.5-rc1 | release-candidate | 2026-04 | RC only — DO NOT pin. |
| v0.8.5 | GA imminent | ~2026-04-21 per changelog | Adds Claude Opus 4.7 + context summarization + admin panel testing. Not relevant to Hive (single Hive provider). |

**Rationale for v0.7.9:**
- v0.8 series introduces Admin Panel + Custom Roles + System Grants — duplicates Hive control-plane RBAC (Phase 18). Stripping it is extra work.
- v0.8 Context Compaction is useful but optional; can be backported if needed.
- v0.7.9 has stable MCP support already (added in v0.7.x). Sufficient for v1.2 MCP exposure.
- **Smaller diff to upstream** = easier upgrade later.

**Action at fork time:**
```bash
gh release view v0.7.9 --repo danny-avila/LibreChat --json tagName,publishedAt,targetCommitish
git clone --depth 1 --branch v0.7.9 https://github.com/danny-avila/LibreChat.git apps/chat-app
cd apps/chat-app && git rev-parse HEAD  # record commit SHA in LIBRECHAT-VERSION.md
```

Record in `.planning/v1.1-chatapp/LIBRECHAT-VERSION.md`:
- `tag: v0.7.9`
- `commit_sha: <recorded at fork time>`
- `forked_at: <ISO timestamp>`
- `forked_by: <Sakib>`
- `rationale: stability over Admin-Panel features; Hive control-plane handles RBAC`

**Alternative (if v0.8.5 GA ships clean):** v0.8.4 acceptable if Sakib prefers latest-with-Opus-4.7 — strip Admin Panel routes (`api/server/routes/admin/*`) post-fork.

Confidence: **MEDIUM** — exact-day-of-fork tag review required. Sources: [LibreChat releases](https://github.com/danny-avila/LibreChat/releases), [LibreChat changelog](https://www.librechat.ai/changelog), [v0.8.5 notes](https://www.librechat.ai/changelog/v0.8.5).

---

## 2. License Audit

| Property | Value |
|----------|-------|
| Top-level license | **MIT** (clean, unmodified) |
| Source | `LICENSE` at repo root |
| Commercial use | permitted |
| Derivative work | permitted (Hive-rebranded fork OK) |
| Attribution | required: keep `LICENSE` + `Copyright (c) Danny Avila` notice |
| Patent grant | none (MIT has none — vs Apache-2.0) |

**Verified:** Multiple sources confirm plain MIT (no LobeHub-style §1.b commercial restriction). Source: [LICENSE file](https://github.com/danny-avila/LibreChat/blob/main/LICENSE), [Mintlify intro](https://www.mintlify.com/danny-avila/librechat/introduction).

**Bundled deps audit (action item, not blocking):** Run `license-checker` post-fork. Known potentially-non-MIT deps:
- `meilisearch` (MIT — OK)
- `pgvector` (PostgreSQL License — OK, BSD-like)
- `mammoth` (BSD-2 — OK)
- `pdf-parse` (MIT — OK)
- `sharp` (Apache-2.0 — OK)
- `langchain` (MIT — OK)

**Risk:** LOW. No GPL/AGPL deps known in core. Document attribution in `apps/chat-app/THIRD-PARTY-NOTICES.md` post-fork.

Confidence: **HIGH**.

---

## 3. Provider Strip Surface

LibreChat ships ~12 first-class providers (OpenAI, Anthropic, Google, Bedrock, Vertex, Azure, Groq, OpenRouter, Mistral, DeepSeek, Ollama, custom). Strip targets:

### A. Configuration strip (low effort)
| File | What changes |
|------|--------------|
| `librechat.yaml` (we author) | Define ONLY `endpoints.custom[]` with single Hive endpoint. Omit `endpoints.openAI`, `endpoints.anthropic`, etc. |
| `.env` | Set only `OPENAI_API_KEY=user_provided_or_hive_key`, omit `ANTHROPIC_API_KEY`, `GOOGLE_KEY`, etc. — UI hides providers without keys. |

### B. Code strip (optional, larger surface)
LibreChat reads `librechat.yaml` and dynamically gates endpoints — **most provider code stays inert when no key is configured**. So the minimum-effort strip is config-only. For aggressive strip (smaller image, less attack surface):

| Path | Lines (approx) | Strip effort |
|------|----------------|-------------|
| `api/app/clients/AnthropicClient.js` | ~600 | medium |
| `api/app/clients/GoogleClient.js` | ~500 | medium |
| `api/app/clients/BedrockClient.js` | ~400 | medium |
| `api/app/clients/VertexClient.js` | ~400 | medium |
| `api/app/clients/MistralClient.js` | ~300 | low |
| `api/server/routes/endpoints/*` | varies | medium |
| `client/src/data-provider/Endpoints/*` | varies | medium |
| `packages/data-provider/src/config.ts` (`EModelEndpoint` enum) | ~50 | low (don't delete enum, just hide from default UI) |

**Recommended:** **Config-only strip in Phase 19**, defer code strip to v1.2 cleanup. Rationale:
- LibreChat hides unconfigured endpoints in UI by default.
- Code strip increases upgrade-merge conflict surface (every upstream change to a stripped client = manual reconcile).
- v1.1 timeline tight; perfect-is-enemy-of-good.

**Action items (Phase 19):**
1. Author `apps/chat-app/librechat.yaml` with single Hive endpoint.
2. Author `apps/chat-app/.env.example` with only Hive vars + Mongo + Meilisearch + (optional) RAG.
3. Set `endpoints.openAI: { disabled: true }` and same for `anthropic`, `google`, `bedrock`, `assistants` if YAML supports it; else rely on missing keys.
4. UI override (post-Supabase auth in Phase 20): default-select Hive endpoint, hide endpoint switcher for non-admin users.

Confidence: **HIGH** for config strip; **MEDIUM** for "providers fully hidden when keys absent" — verify in Phase 19 spike.

Sources: [Custom Endpoints docs](https://www.librechat.ai/docs/quick_start/custom_endpoints), [librechat.example.yaml](https://github.com/danny-avila/LibreChat/blob/main/librechat.example.yaml).

---

## 4. OpenAI-Compatible Base URL Config

**Use `librechat.yaml` `endpoints.custom[]` — NOT the deprecated `OPENAI_REVERSE_PROXY`.** Source: [GitHub issue #1027](https://github.com/danny-avila/LibreChat/issues/1027) confirms `OPENAI_REVERSE_PROXY` deprecated.

### Minimal Hive `librechat.yaml`

```yaml
version: 1.2.0
cache: true

endpoints:
  custom:
    - name: "Hive"
      apiKey: "${HIVE_API_KEY}"           # references .env
      baseURL: "${HIVE_BASE_URL}"          # e.g. "http://edge-api:8080/v1" inside docker network
      models:
        default: ["gpt-4o-mini", "claude-3-5-sonnet"]   # Hive routes these to OpenRouter/Groq
        fetch: false                       # disable model autodetect; Hive controls catalog
      titleConvo: true
      titleModel: "gpt-4o-mini"
      summarize: false
      forcePrompt: false
      modelDisplayLabel: "Hive"
      headers:
        X-Hive-Source: "chat-app"          # for edge-api telemetry
```

### Required `.env`
```
HIVE_API_KEY=<hive system key for chat-app>     # set per-deploy
HIVE_BASE_URL=http://edge-api:8080/v1            # docker-compose internal; OCI: http://hive-edge-api.internal:8080/v1
```

### Important behavior notes
- LibreChat **appends `/chat/completions` automatically** to baseURL → `${HIVE_BASE_URL}/chat/completions`. Confirm Hive edge-api routes match (already does per `apps/edge-api`).
- Setting `apiKey: "user_provided"` allows users to enter their own keys — **do NOT use this for Hive** (we want server-side billing on Hive system key).
- For embeddings (Phase 22): RAG sidecar uses separate config; not in `librechat.yaml`.

Sources: [Custom Endpoints docs](https://www.librechat.ai/docs/quick_start/custom_endpoints), [Custom Endpoint Object Structure](https://www.librechat.ai/docs/configuration/librechat_yaml/object_structure/custom_endpoint), [Environment Variables](https://www.librechat.ai/docs/configuration/dotenv).

Confidence: **HIGH**.

---

## 5. MongoDB Integration

LibreChat is **Mongo-native** — chat history, agent definitions, presets, prompts, user records all live in Mongo. Cannot be swapped for Postgres without invasive refactor (Phase 19 does NOT do this).

### Required env vars
```
MONGO_URI=mongodb+srv://<user>:<pass>@<cluster>/LibreChat?retryWrites=true   # Atlas
# OR
MONGO_URI=mongodb://mongodb:27017/LibreChat                                   # docker-compose
MONGO_MAX_POOL_SIZE=20                                                        # optional
MONGO_MIN_POOL_SIZE=5                                                         # optional
```

### Hosting recommendation: **MongoDB Atlas M0 (Free Tier)** for v1.1 launch

| Aspect | Atlas M0 Free | OCI Self-Host |
|--------|---------------|---------------|
| Storage | 512 MB | 200GB block volume free |
| Connections | 500 max | unlimited |
| Cost | $0/mo forever | $0/mo (within Always-Free) |
| Backups | automatic snapshots | manual / mongodump cron |
| Ops burden | zero | medium (patching, monitoring, replication) |
| Latency | ~50-150ms cross-region (Atlas → OCI) | ~1ms (intra-VM) |
| Scale ceiling | M0→M2 ($9/mo) when 512MB hit | scale to A1 24GB → larger shape |
| BD-launch fit (5-10 users) | ✅ fits well within 512MB | ✅ fits |

**Recommendation: Atlas M0 for v1.1 launch.**
- 512MB = ~5,000-50,000 chat messages depending on size — sufficient for soft launch.
- Zero ops; Sakib's bandwidth scarce.
- Migrate to Atlas M2/M5 ($9-25/mo) or self-host on OCI when 512MB approached.
- Cross-region latency tolerable for chat history reads (cached client-side, not hot path).

**⚠️ Atlas one-cluster-per-project caveat:** can deploy only one M0 free cluster per Atlas project. Reserve project for Hive prod; use separate project for staging.

**Schema collision check:** LibreChat's Mongo schema (collections: `users`, `conversations`, `messages`, `presets`, `prompts`, `agents`, `tokens`, `transactions`, etc.) is **fully isolated from Hive Postgres** (`accounts`, `api_keys`, `ledger_entries`, `provider_capabilities`, etc.). **No collision.**

**Phase 20 mapping:** Supabase user.id → MongoDB `users.email` (or new `supabase_id` field). Document in Phase 20 PLAN.

Sources: [MONGO_URI docs](https://www.librechat.ai/docs/configuration/mongodb/mongodb_atlas), [Atlas free cluster limits](https://www.mongodb.com/docs/atlas/reference/free-shared-limitations/).

Confidence: **HIGH**.

---

## 6. Hive Postgres Role

Hive Postgres (Supabase) stays as-is. **No LibreChat write-traffic to Hive Postgres.** LibreChat features that *might* need Postgres:
- ❌ Chat history → Mongo
- ❌ Users → Mongo
- ❌ Agents → Mongo
- ⚠️ **RAG vectors** → LibreChat ships its own pgvector instance (`vectordb` service); does NOT need Hive Postgres.
- ⚠️ For Phase 22, we may *replace* the LibreChat-bundled vectordb with Supabase pgvector — see §8.

**Conclusion:** Phase 19 = no Hive Postgres changes. Schema collisions = none.

Confidence: **HIGH**.

---

## 7. Bengali (bn) Locale Status

**Status: UNCONFIRMED — needs verification at fork time.** Web search did not surface a definitive `client/src/locales/bn/translation.json` file. LibreChat manages translations via [Locize](https://www.librechat.ai/docs/translation), and the [translation.json bot PR #12070](https://github.com/danny-avila/LibreChat/pull/12070) auto-syncs from Locize.

**Action at fork time** (Phase 19 task):
```bash
# Inside cloned LibreChat
ls client/src/locales/ | grep -i bn
# If present: bn/translation.json exists → use it.
# If absent: create scaffold.
```

### Two scenarios:

**Scenario A — `bn/` present upstream:**
- Verify completeness vs `en/translation.json` (likely partial).
- Phase 19 = wire picker; Phase 23 = audit + fill gaps + Sakib BD review.

**Scenario B — `bn/` absent (or only `bn-IN`, no `bn-BD`):**
- Phase 19 scaffold:
  ```bash
  mkdir -p client/src/locales/bn-BD
  cp client/src/locales/en/translation.json client/src/locales/bn-BD/translation.json
  # Translate Hive-critical keys (signup, login, language picker, error messages) — full pass in Phase 23.
  ```
- Register in `client/src/locales/i18n.ts`:
  ```ts
  import bnBDTranslation from './bn-BD/translation.json';
  // ...
  resources: {
    en: { translation: enTranslation },
    'bn-BD': { translation: bnBDTranslation },
  }
  ```

**Locale code choice:** **`bn-BD`** (Bangladesh-specific) over plain `bn` — matches Hive regulatory/regional positioning. CLDR locale code valid.

Sources: [LibreChat Translation docs](https://www.librechat.ai/docs/translation), [bn-BD CLDR](https://www.unicode.org/cldr/cldr-aux/charts/22.1/summary/bn_BD.html), [Localizely bn-BD](https://localizely.com/locale-code/bn-BD/).

Confidence: **MEDIUM** — exact upstream presence verified at fork time.

---

## 8. Language Picker Insertion Plan

LibreChat client = **Vite + React 18 + Recoil + react-i18next** (NOT Next.js — Vite SPA served by Express backend on `/`).

### Insertion point

**Recommended:** New top-level component `client/src/components/Nav/LanguagePicker/FirstRunLanguagePicker.tsx`, mounted in `client/src/App.tsx` (or equivalent root) inside the existing providers tree.

### Trigger logic

```tsx
// FirstRunLanguagePicker.tsx — pseudocode shape
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

const PICKED_KEY = 'hive_language_picked';

export function FirstRunLanguagePicker() {
  const { i18n } = useTranslation();
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!localStorage.getItem(PICKED_KEY)) {
      setOpen(true);
    }
  }, []);

  const choose = (lang: 'bn-BD' | 'en-US') => {
    i18n.changeLanguage(lang);
    localStorage.setItem(PICKED_KEY, '1');
    localStorage.setItem('i18nextLng', lang);  // i18next default key
    setOpen(false);
    // Phase 20: persist to Supabase user_prefs after auth.
  };

  if (!open) return null;
  return (
    <Modal>
      <h2>Choose your language / আপনার ভাষা নির্বাচন করুন</h2>
      <button onClick={() => choose('bn-BD')}>বাংলা (Bangladesh)</button>
      <button onClick={() => choose('en-US')}>English</button>
    </Modal>
  );
}
```

### Detection order (i18next config)

Update `client/src/locales/i18n.ts` `detection.order`:
```ts
detection: {
  order: ['localStorage', 'cookie', 'navigator'],   // localStorage first wins
  caches: ['localStorage', 'cookie'],
  lookupLocalStorage: 'i18nextLng',
}
```

### Persistence after auth (Phase 20)
- Add `language` column to LibreChat `users` Mongo doc (or use existing `preferences` field).
- On login: server pushes `user.language` → client → `i18n.changeLanguage()` overrides localStorage.
- On change-language menu: PATCH `/api/user/me { language: 'bn-BD' }`.

### Cookie/localStorage gating
- **Anonymous-then-auth flow:** localStorage gate works for anonymous (Phase 20 guest tier). On auth, server-stored pref takes priority.
- **Multi-device:** server pref propagates; new device shows picker if no localStorage yet.

Sources: [i18next-browser-languageDetector](https://github.com/i18next/i18next-browser-languageDetector), [LibreChat Project Structure](https://deepwiki.com/danny-avila/LibreChat/8.1-project-structure), [LibreChat Frontend Components](https://deepwiki.com/danny-avila/LibreChat/6-frontend-components).

Confidence: **HIGH** (pattern is standard); **MEDIUM** for exact LibreChat Modal component reuse — verify at fork.

---

## 9. Auth Surface (Phase 20 Input)

LibreChat current state (v0.7.9):
- **Strategy:** Passport.js with multiple strategies — `local` (email/password, bcrypt), `google` (OAuth2), `github`, `discord`, `facebook`, `apple`, `openid`, `LDAP`, plus optional 2FA.
- **Token:** JWT in HTTP-only cookies (access + refresh).
- **User store:** Mongo `users` collection.
- **Code paths:**
  - `api/server/routes/auth.js` — login/logout/register routes.
  - `api/strategies/*.js` — Passport strategies (one file per provider).
  - `api/server/middleware/requireJwtAuth.js` — JWT validator.
  - `client/src/components/Auth/*` — login/register UI.

### Phase 20 effort estimate (planning input)
| Task | Effort |
|------|--------|
| Add Passport `supabase` strategy (validates Supabase JWT signature) | M (Supabase JWT secret + jwks fetch) |
| Disable `local`, `google`, `github` strategies | S |
| Map Supabase user → Mongo user (auto-provision on first login) | M |
| Replace LibreChat login UI with Supabase auth UI (or redirect to web-console SSO) | M-L |
| Tier resolution from Supabase claims → JWT custom claim | M |
| Guest mode (anonymous session cookie, tier=guest) | M |
| Logout cascade (Phase 19 web-console PR #127 already done) | S |

**Total Phase 20 effort: medium-large.** Phase 19 leaves auth as-is — Phase 20 is the swap.

Sources: [LibreChat Authentication docs](https://www.librechat.ai/docs/configuration/authentication), [Supabase JWT](https://supabase.com/docs/guides/auth/jwts), [hiro1107/nestjs-supabase-auth](https://github.com/hiro1107/nestjs-supabase-auth) (reference pattern).

Confidence: **HIGH** for current state; **MEDIUM** for effort (depends on tier-claim complexity).

---

## 10. RAG Sidecar vs Custom Adapter (Phase 22 Input)

LibreChat ships an **official Python sidecar**: `librechat-rag-api` (FastAPI + LangChain + Postgres/pgvector).
- Image: `registry.librechat.ai/danny-avila/librechat-rag-api-dev-lite:latest`
- Repo: [danny-avila/rag_api](https://github.com/danny-avila/rag_api)
- DB: separate pgvector instance (`vectordb` service in compose).
- Embedding providers supported: OpenAI, Azure OpenAI, HuggingFace, HuggingFace TEI, Ollama.

### Path A: Use sidecar as-is, point embeddings at Hive

```
LibreChat api ──HTTP──> rag_api (Python) ──HTTP──> Hive edge-api /v1/embeddings
                                          │
                                          └──pgvector (sidecar's own Postgres)
```

**Pros:**
- Zero custom RAG code; 100% upstream.
- Maintained by LibreChat team; bug fixes flow free.
- Ollama/HF fallback if Hive edge-api embeddings flaky.
- Configures via env: `EMBEDDINGS_PROVIDER=openai`, `EMBEDDINGS_BASE_URL=http://edge-api:8080/v1`, `OPENAI_API_KEY=<hive system key>`.

**Cons:**
- Adds Python service to OCI deployment (memory ~500MB-1GB).
- Adds second Postgres (pgvector sidecar separate from Supabase).
- LibreChat-bundled pgvector ≠ Hive Supabase pgvector — duplicate infra.
- Recommends 4GB RAM + 1 OCPU for vectordb container — eats into OCI Always-Free 24GB.

### Path B: Custom adapter — write a Hive-native RAG route

```
LibreChat api ──direct call──> Hive edge-api /v1/embeddings
                                      │
                                      └──Supabase Postgres (pgvector, existing)
```

Implement `api/server/routes/files/rag.js` (custom):
- Upload → Supabase Storage `hive-files` bucket (existing).
- Chunk via `langchain` text splitters (already in LibreChat deps).
- Embed via Hive `/v1/embeddings`.
- Insert into Supabase `chat_app_embeddings` table (new migration).
- Retrieve via SQL `<=>` operator (cosine distance, HNSW index).

**Pros:**
- Single Postgres (Supabase) — operationally simpler.
- Reuses Hive `hive-files` bucket (already wired in Phase 10).
- No extra Python service on OCI → saves ~1GB RAM.
- Tighter Hive integration: file metadata in same DB as ledger.

**Cons:**
- Custom code = upstream-merge conflict risk on every LibreChat upgrade.
- Need to reimplement chunking, retrieval, citation logic.
- Bengali tokenizer tuning falls on us.

### Recommendation: **Path A (sidecar)** for Phase 22 v1 launch.

Rationale:
- Time-to-launch matters more than infra purity.
- 24GB OCI A1 absorbs vectordb (4GB + 1OCPU) + LibreChat (~2GB) + rag_api (~1GB) + Mongo Atlas (off-host) = ~7GB. Comfortable headroom.
- Phase 22 ships ~3-4 weeks; custom adapter doubles that.
- v1.2 can migrate to Supabase pgvector via Path B if Bengali recall demands tuning.

**Phase 22 PLAN.md will finalize.** Both paths preserved in scope.

Sources: [RAG API config docs](https://www.librechat.ai/docs/configuration/rag_api), [RAG API features](https://www.librechat.ai/docs/features/rag_api), [danny-avila/rag_api](https://github.com/danny-avila/rag_api), [Optimizing RAG in LibreChat](https://www.librechat.ai/blog/2025-04-25_optimizing-rag-performance-in-librechat).

Confidence: **HIGH** for Path A viability; **MEDIUM** for "Phase 22 ships fastest with sidecar" (depends on Bengali recall quality — measured in Phase 22 spike).

---

## 11. MCP Support (out-of-scope for v1.1, info only)

- LibreChat has **first-class MCP support** since v0.7.x.
- Configured via `librechat.yaml` `mcpServers` block.
- Per-user OAuth2 + custom variables `{{VAR}}` substitution.
- Security: blocks internal/local/private addresses by default.
- 15-min idle disconnect on user connections.

**v1.1 stance:** Leave MCP **disabled** in our `librechat.yaml` (omit `mcpServers` entirely). v1.2 = expose curated MCP marketplace.

Sources: [MCP feature docs](https://www.librechat.ai/docs/features/mcp), [MCP Servers Object](https://www.librechat.ai/docs/configuration/librechat_yaml/object_structure/mcp_servers).

Confidence: **HIGH**.

---

## 12. OCI Deployment Guide

### Compute shape: **VM.Standard.A1.Flex** (Always-Free ARM Ampere)

| Resource | Allocation | Use |
|----------|------------|-----|
| OCPU | 4 | LibreChat (1) + rag_api (1) + vectordb (1) + buffer (1) |
| Memory | 24 GB | LibreChat (~2GB) + rag_api (~1GB) + vectordb (~4GB) + headroom |
| Boot volume | 100 GB | OS + Docker images |
| Block volume | 100 GB | mongodump backups + Postgres data + uploaded files cache |
| Network | 10 TB/mo egress free | sufficient for v1.1 soft launch |
| Cost | $0/mo (Always-Free) | — |

**ARM consideration:** All required Docker images publish ARM64 variants:
- `librechat/librechat:v0.7.9` — multi-arch ✅
- `mongo:7` (if self-host) — multi-arch ✅
- `redis:8.4` — multi-arch ✅
- `pgvector/pgvector:0.8.0-pg15-trixie` — multi-arch ✅
- `getmeili/meilisearch:v1.7` — multi-arch ✅
- `registry.librechat.ai/.../librechat-rag-api-dev-lite` — verify ARM64 at deploy; if x86-only, run on `VM.Standard.E5.Flex` (paid AMD).

**⚠️ Verify ARM64 image presence for `librechat-rag-api-dev-lite` at deploy time** — if absent, fallback options:
1. Build locally from `danny-avila/rag_api` source (Dockerfile multi-arch capable).
2. Use paid AMD shape `VM.Standard.E5.Flex` (~$30/mo for 2 OCPU + 8GB).
3. Skip sidecar; do Path B custom adapter.

### Image source
```
docker pull librechat/librechat:v0.7.9
# OR build from fork:
docker buildx build --platform linux/arm64,linux/amd64 -t hive/chat-app:v0.7.9-hive .
# Push to private GHCR or Oracle Container Registry.
```

### Cloudflare front (TLS termination at CF, origin pull from OCI)

```
[User] ──HTTPS──> [Cloudflare proxied DNS] ──HTTPS──> [OCI A1 VM nginx :443] ──HTTP──> [LibreChat :3080]
                       │                                  │
                       └─ TLS terminated at CF             └─ Origin cert from CF Origin CA (free)
                          + DDoS, WAF, caching             + nginx: enforce strict origin SNI
```

**Encryption mode:** **Full (Strict)** — Cloudflare Origin CA cert on nginx, validates against CF origin CA chain.

**Steps (Phase 24, info here for completeness):**
1. Provision OCI A1 instance, install Docker, deploy compose stack.
2. Open ingress: 443 (TCP) from Cloudflare IPs only (use OCI security list with CF IP ranges).
3. Issue Cloudflare Origin CA cert for `<subdomain>.hive.bd` → nginx config.
4. CF DNS: A record `<subdomain>` → OCI public IP, **proxied (orange cloud)**.
5. CF SSL/TLS mode: Full (Strict).
6. CF page rules: cache static assets (`/assets/*`), bypass cache for `/api/*`.
7. CF WAF: rate limit, bot challenge.

Sources: [OCI Always Free](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm), [OCI A1 Flex](https://medium.com/@imvinojanv/setup-always-free-vps-with-4-ocpu-24gb-ram-and-200gb-storage-the-ultimate-oracle-cloud-guide-bed5cbf73d34), [Cloudflare Origin CA](https://developers.cloudflare.com/ssl/origin-configuration/origin-ca/), [Cloudflare encryption modes](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/).

Confidence: **HIGH** for shape + topology; **MEDIUM** for ARM64 sidecar image availability.

---

## 13. Streaming + WebSocket

LibreChat streams chat completions via **SSE** (`text/event-stream`) — same protocol Hive edge-api already supports (verified Phase 10 OpenAI compliance).

- **Path:** `POST /api/ask/{endpoint}` → server proxies to provider, streams chunks back as SSE.
- **No native websocket use for chat completions.**
- Other websocket use cases (live presence, collab) — not enabled by default.

**Cloudflare consideration:** SSE works through CF proxy (long-lived HTTP/1.1 connections). Set CF proxy buffering off for `/api/ask/*` if latency hiccups (page rule).

Confidence: **HIGH**.

---

## 14. File Upload Pipeline

LibreChat supports three upload modes:

| Mode | Formats | Tier (Hive plan) |
|------|---------|------------------|
| **Upload as Text** | `.txt`, `.md`, `.csv`, `.json`, `.xml`, `.html`, `.css`, code | guest+ (small files only) |
| **File Search (RAG)** | `.pdf`, `.docx`, `.txt`, `.md`, `.pptx` (default config) | verified+ (Phase 22) |
| **Upload to Provider** | images (vision), code (interpreter) | verified+ |

Default accepts: images, `.json`, `.pdf`, `.docx`, `.txt`, `.pptx`. Configurable via `librechat.yaml` `fileConfig`.

**Phase 22 RAG flow (Path A — sidecar):**
```
client ──multipart──> api ──forward──> rag_api ──parse(pdf/docx)──> chunk ──embed via Hive──> pgvector
                                              │
                                              └──store original ──> Supabase Storage hive-files bucket
                                                                    (override default local storage)
```

Override default storage = wire LibreChat `fileStrategy: "s3"` to Supabase Storage S3-compatible endpoint (already used by Hive edge-api).

Sources: [Upload as Text](https://www.librechat.ai/docs/features/upload_as_text), [File Config](https://www.librechat.ai/docs/configuration/librechat_yaml/object_structure/file_config), [RAG API](https://www.librechat.ai/docs/features/rag_api).

Confidence: **HIGH**.

---

## 15. Environment Variable Inventory (Hive-managed subset)

Phase 19 must populate these on OCI deploy:

| Var | Source | Notes |
|-----|--------|-------|
| `MONGO_URI` | system-managed | Atlas connection string |
| `MEILI_HOST` | system-managed | `http://meilisearch:7700` |
| `MEILI_MASTER_KEY` | system-managed | rotate per env |
| `JWT_SECRET` | system-managed | rotate; align w/ Phase 20 Supabase |
| `JWT_REFRESH_SECRET` | system-managed | rotate |
| `CREDS_KEY` | system-managed | 32-byte hex; rotate |
| `CREDS_IV` | system-managed | 16-byte hex |
| `HIVE_API_KEY` | system-managed | Hive system key for chat-app |
| `HIVE_BASE_URL` | system-managed | `http://hive-edge-api.internal:8080/v1` on OCI |
| `RAG_API_URL` | system-managed | `http://rag_api:8000` (compose-internal) |
| `RAG_OPENAI_API_KEY` | system-managed | same as `HIVE_API_KEY` (sidecar embeddings) |
| `RAG_OPENAI_BASE_URL` | system-managed | same as `HIVE_BASE_URL` |
| `EMBEDDINGS_PROVIDER` | system-managed | `openai` |
| `DOMAIN_CLIENT` | system-managed | e.g. `https://chat.hive.bd` |
| `DOMAIN_SERVER` | system-managed | same as DOMAIN_CLIENT |
| `ALLOW_REGISTRATION` | system-managed | `true` for Phase 19; gated by tier in Phase 21 |
| `ALLOW_EMAIL_LOGIN` | system-managed | `true` (Phase 19), false post-Supabase Phase 20 |
| `SEARCH` | system-managed | `true` |
| `MEILI_NO_ANALYTICS` | system-managed | `true` |
| `OPENAI_API_KEY` | system-managed | unset (deprecated for us) |
| `ANTHROPIC_API_KEY` | system-managed | unset (provider stripped) |
| (etc. — all non-Hive provider keys unset) | | |

User-managed vars: **none in Phase 19** (no per-user keys). Phase 20 adds Supabase keys (system-managed).

Source: [Environment Variables docs](https://www.librechat.ai/docs/configuration/dotenv).

Confidence: **HIGH**.

---

## 16. CSP / iframe / Cross-origin

LibreChat default CSP is permissive (no strict-CSP shipped). For Hive deployment:

- **Subdomain:** `chat.hive.bd` (chat-app) vs `app.hive.bd` (web-console) — same eTLD+1 = shared cookie domain `.hive.bd`.
- **Supabase auth cookie:** issued for `.hive.bd` → both subdomains read same session. Phase 20 uses this for SSO.
- **CORS:** LibreChat backend CORS allowlist set to `DOMAIN_CLIENT` only. Phase 20 adds web-console origin for cross-tab logout.
- **iframe:** none required (no embed scenarios in v1.1).
- **CSP hardening:** add `Content-Security-Policy` header in Phase 19 (defense-in-depth):
  ```
  default-src 'self';
  connect-src 'self' https://*.hive.bd https://*.supabase.co;
  img-src 'self' data: https:;
  script-src 'self' 'unsafe-inline';   # LibreChat Vite output uses inline scripts
  style-src 'self' 'unsafe-inline';
  ```

Confidence: **MEDIUM** — exact CSP refinement may need Phase 19 spike (LibreChat may break under stricter policy).

---

## 17. Health Endpoint

- **`GET /health`** — returns 200 since v0.7.6.
- Pre-existing route in `api/server/routes/health.js`.
- **No auth required** (must remain unauthenticated for OCI/CF healthcheck).
- Suitable for OCI Container Instance healthcheck + Cloudflare origin monitoring.

OCI healthcheck config:
```
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD curl -f http://localhost:3080/health || exit 1
```

Cloudflare Origin Health:
- Health Check → URL `https://chat.hive.bd/health` → expected 200.

Source: [LibreChat /health discussion #5961](https://github.com/danny-avila/LibreChat/discussions/5961).

Confidence: **HIGH**.

---

## Standard Stack (Hive chat-app)

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| LibreChat (forked) | v0.7.9 | chat-app base | locked per LICENSE-DECISION |
| MongoDB | Atlas M0 / 7.x | chat history + agents | LibreChat-required |
| Meilisearch | 1.7 | message search | LibreChat-required |
| pgvector (sidecar) | 0.8.0 on pg15 | RAG embeddings | LibreChat RAG default (Phase 22) |
| Hive edge-api | (existing) | OpenAI-compat provider | locked |

### Supporting
| Library | Version | Purpose | When |
|---------|---------|---------|------|
| `librechat-rag-api-dev-lite` | latest | Python RAG sidecar | Phase 22 |
| nginx | 1.27 | TLS terminator on OCI | Phase 24 |
| Docker Compose v2 | 2.x | local + OCI compose | Phase 19 |

### Versions to verify at fork
```bash
gh release view v0.7.9 --repo danny-avila/LibreChat
docker pull librechat/librechat:v0.7.9
docker manifest inspect librechat/librechat:v0.7.9 | grep architecture
```

---

## Architecture Patterns

### Recommended structure
```
apps/chat-app/                    # LibreChat fork at v0.7.9
├── api/                          # Express backend (upstream)
├── client/                       # Vite + React 18 SPA (upstream)
│   └── src/components/Nav/LanguagePicker/   # NEW: first-run modal
├── packages/
│   ├── data-provider/            # upstream
│   ├── data-schemas/             # upstream
│   ├── api/                      # upstream
│   └── client/                   # upstream
├── librechat.yaml                # NEW: Hive single-endpoint config
├── docker-compose.hive.yml       # NEW: chat-app service overlay
├── .env.hive.example             # NEW: Hive env template
└── HIVE-CHANGES.md               # NEW: log of Hive deltas vs upstream

.planning/v1.1-chatapp/
├── LIBRECHAT-VERSION.md          # NEW: pinned tag + commit SHA + rationale
└── LIBRECHAT-UPGRADE-PLAYBOOK.md # NEW: rebase procedure
```

### Pattern: minimal-fork (preferred)
- Don't delete provider code; gate via `librechat.yaml`.
- All Hive deltas live in `librechat.yaml` + `.env` + new `client/src/components/Nav/LanguagePicker/` + theme overrides.
- Upstream upgrade = `git fetch upstream && git merge v0.x.y` → resolve conflicts only in 4-5 modified files.

### Anti-patterns
- ❌ Delete upstream provider clients → upgrade hell on every release.
- ❌ Rewrite Mongo → Postgres → invasive, non-merge-friendly, Phase 19 timeline killer.
- ❌ Use deprecated `OPENAI_REVERSE_PROXY` → use `librechat.yaml` custom endpoint.
- ❌ Pin to `main` (floating) → ship instability.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Custom RAG chunking/retrieval | new Python service | `librechat-rag-api` sidecar | LibreChat-maintained; supported embedding providers swappable |
| Mongo → Postgres migration | invasive refactor | Keep Mongo (Atlas M0 free) | LibreChat is Mongo-native; refactor cost >> infra cost |
| New auth from scratch | rewrite Passport | Add Supabase Passport strategy (Phase 20) | Smaller diff, upgrade-friendly |
| Custom i18n framework | replace react-i18next | Reuse LibreChat's i18next + add `bn-BD` locale | already wired |
| Custom file storage | new S3 client | `fileStrategy: "s3"` → Supabase Storage | LibreChat supports S3-compat out of box |
| TLS termination on container | container-level cert | Cloudflare Origin CA + nginx on OCI | free, mature, DDoS protection |

---

## Common Pitfalls

### Pitfall 1: Floating-tag drift
**What goes wrong:** Pin = `main` or `latest`; CI rebuilds pull moving target; staging diverges from prod.
**Avoid:** Pin to immutable tag + commit SHA in `LIBRECHAT-VERSION.md`. Image tag in compose = `librechat/librechat:v0.7.9` (NOT `latest`).

### Pitfall 2: ARM64 image absence
**What goes wrong:** Pull fails on OCI A1 (ARM) because LibreChat or rag_api ships x86-only.
**Avoid:** `docker manifest inspect <image>` for each before deploy. Fallback build from source with `buildx --platform linux/arm64`.

### Pitfall 3: Mongo Atlas connection-pool exhaustion
**What goes wrong:** M0 = 500-conn ceiling; Node default pool 100/instance × multi-instance = exhaustion.
**Avoid:** Set `MONGO_MAX_POOL_SIZE=20` per instance. Single instance v1.1.

### Pitfall 4: SSE buffering on Cloudflare
**What goes wrong:** CF default buffers responses → SSE chunks land all-at-once → client sees frozen UI.
**Avoid:** CF page rule: `Cache Level: Bypass` + `Disable Performance` for `/api/ask/*`. Set response header `X-Accel-Buffering: no` in nginx.

### Pitfall 5: librechat.yaml mount path
**What goes wrong:** Container reads `/app/librechat.yaml`; bind-mount target wrong → falls back to default (all providers).
**Avoid:** Compose mount `./librechat.yaml:/app/librechat.yaml:ro` (not `:/librechat.yaml`).

### Pitfall 6: bn-BD locale non-BCP47-strict
**What goes wrong:** i18next picks `bn` over `bn-BD` because navigator returns plain `bn` → falls back to en.
**Avoid:** Register both `bn` and `bn-BD` in resources, set `fallbackLng: { 'bn-BD': ['bn', 'en'], 'bn': ['bn-BD', 'en'], default: ['en'] }`.

### Pitfall 7: FX/USD leak via LibreChat default UI
**What goes wrong:** LibreChat shows `$` cost estimates per message (built-in token-cost UI). Regulatory violation in BD.
**Avoid:** Disable cost UI in `librechat.yaml`: `interface: { showCost: false, showTokens: false }` — verify exact key names at fork. **Phase 17 audit mandate applies.**

### Pitfall 8: rag_api recommends 4GB / 1 OCPU vectordb
**What goes wrong:** Naive deploy on A1 with multiple services → vectordb OOM-killed.
**Avoid:** Set vectordb container `mem_limit: 4g`, `cpus: '1.0'`. Tune Postgres `shared_buffers=1GB`, `work_mem=64MB`.

---

## 10. Docker Compose Service Draft

```yaml
# deploy/docker/docker-compose.chatapp.yml — NEW
# Layered on top of existing docker-compose.yml via:
#   docker compose -f docker-compose.yml -f docker-compose.chatapp.yml --profile chatapp up
services:
  chat-app:
    image: librechat/librechat:v0.7.9
    # OR for forked: build from apps/chat-app/Dockerfile
    container_name: hive-chat-app
    profiles: ["chatapp"]
    restart: unless-stopped
    ports:
      - "3080:3080"
    depends_on:
      - mongodb
      - meilisearch
      - rag_api
      - edge-api          # existing Hive service
    env_file:
      - ../../.env
    environment:
      MONGO_URI: ${MONGO_URI:-mongodb://mongodb:27017/LibreChat}
      MEILI_HOST: http://meilisearch:7700
      MEILI_MASTER_KEY: ${MEILI_MASTER_KEY}
      JWT_SECRET: ${LIBRECHAT_JWT_SECRET}
      JWT_REFRESH_SECRET: ${LIBRECHAT_JWT_REFRESH_SECRET}
      CREDS_KEY: ${LIBRECHAT_CREDS_KEY}
      CREDS_IV: ${LIBRECHAT_CREDS_IV}
      HIVE_API_KEY: ${HIVE_API_KEY}
      HIVE_BASE_URL: http://edge-api:8080/v1
      RAG_API_URL: http://rag_api:8000
      DOMAIN_CLIENT: ${CHAT_APP_DOMAIN:-http://localhost:3080}
      DOMAIN_SERVER: ${CHAT_APP_DOMAIN:-http://localhost:3080}
      SEARCH: "true"
      MEILI_NO_ANALYTICS: "true"
      ALLOW_REGISTRATION: "true"          # Phase 21 gates by tier
      ALLOW_EMAIL_LOGIN: "true"           # Phase 20 disables (Supabase only)
    volumes:
      - ../../apps/chat-app/librechat.yaml:/app/librechat.yaml:ro
      - chat-app-uploads:/app/uploads
      - chat-app-images:/app/client/public/images
      - chat-app-logs:/app/api/logs

  mongodb:
    image: mongo:7
    container_name: hive-chat-mongo
    profiles: ["chatapp", "local"]
    restart: unless-stopped
    volumes:
      - chat-mongo-data:/data/db
    command: ["mongod", "--noauth"]   # local dev only; Atlas in staging/prod
    # NOT exposed publicly on OCI — bind to docker network only

  meilisearch:
    image: getmeili/meilisearch:v1.7
    container_name: hive-chat-meili
    profiles: ["chatapp"]
    restart: unless-stopped
    environment:
      MEILI_MASTER_KEY: ${MEILI_MASTER_KEY}
      MEILI_NO_ANALYTICS: "true"
    volumes:
      - chat-meili-data:/meili_data

  vectordb:
    image: pgvector/pgvector:0.8.0-pg15-trixie
    container_name: hive-chat-vectordb
    profiles: ["chatapp"]
    restart: unless-stopped
    environment:
      POSTGRES_DB: mydatabase
      POSTGRES_USER: myuser
      POSTGRES_PASSWORD: ${RAG_PG_PASSWORD}
    volumes:
      - chat-vectordb-data:/var/lib/postgresql/data
    deploy:
      resources:
        limits:
          memory: 4G
          cpus: '1.0'

  rag_api:
    image: registry.librechat.ai/danny-avila/librechat-rag-api-dev-lite:latest
    container_name: hive-chat-rag-api
    profiles: ["chatapp"]
    restart: unless-stopped
    environment:
      DB_HOST: vectordb
      POSTGRES_DB: mydatabase
      POSTGRES_USER: myuser
      POSTGRES_PASSWORD: ${RAG_PG_PASSWORD}
      EMBEDDINGS_PROVIDER: openai
      RAG_OPENAI_API_KEY: ${HIVE_API_KEY}
      RAG_OPENAI_BASEURL: http://edge-api:8080/v1
    depends_on:
      - vectordb
      - edge-api

volumes:
  chat-app-uploads:
  chat-app-images:
  chat-app-logs:
  chat-mongo-data:
  chat-meili-data:
  chat-vectordb-data:
```

**For Phase 19 (no RAG yet):** ship without `rag_api` + `vectordb` services; add in Phase 22.

---

## Code Examples

### Example: minimal `apps/chat-app/librechat.yaml` (Phase 19 baseline)

```yaml
version: 1.2.0
cache: true

interface:
  endpointsMenu: false        # hide endpoint switcher; lock to Hive
  modelSelect: true            # let user pick from Hive catalog
  parameters: false            # hide advanced params for v1.1
  sidePanel: true
  presets: false               # disable presets for v1.1
  showCost: false              # FX/USD regulatory — hide cost UI
  showTokens: false            # hide token counters

endpoints:
  custom:
    - name: "Hive"
      apiKey: "${HIVE_API_KEY}"
      baseURL: "${HIVE_BASE_URL}"
      models:
        default:
          - "gpt-4o-mini"
          - "claude-3-5-sonnet-20241022"
          - "gemini-2.5-flash"
        fetch: false
      titleConvo: true
      titleModel: "gpt-4o-mini"
      forcePrompt: false
      modelDisplayLabel: "Hive"
      headers:
        X-Hive-Source: "chat-app"
```

### Example: register `bn-BD` in `client/src/locales/i18n.ts` (after fork)

```ts
// PSEUDOCODE — verify exact LibreChat path at fork
import enUSTranslation from './en-US/translation.json';
import bnBDTranslation from './bn-BD/translation.json';

i18n.use(initReactI18next).init({
  resources: {
    'en-US': { translation: enUSTranslation },
    'bn-BD': { translation: bnBDTranslation },
  },
  fallbackLng: {
    'bn': ['bn-BD', 'en-US'],
    'bn-BD': ['en-US'],
    default: ['en-US'],
  },
  detection: {
    order: ['localStorage', 'cookie', 'navigator'],
    caches: ['localStorage', 'cookie'],
  },
});
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `OPENAI_REVERSE_PROXY` env var | `librechat.yaml` `endpoints.custom[].baseURL` | v0.7.x | locked path; deprecated var still works but warns |
| Per-user API key entry | `apiKey: "user_provided"` toggle | always | we use system key, not user-provided |
| Bundled local file storage | `fileStrategy: "s3"` to S3-compat | v0.7+ | enables Supabase Storage reuse |
| LibreChat single-Postgres | separate `vectordb` (pgvector) for RAG | v0.6.5+ | sidecar architecture mature |
| NextAuth (never used) | Passport.js + JWT | always | LibreChat is Express, not Next.js |

**Deprecated/outdated:**
- `OPENAI_REVERSE_PROXY` env: deprecated, removal planned. Use yaml.
- v0.6.x and earlier: missing MCP, smaller RAG support. Don't pin below v0.7.x.

---

## Validation Architecture

(`workflow.nyquist_validation` defaults to enabled; include section.)

### Test Framework
| Property | Value |
|----------|-------|
| Framework (LibreChat upstream) | Jest 29 + Playwright |
| Hive-side test framework | Playwright (existing in `apps/web-console/tests/e2e/`) + Go integration tests |
| Config file | `apps/chat-app/jest.config.js` (upstream) + new `apps/chat-app/tests/e2e/` for Hive E2E |
| Quick run command | `cd apps/chat-app && npm test -- --testPathPattern <name>` |
| Full suite command | `cd apps/chat-app && npm test && npx playwright test` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CHAT-19-01 | Fork at pinned tag matches `LIBRECHAT-VERSION.md` SHA | smoke | `git -C apps/chat-app rev-parse HEAD` vs version file | ❌ Wave 0 |
| CHAT-19-02 | Provider strip — Hive only endpoint visible | e2e | `npx playwright test tests/e2e/providers.spec.ts` | ❌ Wave 0 |
| CHAT-19-03 | Chat completion routes to Hive edge-api | integration | curl→stub edge-api→assert request seen | ❌ Wave 0 |
| CHAT-19-04 | `docker compose up chat-app` healthy in <60s | smoke | `docker compose ps + curl /health` | ❌ Wave 0 |
| CHAT-19-05 | First-run picker modal shows when localStorage empty | e2e | `npx playwright test tests/e2e/language-picker.spec.ts` | ❌ Wave 0 |
| CHAT-19-05 | Picker writes localStorage and applies bn-BD | e2e | same | ❌ Wave 0 |
| CHAT-19-06 | Upgrade playbook documented + executable on staging clone | manual | document review | ❌ Wave 0 |
| CHAT-19-07 | Mongo connection healthy on boot | smoke | container log scan for `Mongoose connected` | ❌ Wave 0 |
| CHAT-19-08 | Compose stack runs on ARM64 (multi-arch verified) | smoke | `docker manifest inspect` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `cd apps/chat-app && npm test -- --testPathPattern <area>` (~30s).
- **Per wave merge:** full Jest + Playwright suite (~5-10min).
- **Phase gate:** full suite + manual fork-from-clean dry-run on OCI staging instance before `/gsd:verify-work`.

### Wave 0 Gaps
- [ ] `apps/chat-app/tests/e2e/providers.spec.ts` — verify only Hive endpoint surfaces (CHAT-19-02).
- [ ] `apps/chat-app/tests/e2e/language-picker.spec.ts` — first-run modal + persistence (CHAT-19-05).
- [ ] `apps/chat-app/tests/integration/hive-routing.test.ts` — assert outbound request hits `${HIVE_BASE_URL}` (CHAT-19-03).
- [ ] `apps/chat-app/tests/smoke/health.test.ts` — container boot + `/health` check (CHAT-19-04, CHAT-19-07).
- [ ] `deploy/docker/docker-compose.chatapp.yml` — service overlay (covered in §10 above).
- [ ] `.planning/v1.1-chatapp/LIBRECHAT-VERSION.md` — pinned-tag artifact.
- [ ] `.planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md` — upgrade procedure.
- [ ] CI: matrix amd64+arm64 build of forked image.

---

## 11. Open Questions / Risks

1. **Exact pinned tag at fork day.** Recommend v0.7.9 today; revisit if v0.8.5 GA ships clean and Sakib wants Opus 4.7 + context summarization. Decided in `LIBRECHAT-VERSION.md` at fork time.
2. **`bn-BD` locale upstream presence.** LibreChat-AI/Locize-managed; cannot confirm without inspecting `client/src/locales/` at fork. If absent, scaffold from `en-US/translation.json` and partial-translate critical strings in Phase 19; full pass in Phase 23.
3. **ARM64 image for `librechat-rag-api-dev-lite`.** Verify `docker manifest inspect` at deploy. Fallback: build from source with `buildx`, push to GHCR.
4. **Atlas M0 512MB headroom.** Sufficient for soft launch; alert at 70% usage; migrate to M2 ($9/mo) when needed.
5. **MongoDB Atlas latency from OCI region.** Atlas regions: pick `ap-south-1` (Mumbai) for BD proximity. Cross-region from OCI Hyderabad → ~30-80ms acceptable for chat history (not in inference hot path).
6. **CSP hardening trade-off.** Strict CSP may break LibreChat Vite inline scripts. Spike during Phase 19 implementation; acceptable to ship permissive in v1.1, harden v1.2.
7. **LibreChat default cost-display UI** — confirm exact `interface.showCost` key name at fork; FX/USD audit (Phase 17) re-applies to chat-app surface.
8. **Upgrade-merge-conflict rate.** Smaller fork (config-only strip) → fewer conflicts. Upstream churn ~weekly; Hive upgrades quarterly = manageable.
9. **Phase 22 RAG path final decision** — sidecar (Path A) recommended; revisit in Phase 22 PLAN if Bengali recall demands Hive-native adapter (Path B).
10. **OCI A1 capacity availability.** Always-Free A1 capacity-constrained in popular regions; may need to retry instance creation. Plan: create early, keep instance allocated.
11. **MongoDB self-host on OCI A1 fallback.** If Atlas latency/cost prohibitive at scale, self-host on OCI: 4GB RAM dedicated to mongo, cron mongodump → Supabase Storage backup.
12. **`docker-compose.staging.yml` integration.** Hive existing staging compose is Workers-flavored (Upstash Redis); chat-app on OCI = separate compose context. Phase 24 finalizes; Phase 19 only adds local profile.

---

## Sources

### Primary (HIGH confidence)
- [LibreChat repo](https://github.com/danny-avila/LibreChat) — license, Dockerfile, monorepo layout
- [LibreChat docs (librechat.ai)](https://www.librechat.ai/docs) — config, env vars, RAG, MCP, auth
- [LibreChat changelog](https://www.librechat.ai/changelog) — release timeline
- [librechat.example.yaml](https://github.com/danny-avila/LibreChat/blob/main/librechat.example.yaml) — YAML reference
- [LibreChat docker-compose.yml](https://github.com/danny-avila/LibreChat/blob/main/docker-compose.yml) — compose reference
- [danny-avila/rag_api](https://github.com/danny-avila/rag_api) — RAG sidecar source
- [Custom Endpoints quick start](https://www.librechat.ai/docs/quick_start/custom_endpoints)
- [MongoDB Atlas free cluster limits](https://www.mongodb.com/docs/atlas/reference/free-shared-limitations/)
- [OCI Always Free Resources](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm)
- [Cloudflare Origin CA](https://developers.cloudflare.com/ssl/origin-configuration/origin-ca/)
- [LibreChat /health discussion](https://github.com/danny-avila/LibreChat/discussions/5961)
- [LibreChat Project Structure (DeepWiki)](https://deepwiki.com/danny-avila/LibreChat/8.1-project-structure)

### Secondary (MEDIUM confidence)
- [LibreChat MCP feature](https://www.librechat.ai/docs/features/mcp)
- [LibreChat Authentication](https://www.librechat.ai/docs/configuration/authentication)
- [LibreChat translations](https://www.librechat.ai/docs/translation)
- [v0.8.5 changelog](https://www.librechat.ai/changelog/v0.8.5)
- [Optimizing RAG in LibreChat](https://www.librechat.ai/blog/2025-04-25_optimizing-rag-performance-in-librechat)
- [i18next-browser-languageDetector](https://github.com/i18next/i18next-browser-languageDetector)

### Tertiary (LOW confidence — flagged for verification at fork time)
- Exact `bn-BD` locale presence in `client/src/locales/` (Locize-managed; verify on fork)
- Exact `interface.showCost` YAML key name (verify in `librechat.example.yaml` at v0.7.9)
- ARM64 manifest for `librechat-rag-api-dev-lite` (verify with `docker manifest inspect` at deploy)

---

## Metadata

**Confidence breakdown:**
- License audit: **HIGH** — multiple sources confirm plain MIT.
- Pinned tag: **MEDIUM** — recommendation grounded; final decision at fork day.
- Provider strip surface: **HIGH** for config-strip; **MEDIUM** for "all hidden when keys absent" (verify in spike).
- Base-URL config: **HIGH** — yaml schema documented.
- MongoDB integration: **HIGH** for env vars; **HIGH** for Atlas vs OCI recommendation.
- Bengali locale: **MEDIUM** — upstream presence not confirmed; scaffold path documented.
- Language picker: **HIGH** for pattern; **MEDIUM** for exact LibreChat Modal component reuse.
- Auth surface: **HIGH** for current state.
- RAG path (sidecar vs custom): **HIGH** for both viable; **MEDIUM** for "Path A faster" prediction.
- OCI deployment: **HIGH** for shape; **MEDIUM** for ARM64 sidecar image availability.
- Streaming/CSP/file/health: **HIGH**.

**Research date:** 2026-04-26
**Valid until:** 2026-05-26 (LibreChat releases monthly; tag landscape may shift)
**Re-research trigger:** v0.8.5 GA ships before fork day, OR Atlas free-tier policy changes, OR OCI Always-Free A1 deprecated.
