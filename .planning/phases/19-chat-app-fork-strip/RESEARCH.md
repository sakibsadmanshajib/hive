> ⚠️ **DEPRECATED — DO NOT FOLLOW THIS DOCUMENT FOR EXECUTION.**
>
> Phase 19 was pivoted from Lobe Chat to **LibreChat (MIT)** on 2026-04-25 per
> [`../../v1.1-chatapp/LICENSE-DECISION.md`](../../v1.1-chatapp/LICENSE-DECISION.md).
> The active research artifact is **[RESEARCH-LIBRECHAT.md](RESEARCH-LIBRECHAT.md)**;
> the executable plan is **[PLAN.md](PLAN.md)**.
>
> This file is retained as the historical record of the rejected Lobe Chat alternative
> (license blocker + Cloudflare Workers incompatibility). Executor agents MUST NOT
> read this document for implementation guidance — all references to Lobe Chat,
> LobeHub Community License, Apache-2.0, Workers/Fly fallback, Drizzle/Postgres-only
> schema, and `lobehub/lobe-chat` upstream are obsolete.

# Phase 19: Chat-App — Fork & Strip Lobe Chat (Pinned Version) + Language Picker — Research [DEPRECATED]

**Researched:** 2026-04-25
**Status:** Superseded 2026-04-25 — see `RESEARCH-LIBRECHAT.md`.
**Domain:** Next.js 15 fork integration, multi-provider chat UI, Drizzle/Postgres schema migration, OpenNext+CF Workers compatibility, i18n insertion, license compliance
**Confidence:** HIGH on tag selection / provider strip surface / schema layout / Workers blockers; MEDIUM on exact base-URL config behavior; **HIGH** on license-derivative-work risk.

---

## Summary

Lobe Chat's GitHub repo `lobehub/lobe-chat` was **renamed to `lobehub/lobehub`** and at v2.0.0 (2026-01-27) pivoted to a fundamentally different "Agent teammates" multi-package platform (50+ workspace packages, builtin-tools, agent-runtime, conversation-flow). The product the user actually wants ("Lobe Chat") corresponds to the **v1.x line, last released as v1.143.3 on 2026-01-25**. Pinning to v2.x would fork a different application entirely.

Two non-trivial surprises emerged that materially affect the plan:

1. **License is NOT plain Apache-2.0.** It is the *LobeHub Community License* — Apache-2.0 + commercial-derivative restrictions. Distributing a Hive-rebranded fork is a **derivative work** and §1.b explicitly requires a paid commercial license from LobeHub LLC. Phase 19 cannot ship a public derivative product (`apps/chat-app` deployed under hive.bd) without obtaining that license OR keeping the fork strictly internal/unmodified.

2. **Cloudflare Workers deployment is officially unsupported by upstream Lobe.** GitHub issue #4241 closed by maintainer `arvinxx`: *"Currently we don't support cloudflare deployment due to its runtime issue."* Stock dependency set includes `sharp` (native libvips), `pg` (Node net socket), `ws` (websocket server), `pdf-parse`, `pdfjs-dist`, `mammoth` — all incompatible or marginal with Workers. Phase 24's CF-Workers plan inherits this risk; Lobe-on-Workers is **NEEDS PATCHES at minimum, more realistically BLOCKED** without significant refactor. Web-console pattern (`@opennextjs/cloudflare`, `nodejs_compat`) does not extrapolate.

**Primary recommendation:** Pin **`v1.143.3`** (last v1.x stable). Treat license as the gating item — open a commercial license conversation with LobeHub LLC **before** writing fork code. Treat Workers deployment as Plan B; default to Docker (control-plane, edge-api stack-aligned) on Fly.io / VPS for chat-app, deferring Workers to a separate compatibility spike inside Phase 24.

---

## User Constraints (from V1.1-MASTER-PLAN.md v2 revisions)

### Locked Decisions
- **Pinned tag** at fork time, not "latest stable" rolling.
- **First-run language picker** (bn-BD / en-US) at setup screen, persisted to user prefs.
- **No auto-trial credits.** Owner-discretionary credit grants only (Phase 14).
- **Verified-tier-only** file upload.
- **Zero USD/FX leak** to BD customers — chat-app top-up + wallet must be BDT-only.
- **Single OpenAI-compatible provider** wired to `http://edge-api:8080`. All other providers stripped.
- **Schema in Supabase Postgres** under `chat_app` namespace.
- **Docker compose service** in `deploy/docker/docker-compose.yml`.

### Claude's Discretion
- Pinned tag selection (recommend specific tag with rationale).
- Strip implementation strategy (delete files vs. config flag).
- Language-picker insertion point.
- Schema namespace strategy (Postgres `SCHEMA chat_app` vs. table prefix).

### Deferred Ideas (out of scope here)
- MCP marketplace, projects/RAG collections, web-search plugins, voice/STT/TTS, image gen, multi-tenant, ads, regions beyond BD, auto-trial credits.

---

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| CHATAPP-19-01 | Fork Lobe Chat at pinned upstream tag into `apps/chat-app/` | §Pinned Tag — recommend `v1.143.3` |
| CHATAPP-19-02 | Strip non-Hive providers, lock to single OpenAI-compat provider → edge-api | §Provider-Strip Plan |
| CHATAPP-19-03 | Wire OpenAI provider base URL to `http://edge-api:8080` | §Base-URL Config |
| CHATAPP-19-04 | Migrate Lobe schema into Supabase Postgres `chat_app` namespace | §Schema Migration Plan |
| CHATAPP-19-05 | Add Docker Compose service (reuse `--profile local` pattern) | §Docker Compose Service Draft |
| CHATAPP-19-06 | First-run language picker modal (bn-BD / en-US), persists to user prefs | §Language Picker Insertion |
| CHATAPP-19-07 | Document upgrade procedure | §LOBE-UPGRADE-PLAYBOOK template (Open Question) |
| CHATAPP-19-08 | CI lint/typecheck/build green | §Build Constraints |

---

## Pinned Tag Recommendation

**Recommended tag:** **`v1.143.3`** (commit `d718de7e1af2dd7281c4eaca70f627e4b9deddb1`, published 2026-01-25)

### Rationale

| Tag | Date | Verdict |
|-----|------|---------|
| `v2.1.52` | 2026-04-20 | **REJECT.** v2.0.0 (2026-01-27) is a different product — `lobehub/lobehub` "agent teammates" platform with 50+ workspace packages (`agent-runtime`, `builtin-tool-*`, `conversation-flow`). Forking this means owning a multi-agent orchestration stack, not a chat UI. Out of v1.1 scope. |
| `v1.143.3` | 2026-01-25 | **ACCEPT.** Last v1.x release. The Lobe Chat product Sakib initially scoped — single Next.js app, 68 model-providers, NextAuth+Clerk auth, Drizzle+Postgres schema. 5283 files, manageable strip surface. |
| `v1.143.0` / `v1.142.x` | Oct–Dec 2025 | Acceptable fallback if v1.143.3 reveals a regression during fork-bootstrap, but no known issues warrant downgrading. |

### Tag pinning mechanics
- Lobe lightweight tag (annotated tag was used pre-rename, lightweight after). Pin via:
  - `git clone --depth 1 --branch v1.143.3 https://github.com/lobehub/lobehub.git apps/chat-app`
  - **Repo rename note:** `lobehub/lobe-chat` URLs redirect to `lobehub/lobehub`. Pin against the new canonical URL to avoid future broken redirects.
- Record commit SHA in `.planning/v1.1-chatapp/LOBE-VERSION.md` (per master plan task 6).

### Source verification
- **HIGH** — `gh release list --repo lobehub/lobehub --limit 100`. v2.0.0 named *"LobeHub: Agent teammates that grow with you"* — confirmed product pivot.
- **HIGH** — Tree comparison: v1.143.3 has `src/config/modelProviders/{anthropic,openai,...}.ts` (68 files); v2.1.52 has `packages/builtin-tool-{calculator,gtd,knowledge-base,...}` (40+ tool packages, no centralized providers dir).

---

## License Audit (CRITICAL — read before planning fork)

### License: LobeHub Community License (Apache-2.0 + commercial conditions)

Verbatim from `https://github.com/lobehub/lobehub/blob/v1.143.3/LICENSE`:

> From 1.0, LobeChat is licensed under the LobeHub Community License, based on Apache License 2.0 with the following additional conditions:
> 1. The commercial usage of LobeChat:
>    a. LobeChat may be utilized commercially, including as a frontend and backend service without modifying the source code.
>    b. **a commercial license must be obtained from the producer if you want to develop and distribute a derivative work based on LobeChat.**
> Please contact hello@lobehub.com by email to inquire about licensing matters.

### Risk assessment

| Activity | License clause | Verdict |
|----------|----------------|---------|
| Run unmodified Lobe Chat self-hosted internally | §1.a permits | OK |
| Strip providers, add bn-BD locale, add language picker, change branding, ship as `chat.hive.bd` | §1.b → derivative work for distribution | **REQUIRES COMMERCIAL LICENSE** |
| Fork privately for evaluation | Apache-2.0 default | OK (no distribution) |

### Recommended action (planner must surface this)
1. **Email `hello@lobehub.com`** before any fork commit lands. Get a written quote for commercial derivative license. Assume cost is non-trivial (LobeHub's revenue model).
2. **Alternative: stay below the derivative-work line.** Run upstream container `lobehub/lobe-chat` unmodified, pass all customization via env vars (`OPENAI_PROXY_URL`, `OPENAI_MODEL_LIST`, `CUSTOM_MODELS`, `KEY_VAULTS_SECRET`, theming via existing settings). This severely constrains the language-picker + strip-providers goals — most of those need source edits.
3. **Alternative: adopt fully Apache-2.0/MIT chat-UI.** Candidates: LibreChat (MIT), Chatbot UI (MIT), Open WebUI (BSD-3-clause variant — verify), big-AGI (MIT). Eliminates licensing block entirely. Master plan Track B would need re-grounding.

**This decision must be made before Phase 19 PLAN.md is written.** Recording in `.planning/v1.1-chatapp/LICENSE-DECISION.md` recommended.

### Attribution requirements (if commercial license obtained)
- Apache-2.0 §4: preserve `LICENSE`, `NOTICE`, copyright notices in distributed source.
- Add `LICENSE-LOBEHUB` and Hive-side `NOTICE` referencing upstream.
- Web UI footer should retain "Powered by LobeChat" or whatever LobeHub LLC stipulates in commercial agreement.

**Confidence:** HIGH — license text fetched from GitHub raw at pinned tag.

---

## Provider-Strip Plan

### Surface (verified at v1.143.3)

| Path | Files | Action |
|------|-------|--------|
| `src/config/modelProviders/*.ts` | **68 provider files** (ai21, ai302, anthropic, azure, baichuan, bedrock, cerebras, cloudflare, cohere, cometapi, deepseek, fireworksai, github, google, groq, higress, huggingface, hunyuan, infiniai, internlm, jina, mistral, moonshot, novita, ollama, openai, openrouter, perplexity, qwen, stepfun, taichu, togetherai, upstage, vllm, volcengine, wenxin, xai, zeroone, zhipu, ...) | **Keep:** `openai.ts` (rename internally to `hive` if needed) and `index.ts`. **Delete:** all other 66 files. Update `index.ts` exports. |
| `src/libs/agent-runtime/*` | per-provider runtime adapters | Strip non-OpenAI adapters. Keep OpenAI adapter as the single hot path. |
| `src/config/aiModels/*` (if present) | model catalogs per provider | Keep openai catalog; replace with Hive-supplied catalog populated from Hive control-plane `/v1/models`. |
| `src/store/aiInfra/slices/*` | provider state slices | Trim to single-provider; preserve type shape (Lobe assumes multi-provider Map). |
| `locales/*/modelProvider.json`, `locales/*/providers.json` | provider strings | Strip stripped-provider keys to keep i18n clean. |
| Settings UI: `src/features/AgentSetting/*`, `src/app/[variants]/(main)/settings/llm/*` | per-provider config tabs | Hide non-Hive provider tabs. Lock model-list to Hive catalog. |

### Strip strategy
- **Hard delete** stripped provider files (vs. feature-flag) — reduces strip surface to single `git rm` block, simplifies upgrade-merge resolution.
- Rename internal provider id `openai` → `hive` only AFTER first green build. Wider rename complicates upstream-merge.
- Leave types/shape intact: many Lobe components iterate `providerList`. A list with one entry is safer than refactoring the abstraction.

### Effort estimate
- **Provider deletion:** ~70 files, ~2-3 hours including i18n cleanup.
- **Settings UI hide:** ~1 day. Many references — likely several iteration rounds to drive UI to clean state.
- **Build green after strip:** ~1 day (typecheck cascade — `providerList` consumers may explode if cardinality assumptions exist).

### Risks
- Lobe's "model fetcher" and "model auto-discovery" feature calls each enabled provider's `/models` endpoint. With single provider it just calls Hive — verify Hive `/v1/models` returns OpenAI-compat shape.
- "Disable Provider" UI in settings may be partially gated by enabled-provider count; test with cardinality 1.

**Confidence:** HIGH on file paths (verified via gh tree at SHA `d718de7e...`); MEDIUM on UI propagation surface — real depth shows up only at typecheck.

---

## Base-URL Config

### Verified env vars (from `.env.example` at v1.143.3)

| Env var | Purpose | Hive value |
|---------|---------|-----------|
| `OPENAI_API_KEY` | Required even if proxy used (Lobe sends `Authorization: Bearer <key>`) | Hive API key issued by control-plane |
| **`OPENAI_PROXY_URL`** | **Override OpenAI base URL** | `http://edge-api:8080/v1` (in Docker), `https://api.hive.bd/v1` (production) |
| `OPENAI_MODEL_LIST` | Whitelist + ordering (`gpt-3.5-turbo,gpt-4o-mini,...`) | Hive-supported model IDs (sourced from Hive `/v1/models`) |
| `CUSTOM_MODELS` | Same role as MODEL_LIST in newer code paths | Mirror of Hive catalog |
| `KEY_VAULTS_SECRET` | Per-user API-key encryption | Generate strong random; store in CF/Docker secrets |
| `DATABASE_URL` | Postgres URL | Supabase pooler URL (transaction mode for serverless, session mode for long-lived) |
| `DATABASE_DRIVER` | `node`/`neon`/`pglite` | `node` (postgres.js or pg) |
| `S3_*` | File storage | Reuse Hive `hive-files` Supabase Storage bucket vars |
| `NEXT_AUTH_SECRET`, `NEXTAUTH_URL` | NextAuth | Phase 20 replaces with Supabase. Keep stub for v19. |

### Mechanics
- Lobe's OpenAI adapter is `src/libs/agent-runtime/openai/index.ts` — instantiates the OpenAI SDK with `baseURL: process.env.OPENAI_PROXY_URL`. Verified pattern via Lobe self-hosting docs at `lobehub.com/docs/self-hosting/environment-variables/model-provider`.
- Per-user override: Lobe's settings UI lets each user set their own provider proxy URL. **Lock this UI down** for Hive — base URL must be system-managed, not user-changeable.

**Confidence:** HIGH on env-var names (raw `.env.example` fetched). MEDIUM on per-user-override lockdown surface (need source confirmation in plan).

---

## Schema Migration Plan (Drizzle → Supabase `chat_app` schema)

### Upstream schema layout (verified at v1.143.3)

`packages/database/src/schemas/` — **21 schema files**:
```
agent.ts        — agent definitions
aiInfra.ts      — provider/model registry per user
apiKey.ts       — Lobe-internal API keys (orthogonal to Hive keys)
asyncTask.ts    — async task tracking
chatGroup.ts    — group chat
document.ts     — knowledge base documents
file.ts         — file uploads
generation.ts   — image gen jobs
message.ts      — chat messages
nextauth.ts     — NextAuth tables (drop in Phase 20)
oidc.ts         — OIDC sessions
rag.ts          — RAG chunks + embeddings
ragEvals.ts     — RAG eval runs
rbac.ts         — Lobe roles (drop or merge with Hive RBAC Phase 18)
relations.ts    — Drizzle relations declarations
session.ts      — chat sessions
topic.ts        — chat topics
user.ts         — user profile
userMemories.ts — long-term memory store
```

`packages/database/migrations/` — **41 SQL files** (`0000_init.sql` through `0040_*.sql`) plus `meta/000N_snapshot.json` Drizzle metadata. `0005_pgvector.sql` enables pgvector (already on in Hive Supabase — confirmed by master plan).

### Recommended namespace approach: dedicated Postgres schema

```sql
CREATE SCHEMA IF NOT EXISTS chat_app;
-- All Lobe migrations apply with SET search_path = chat_app;
```

**Why dedicated schema (not table prefix):**
- Cleanest separation from Hive's existing 21 migrations in `public`.
- Drizzle 0.44.6 supports `pgSchema('chat_app')` (`drizzle-orm/pg-core`) — wrap Lobe's schema definitions in a single schema object.
- Future Lobe upgrades replay upstream migrations against `chat_app` without colliding with Hive ones.
- Avoids name collisions on common identifiers (`users`, `files`, `messages`).

### Drizzle config changes
1. Edit `drizzle.config.ts` (lobe root) → `schemaFilter: ['chat_app']`.
2. In `packages/database/src/schemas/index.ts` wrap exports with `pgSchema('chat_app')`.
3. Regenerate migrations once with `pnpm drizzle-kit generate` — produces a single Hive-side `0000_chatapp_init.sql` baseline. Discard upstream's 41 migrations from the `chat_app` migration trail; let Hive's Supabase migration pipeline (`supabase/migrations/`) own a single `chat_app` baseline.
4. Mirror this baseline as `supabase/migrations/20260501_01_chat_app_baseline.sql` (placeholder date — adjust at execution).

### Postgres extensions required
- `pgvector` — already enabled (master plan §"v2 revisions").
- `pg_trgm` — Lobe uses for full-text-ish search; verify enabled.
- `vector` index types: HNSW for embedding queries (Phase 22 will tune).

### Auth-table handling (transition to Phase 20)
- v19: keep `chat_app.next_auth_users`, `chat_app.next_auth_accounts`, `chat_app.next_auth_sessions` for boot.
- v20: introduce mapping table `chat_app.user_supabase_link (lobe_user_id uuid, supabase_user_id uuid)` and migrate.

### Risks
- Lobe assumes `public` schema in some raw SQL probes (e.g. health checks). Audit `packages/database/src/server/postgres/index.ts` for hardcoded schema refs.
- pgvector index dimensions: Lobe defaults to 1536 (text-embedding-3-small). Hive's chosen embedding model (Phase 22) may differ — coordinate.
- Supabase row-level-security (RLS): Lobe schema is NOT RLS-aware. Either grant service-role-only access (chat-app calls Postgres via service role) or write RLS policies in v20 alongside Supabase auth.

**Confidence:** HIGH on schema file inventory (gh tree); MEDIUM on Drizzle pgSchema mechanics (verify in plan against Drizzle 0.44.6 docs).

---

## OpenNext + Cloudflare Workers Compatibility Report

### **Verdict: BLOCKED for Phase 19 boot target. NEEDS-PATCHES at best for Phase 24.**

### Evidence

#### 1. Upstream maintainer position (definitive)
[github.com/lobehub/lobe-chat/issues/4241](https://github.com/lobehub/lobe-chat/issues/4241) — closed by maintainer `arvinxx`:

> *"Currently we don't support cloudflare deployment due to its runtime issue"*

#### 2. Stock dependency set (audited from `package.json` at v1.143.3)

| Dep | Version | Workers compat |
|-----|---------|----------------|
| `next` | `~15.3.6` | OK with `@opennextjs/cloudflare` + `nodejs_compat` |
| `react` | `^19.2.1` | OK |
| `sharp` | `^0.34.4` | **BLOCKED** — native libvips. Used for image optimization + thumbnail. Workers has no native binary path. |
| `pg` | `^8.16.3` | **BLOCKED** — uses TCP sockets. Workers needs Cloudflare Hyperdrive or `postgres.js` over TLS-via-fetch. |
| `ws` | `^8.18.3` | **BLOCKED** — websocket *server*. Workers offers WebSocketPair, not `ws` lib API. Need to confirm Lobe runs ws as server (vs. client only). |
| `pdf-parse` | `^1.1.1` | **BLOCKED** — Node fs + Buffer-heavy. |
| `pdfjs-dist` | `4.8.69` | NEEDS-PATCHES — works in Workers if loaded as ESM with WASM, but Lobe's import path likely Node-targeted. |
| `mammoth` | `^1.11.0` | **BLOCKED** — Node-only DOCX parser. |
| `@aws-sdk/client-s3` | `~3.893.0` | OK on Workers since v3.300+ |
| `@electric-sql/pglite` | `0.2.17` | OK (WASM SQLite) — alt DB driver if pg blocked |
| `drizzle-orm` | `^0.44.6` | OK with `postgres.js` driver or pglite |

#### 3. Workers-specific limits
- **Bundle size:** Workers Free 1MB / Paid 10MB (after gzip). Lobe stock build ~30MB+ uncompressed — Cloudflare Workers Standard plan 10MB ceiling tight; may exceed.
- **CPU time per request:** 30s default (raise via env). Lobe RAG indexing exceeds.
- **Memory:** 128MB. Sharp + pdfjs-dist + embedding generation will OOM.
- **`build` heap:** Lobe sets `--max-old-space-size=6144` (`scripts.build` field). Build host needs 6+GB RAM — applies regardless of deploy target, but confirms "this is a heavy app".

### What works on Workers
- Pure SSR pages.
- API routes that proxy to external services (Hive edge-api).
- Supabase fetch-based clients.
- Streaming via SSE (Workers supports streaming `Response`).

### What does NOT work without refactor
- File upload pipeline (sharp resize, PDF/DOCX parse) → must run on Node sidecar.
- Direct `pg` Postgres → must move to Hyperdrive or Supabase REST/REST-via-fetch.
- WebSocket server endpoints — confirm if Lobe uses any (some experimental features do).

### Recommendation for v1.1
1. **Phase 19 deploy target = Docker** (`docker compose --profile local up chat-app`). Aligns with control-plane + edge-api + redis stack. Production deploy = Fly.io or container-based VPS.
2. **Phase 24 spike outcome (predict):** "Lobe-on-Workers requires forking out file-processing into a Node sidecar (Workers cannot do sharp/pdf-parse). Either accept hybrid Workers + Node sidecar, or deploy chat-app entirely on Fly.io/Render and keep web-console on Workers."
3. **Master plan note (§v2 revisions row "OpenNext + CF Workers fit") needs revision.** "Confirmed feasible for Next.js 14/15" is true generically but **NOT for Lobe Chat specifically** due to native deps. Master plan said *"Spike still validates Lobe-specific deps"* — research now provides that data ahead of the spike.

**Confidence:** HIGH on dep-driven block (deps audited from package.json raw); HIGH on maintainer position (issue text fetched); MEDIUM on bundle-size estimate (no actual build run yet).

---

## Language Picker Insertion Plan

### Reality check
**`bn-BD` is NOT in upstream Lobe locale set.** Upstream `locales/` at v1.143.3:

`ar, bg-BG, de-DE, en-US, es-ES, fa-IR, fr-FR, it-IT, ja-JP, ko-KR, nl-NL, pl-PL, pt-BR, ru-RU, tr-TR, vi-VN, zh-CN, zh-TW`

— **no `bn-BD`**. Phase 19 must add the locale dir before the picker has anything to point at.

### Tasks
1. **Add `bn-BD` locale skeleton.** Copy `locales/en-US/*.json` → `locales/bn-BD/*.json`. ~30 files. Initially leave English strings — Phase 23 owns translation.
2. **Register locale.** Lobe i18n config likely at `src/locales/resources.ts` or `src/const/locale.ts`. Add `bn-BD` to `DEFAULT_LANG_LIST` / `locales` array.
3. **Insertion point for picker modal:**
   - **Best candidate:** `src/app/[variants]/(main)/layout.tsx` or `(main)/welcome/*` — wrap with a `FirstRunGate` client component.
   - Gate: read `localStorage.getItem('hive-locale-picked')`. If null, render modal that captures `bn-BD` | `en-US` and:
     - Calls Lobe i18n `i18n.changeLanguage(...)`.
     - Sets cookie `NEXT_LOCALE=<chosen>` (Lobe respects this for SSR variant routing — verify).
     - Sets `localStorage.setItem('hive-locale-picked', 'true')`.
     - Phase 20: also POST to user-prefs endpoint after auth.
4. **i18n key for the modal itself:** add `welcome.languagePicker.{title,bengali,english,confirm}` strings to `bn-BD/welcome.json` and `en-US/welcome.json` (Lobe convention: `locales/{lang}/{namespace}.json`).
5. **Variant-routing impact:** Lobe routes are `/[variants]/...`. The variant segment encodes locale + theme. Setting cookie + i18n is the supported pattern; confirm route-resolution side effects in plan.

### Code snippet shape (for plan reference)
```typescript
// src/components/FirstRunGate.tsx (new file)
'use client';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal, Radio } from 'antd';

const STORAGE_KEY = 'hive-locale-picked';

export function FirstRunGate({ children }: { children: React.ReactNode }) {
  const { i18n } = useTranslation('welcome');
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    if (!localStorage.getItem(STORAGE_KEY)) setOpen(true);
  }, []);

  const handleSelect = async (locale: 'bn-BD' | 'en-US') => {
    document.cookie = `NEXT_LOCALE=${locale};path=/;max-age=31536000`;
    await i18n.changeLanguage(locale);
    localStorage.setItem(STORAGE_KEY, 'true');
    setOpen(false);
  };
  // ... render modal
  return <>{children}{/* modal */}</>;
}
```

**Confidence:** HIGH on locale absence; MEDIUM on exact insertion file (Lobe app router structure has variants — pin in plan via PR review).

---

## Auth Current-State Report (input to Phase 20)

### Lobe v1.143.3 supports both Clerk AND NextAuth, runtime-toggled

Verified files:
- **NextAuth:** `src/app/(backend)/api/auth/[...nextauth]/route.ts`, `src/libs/next-auth/` (8+ files).
- **Clerk:** `src/app/[variants]/(auth)/login/[[...login]]/page.tsx`, env vars `NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY` + `CLERK_SECRET_KEY`.
- **Self-hosted default:** NextAuth (Clerk requires SaaS keys).
- **OAuth flows:** `src/app/[variants]/oauth/{callback,consent}/*` — Lobe runs its own OAuth provider for plugins.

### NextAuth providers enumerated by Lobe
GitHub, Google, Auth0, Authentik, Casdoor, Cloudflare Zero Trust, Logto, MicroSoft Entra ID, Okta, Zitadel — declared in `src/libs/next-auth/sso-providers/*`.

### Replacement effort for Phase 20 (Supabase SSO)
1. **Delete or stub** `[...nextauth]/route.ts` — replace with Supabase auth-helpers route handler.
2. **Adapter swap:** Lobe uses `@auth/drizzle-adapter`. Supabase uses GoTrue + JWTs. Need a thin shim that maps Supabase user → Lobe `users` row. Easiest: keep `users` table, write Supabase-trigger that on user create inserts a `chat_app.users` row with `id = supabase auth.users.id`.
3. **Tier resolution middleware** (master plan Phase 20 §"Tier resolution"): runs on every Lobe API route — wraps Supabase JWT validation, computes tier from `email_verified + phone_verified + credit_grants`, attaches to request.
4. **Guest mode:** Lobe currently REQUIRES auth on most chat endpoints. v20 needs to relax: anonymous Supabase session OR cookie-based pseudo-user with tier=guest.
5. **Logout cascade:** `apps/web-console/app/auth/sign-out/route.ts` already exists (commit `3435f1b`). Mirror in chat-app to clear Lobe session + Supabase session in same handler.

**Phase 20 effort estimate:** **5–8 days**. Highest-risk item in Track B because Lobe couples user identity tightly to NextAuth across many components.

**Confidence:** HIGH on file paths and provider list; MEDIUM on cascade impact (cross-cuts many components).

---

## Standard Stack (versions to plan against)

| Component | Version | Source |
|-----------|---------|--------|
| Lobe Chat | `v1.143.3` | gh release list |
| Next.js | `~15.3.6` | package.json |
| React | `^19.2.1` | package.json |
| drizzle-orm | `^0.44.6` | package.json |
| pnpm | `10.18.3` | `packageManager` field |
| Node | unspecified `engines.node`; assume 20 LTS minimum | inferred from Next.js 15 baseline |
| postgres driver | `pg ^8.16.3` (or swap to `postgres.js` for serverless) | package.json |
| pgvector | `0005_pgvector.sql` upstream migration | DB migration tree |
| i18next | bundled in Lobe | locale tree |
| OpenAI SDK | bundled (provider runtime) | inferred |
| antd | UI framework Lobe uses | inferred from settings UI |

### Hive-side additions
- **No new top-level deps** for v19 strip work — keep delta minimal.
- v22 (RAG) + v20 (auth) introduce Supabase auth-helpers and embedding client — out of v19 scope.

---

## Architecture Patterns

### Recommended layout
```
apps/chat-app/                 ← forked from lobehub/lobehub@v1.143.3
├── src/
│   ├── config/modelProviders/ ← stripped to openai.ts + index.ts
│   ├── libs/agent-runtime/    ← stripped to openai/
│   └── components/FirstRunGate.tsx  ← NEW (language picker)
├── locales/
│   ├── bn-BD/                 ← NEW skeleton (copy of en-US)
│   └── en-US/                 ← upstream
├── packages/database/
│   ├── src/schemas/           ← wrapped in pgSchema('chat_app')
│   └── migrations/            ← regenerated single baseline
├── .env.example               ← Hive-tuned env vars
├── Dockerfile                 ← in apps/chat-app/ OR deploy/docker/
└── LICENSE-LOBEHUB            ← upstream license preserved + Hive NOTICE
```

### Anti-patterns to avoid
- **DO NOT** flatten Lobe's `[variants]` route group — variants encode locale + theme + DB-mode. Removing breaks SSR locale switching.
- **DO NOT** dual-source provider lists (env + code). Pick env-driven (`OPENAI_MODEL_LIST`) to keep Hive catalog as single source.
- **DO NOT** edit upstream migrations in place — write Hive baseline, freeze upstream.

---

## Don't Hand-Roll

| Problem | Don't build | Use instead | Why |
|---------|-------------|-------------|-----|
| OpenAI-compat client | custom fetch | OpenAI SDK already in Lobe via `agent-runtime/openai` | Streaming, retries, error mapping handled |
| First-run gate | sessionStorage hack | `localStorage` + cookie + i18next `changeLanguage` | Lobe respects `NEXT_LOCALE` cookie for SSR |
| Provider strip via feature flag | conditional wiring | hard-delete files | Reduces upgrade-merge surface |
| Schema namespace via prefix | `chat_app_users`, `chat_app_messages` | Postgres `SCHEMA chat_app` + Drizzle `pgSchema()` | Cleaner upgrade replay |
| Re-implementing NextAuth | scratch session JWT | Phase 20 = Supabase auth-helpers | Already targeted in master plan |

---

## Common Pitfalls

### Pitfall 1: License risk surfaces only after fork
**What goes wrong:** Team forks, strips, rebrands, ships — receives DMCA from LobeHub LLC.
**Prevention:** Email `hello@lobehub.com` BEFORE first commit to Hive's chat-app dir.
**Warning sign:** Any internal doc that says "Apache-2.0" without qualifying with "Community License".

### Pitfall 2: v2.x mistaken for "Lobe Chat"
**What goes wrong:** Pinning v2.1.52 imports a 50+ package agent-platform that takes weeks to understand, not a chat UI.
**Prevention:** Pin v1.143.3 explicitly in `LOBE-VERSION.md` with commit SHA `d718de7e1af2dd7281c4eaca70f627e4b9deddb1`.

### Pitfall 3: Workers blocker discovered in Phase 24
**What goes wrong:** Phases 19–23 all assume Workers. Phase 24 spike fails. Replan emergency.
**Prevention:** This research surfaces it now. Phase 19 boot target = Docker. Workers stays a Phase 24 "compatibility spike that may fail" item, with Fly.io as documented fallback.

### Pitfall 4: bn-BD locale assumed present
**What goes wrong:** Picker built, points at non-existent locale, Bengali UI never renders.
**Prevention:** Phase 19 task — copy `en-US/*.json` → `bn-BD/*.json` skeleton FIRST, before picker.

### Pitfall 5: Schema collision in `public`
**What goes wrong:** Lobe migrations create `users` in `public` — collides with Hive's `auth.users` exposure or future identity tables.
**Prevention:** `pgSchema('chat_app')` from day one. Drizzle migration generated against `chat_app` only.

### Pitfall 6: Per-user provider override unlocked
**What goes wrong:** End user opens settings, swaps base URL to OpenAI direct, exfiltrates Hive API key (or uses pre-paid Hive credits to call OpenAI without Hive sanitizer/audit).
**Prevention:** Lock `OPENAI_PROXY_URL` and `OPENAI_API_KEY` settings UI to read-only system value.

### Pitfall 7: `KEY_VAULTS_SECRET` rotation breaks all keys
**What goes wrong:** Lobe encrypts user-stored API keys with `KEY_VAULTS_SECRET`. Rotating it without re-encryption strategy invalidates all user-stored keys. (Less impactful for Hive since system-managed key is system-side, but still relevant if user-keys feature retained.)
**Prevention:** Treat `KEY_VAULTS_SECRET` as forever-key OR build re-encryption migration up front.

### Pitfall 8: Lobe migrations replay against wrong schema
**What goes wrong:** Upstream upgrade later re-imports 41 migrations and runs them against `public`.
**Prevention:** Hive's regenerated baseline lives in `supabase/migrations/`. Upstream's `packages/database/migrations/` should be deleted from fork OR clearly marked "do not run" in upgrade playbook.

---

## Docker Compose Service Draft

Aligned with existing `deploy/docker/docker-compose.yml` patterns (port 3000 used by web-console; chat-app uses 3210 to match Lobe `next start -p 3210`):

```yaml
  chat-app:
    image: hive-chat-app:ci
    build:
      context: ../../
      dockerfile: deploy/docker/Dockerfile.chat-app
    ports:
      - "3210:3210"
    depends_on:
      edge-api:
        condition: service_healthy
      control-plane:
        condition: service_healthy
    environment:
      # Provider wiring → Hive edge-api
      OPENAI_API_KEY: ${HIVE_API_KEY}
      OPENAI_PROXY_URL: http://edge-api:8080/v1
      OPENAI_MODEL_LIST: ${HIVE_MODEL_LIST:-gpt-4o-mini,gpt-4o,gpt-3.5-turbo}
      # Database — Supabase Postgres, chat_app schema
      DATABASE_URL: ${SUPABASE_DB_URL}
      DATABASE_DRIVER: node
      KEY_VAULTS_SECRET: ${LOBE_KEY_VAULTS_SECRET}
      # NextAuth (placeholder; Phase 20 replaces with Supabase)
      NEXT_AUTH_SECRET: ${LOBE_NEXTAUTH_SECRET}
      NEXTAUTH_URL: ${NEXTAUTH_URL:-http://localhost:3210/api/auth}
      # File storage — reuse Hive Supabase bucket
      S3_ENDPOINT: ${S3_ENDPOINT}
      S3_ACCESS_KEY_ID: ${S3_ACCESS_KEY}
      S3_SECRET_ACCESS_KEY: ${S3_SECRET_KEY}
      S3_REGION: ${S3_REGION}
      S3_BUCKET: ${S3_BUCKET_FILES:-hive-files}
      # Default locale (overridden by picker)
      DEFAULT_AGENT_CONFIG: ${LOBE_DEFAULT_AGENT_CONFIG:-}
    healthcheck:
      test: ["CMD", "wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:3210/api/health"]
      interval: 5s
      timeout: 3s
      retries: 5
      start_period: 60s
```

### Profile decision
- **No profile** (default startup) — chat-app is a first-class service like web-console, not opt-in like SDK tests.
- redis dependency NOT added — Lobe's optional cache only; not in v19 scope.
- Build context = repo root (`../../`) consistent with other services.

### Healthcheck
- Lobe v1.143.3 exposes `/api/health` (verify in plan; if not present, use `/api/auth/providers` HEAD).

**Confidence:** HIGH on env-var contract (verified .env.example); MEDIUM on healthcheck endpoint (assumed; verify at PLAN time).

---

## Code Examples

### Drizzle pgSchema wrap
```typescript
// packages/database/src/schemas/_schema.ts (NEW — Hive addition)
import { pgSchema } from 'drizzle-orm/pg-core';
export const chatAppSchema = pgSchema('chat_app');

// packages/database/src/schemas/user.ts (modified)
import { chatAppSchema } from './_schema';
export const users = chatAppSchema.table('users', {
  id: uuid('id').primaryKey(),
  email: text('email').notNull().unique(),
  // ... rest unchanged
});
```

### OpenAI provider single-source lock
```typescript
// src/config/modelProviders/index.ts (post-strip)
import { OpenAIProviderCard } from './openai';
export const DEFAULT_MODEL_PROVIDER_LIST = [OpenAIProviderCard];
export const filterEnabledModels = (...) => DEFAULT_MODEL_PROVIDER_LIST;
// All other exports → empty arrays / no-op
```

### Variant-aware locale cookie
```typescript
// src/components/FirstRunGate.tsx — see §Language Picker for full snippet.
// Key call: document.cookie = `NEXT_LOCALE=${locale};path=/;max-age=31536000;samesite=lax`;
```

---

## State of the Art

| Old approach | Current approach | When changed |
|--------------|------------------|--------------|
| Lobe Chat as single Next.js app | Lobe Hub as multi-package agent platform | v2.0.0 (2026-01-27) — **avoid v2.x for this fork** |
| `lobehub/lobe-chat` GitHub repo | `lobehub/lobehub` (rename) | with v2.0.0 — old URLs redirect, but pin against new canonical |
| `@cloudflare/next-on-pages` | `@opennextjs/cloudflare` (Workers) | already in Hive web-console; same pattern WHERE applicable |
| Plain Apache-2.0 (pre-1.0) | LobeHub Community License (post-1.0) | v1.0 onward — license restriction is recent-ish, check trap |

---

## Validation Architecture

> Master plan + repo carry no `workflow.nyquist_validation` flag. Treat as enabled.

### Test framework
| Property | Value |
|----------|-------|
| Framework | Vitest (Lobe upstream uses Vitest); Playwright for E2E |
| Config file | `vitest.config.ts` (forked from upstream); `apps/web-console/playwright.config.ts` (Hive pattern reused) |
| Quick run | `pnpm vitest run --reporter=basic <path>` |
| Full suite | `pnpm test` |

### Phase requirements → test map

| Req ID | Behavior | Test type | Command | File exists? |
|--------|----------|-----------|---------|--------------|
| CHATAPP-19-01 | Fork at `v1.143.3` SHA | smoke | `git -C apps/chat-app rev-parse HEAD == d718de7e...` | Wave 0 (post-fork) |
| CHATAPP-19-02 | Provider list cardinality = 1 | unit | `pnpm vitest run src/config/modelProviders/index.test.ts` | Wave 0 |
| CHATAPP-19-03 | Chat completion via Hive edge-api | integration | `docker compose --profile local up -d && curl -X POST http://localhost:3210/api/chat/openai ...` | Wave 0 |
| CHATAPP-19-04 | `chat_app` schema applied | integration | `psql $SUPABASE_DB_URL -c "\dn chat_app"` | Wave 0 |
| CHATAPP-19-05 | Compose service starts healthy | smoke | `docker compose --profile local up -d chat-app && docker compose ps chat-app` | Wave 0 |
| CHATAPP-19-06 | Picker shows on first visit, persists choice | E2E | `npx playwright test e2e/first-run-picker.spec.ts` | Wave 0 |
| CHATAPP-19-07 | Upgrade-playbook procedure documented | manual | review of `LOBE-UPGRADE-PLAYBOOK.md` | Wave 0 |
| CHATAPP-19-08 | Build green | smoke | `pnpm --filter chat-app build` | Wave 0 |

### Sampling rate
- Per task commit: `pnpm --filter chat-app vitest run --reporter=basic <changed-area>`
- Per wave merge: `pnpm --filter chat-app test` + `pnpm --filter chat-app build`
- Phase gate: full suite green + Docker compose smoke + first-run picker E2E

### Wave 0 gaps
- [ ] `apps/chat-app/` directory does not yet exist (Wave 1, task 1)
- [ ] `deploy/docker/Dockerfile.chat-app` — new
- [ ] `e2e/first-run-picker.spec.ts` — new
- [ ] `supabase/migrations/2026XXXX_01_chat_app_baseline.sql` — generated post-fork
- [ ] `.planning/v1.1-chatapp/LOBE-VERSION.md` — record commit SHA
- [ ] `.planning/v1.1-chatapp/LOBE-UPGRADE-PLAYBOOK.md` — new
- [ ] `.planning/v1.1-chatapp/LICENSE-DECISION.md` — document outcome of LobeHub LLC contact

---

## Open Questions / Risks for Planner

1. **License decision (BLOCKER).** Will Hive obtain commercial license from LobeHub LLC, OR pivot to MIT-licensed alternative (LibreChat / Open WebUI / Chatbot UI), OR keep Lobe stock-unmodified?
   - Recommendation: dispatch a one-off task BEFORE Phase 19 PLAN.md to email LobeHub LLC and decide.

2. **Workers vs. Docker hosting for chat-app (architectural).** Master plan §Phase 24 assumes Workers. Research evidence says Lobe-on-Workers is BLOCKED. Should chat-app deploy on Fly.io/VPS (Docker) while web-console stays on Workers? If so, Phase 24 scope changes.

3. **Provider override lockdown depth.** Lobe lets each user paste a custom proxy URL in settings. If we hide the field, do we also need to block the underlying API endpoint that updates user provider config? (Defense-in-depth for Pitfall 6.)

4. **Lobe v1.143.x EOL.** With v2.0.0 already shipped (2026-01-27), how long will LobeHub LLC patch v1.143.x for security? Master plan's "upgrade playbook" assumes future v1.x releases, but upstream has moved on. Likely Hive owns security patches for stripped fork.

5. **OpenAI SDK version Lobe uses vs. Hive contract.** Verify Lobe's bundled OpenAI SDK matches what Hive `/v1` actually serves (function calling shape, tool_calls schema, audio modality coverage).

6. **`packages/database` workspace placement.** Lobe v1.143.3 already uses `packages/database` workspace. Hive's repo is Go-first with one Next.js app — does chat-app live as standalone `apps/chat-app/` (with internal `packages/database`) or do we promote `packages/database` to repo root? Lean toward **keep as-is, isolated under apps/chat-app/**.

7. **Bengali default model.** Phase 23 picks; Phase 19 needs to pick a *bootable* default for `OPENAI_MODEL_LIST`. Recommend `gpt-4o-mini` — acceptable Bengali, low cost, broadly supported.

8. **`KEY_VAULTS_SECRET` provisioning.** Generate-and-store in Hive's existing secret store; document rotation playbook.

9. **Sharp on Docker.** `sharp` works fine in Docker (Lobe's standard target); only Workers-blocked. Confirm Dockerfile uses a base image with libvips (`node:20-bookworm-slim` works; `node:20-alpine` needs `apk add vips-dev`).

10. **Strip surface re-discovery on upgrade.** Each future Lobe upgrade may add a new provider Hive must re-strip. Upgrade playbook should include `git diff --stat upstream/v1.143.3..upstream/<new-tag> -- src/config/modelProviders/` as the "what new providers were added" check.

---

## Sources

### Primary (HIGH confidence)
- `gh release list --repo lobehub/lobehub --limit 100 --exclude-pre-releases` → tag inventory
- `gh api repos/lobehub/lobehub/git/trees/d718de7e...?recursive=1` → file tree at v1.143.3 (5283 blobs)
- `gh api repos/lobehub/lobehub/contents/LICENSE?ref=v1.143.3` (raw) → LobeHub Community License full text
- `gh api repos/lobehub/lobehub/contents/.env.example?ref=v1.143.3` (raw) → `OPENAI_PROXY_URL`, `OPENAI_MODEL_LIST`, `KEY_VAULTS_SECRET`, `DATABASE_URL` confirmed
- `gh api repos/lobehub/lobehub/contents/package.json?ref=v1.143.3` (raw) → Next 15.3.6, React 19.2.1, Drizzle 0.44.6, sharp/pg/ws/pdf-parse/mammoth deps confirmed
- `gh issue view 4241 --repo lobehub/lobe-chat` → maintainer `arvinxx` confirms no Cloudflare deployment support
- `/home/sakib/hive/CLAUDE.md`, `/home/sakib/hive/.planning/v1.1-chatapp/V1.1-MASTER-PLAN.md`, `/home/sakib/hive/deploy/docker/docker-compose.yml`, `/home/sakib/hive/apps/web-console/{wrangler.jsonc,open-next.config.ts,next.config.ts}` → Hive-side patterns

### Secondary (MEDIUM confidence)
- [OpenNext Cloudflare adapter](https://opennext.js.org/cloudflare) — Workers compat baseline (generic, not Lobe-specific)
- [Cloudflare Workers Next.js docs](https://developers.cloudflare.com/workers/framework-guides/web-apps/nextjs/) — `nodejs_compat`, compatibility date guidance
- [Cloudflare blog — OpenNext adapter](https://blog.cloudflare.com/deploying-nextjs-apps-to-cloudflare-workers-with-the-opennext-adapter/) — Next 14/15 + Workers
- [LobeHub self-hosting Vercel docs](https://lobehub.com/docs/self-hosting/platform/vercel) — alternative deploy target reference

### Tertiary (LOW confidence — flagged for verification at PLAN time)
- Drizzle 0.44.6 `pgSchema()` syntax for migration generation — verify against drizzle-kit docs at PLAN time.
- `/api/health` endpoint presence at v1.143.3 — verify by file probe before Dockerfile commit.
- Sharp libvips bundling on Workers via WASM — current consensus is "no", but watch for Cloudflare native-binding announcements.

---

## Metadata

**Confidence breakdown:**
- Pinned tag selection: **HIGH** — release list + tree comparison verified.
- Provider-strip surface: **HIGH** — 68 files enumerated.
- Schema migration plan: **MEDIUM** — pgSchema mechanics verified by docs but not by hands-on migration generation.
- Workers compat: **HIGH** — maintainer statement + dep audit.
- Language picker: **MEDIUM** — locale absence verified, exact insertion path needs file probe.
- Auth state: **HIGH** — 14 files inventoried.
- License: **HIGH** — full LICENSE fetched.
- Docker compose: **HIGH** — pattern matches existing services.

**Research date:** 2026-04-25
**Valid until:** 2026-05-25 for tag/version data; license risk is permanent until LobeHub LLC outcome known.
