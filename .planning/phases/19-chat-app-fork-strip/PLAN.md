---
phase: 19-chat-app-fork-strip
plan: 01
type: execute
wave: 1
depends_on: []
branch: b/phase-19-chat-app-fork-strip
milestone: v1.1
track: B
files_modified:
  - apps/chat-app/                                # full LibreChat fork tree (read-only baseline; only config + locale + modal touched)
  - apps/chat-app/librechat.yaml
  - apps/chat-app/.env.example
  - apps/chat-app/Dockerfile
  - apps/chat-app/client/src/locales/bn-BD/translation.json
  - apps/chat-app/client/src/components/Nav/LanguagePicker/FirstRunLanguageModal.tsx
  - apps/chat-app/client/src/components/Nav/LanguagePicker/index.ts
  - apps/chat-app/client/src/App.tsx
  - apps/chat-app/client/src/hooks/useFirstRunLocale.ts
  - deploy/docker/docker-compose.yml
  - .env.example
  - .planning/v1.1-chatapp/LIBRECHAT-VERSION.md
  - .planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md
  - .planning/phases/19-chat-app-fork-strip/19-VERIFICATION.md
  - .planning/REQUIREMENTS.md
  - .github/workflows/chat-app-ci.yml
autonomous: true
requirements:
  - CHATAPP-19-01   # LibreChat v0.7.9 forked into apps/chat-app/ with pinned commit SHA recorded
  - CHATAPP-19-02   # librechat.yaml strips non-Hive providers; single endpoints.custom[] points at edge-api:8080/v1
  - CHATAPP-19-03   # FX zero-leak: interface.showCost=false, interface.showTokens=false (Phase 17 mandate enforcement point)
  - CHATAPP-19-04   # MongoDB connection env-driven; Atlas M0 wired for staging, local Mongo for dev profile
  - CHATAPP-19-05   # bn-BD locale verified-or-scaffolded (skeleton only; Phase 23 owns translations)
  - CHATAPP-19-06   # First-run language-picker modal (localStorage-gated) persists locale via prefs hook (Phase 20 wires to DB)
  - CHATAPP-19-07   # Docker Compose service `chat-app` boots locally with healthcheck, depends_on edge-api+control-plane
  - CHATAPP-19-08   # LIBRECHAT-UPGRADE-PLAYBOOK.md documents upstream-track + re-strip procedure
  - CHATAPP-19-09   # CI workflow runs lint + typecheck + build green for chat-app workspace
must_haves:
  truths:
    - "`apps/chat-app/` is a verbatim fork of `danny-avila/LibreChat@v0.7.9` (pre-Admin-Panel) and the exact commit SHA is recorded in `.planning/v1.1-chatapp/LIBRECHAT-VERSION.md`."
    - "`apps/chat-app/librechat.yaml` declares exactly one custom endpoint pointing at `http://edge-api:8080/v1`; no Anthropic/Google/Mistral/etc providers are exposed; per-user endpoint override is disabled."
    - "`apps/chat-app/librechat.yaml` `interface:` block sets `showCost: false` and any token/cost-display toggle to false — zero customer-visible USD/FX/token-cost surface (Phase 17 mandate)."
    - "MongoDB connection string is read from env (`MONGO_URI`); `.env.example` documents Atlas M0 free-tier format for staging and a local-Mongo fallback string for `--profile local`."
    - "Either upstream `bn-BD` locale exists in LibreChat v0.7.9 (verified at fork) OR a `bn-BD/translation.json` skeleton was scaffolded from `en-US` (English strings retained — Phase 23 translates)."
    - "First visit to chat-app shows a language-picker modal (bn-BD / en-US); choice writes to `localStorage.hive_locale_v1`; modal does not re-show on subsequent visits; locale propagates to react-i18next."
    - "`docker compose --env-file ../../.env --profile local up chat-app` produces a service that passes its healthcheck and serves the LibreChat UI on its mapped port."
    - "GitHub Actions workflow `chat-app-ci.yml` runs lint, typecheck, and build for `apps/chat-app/` on PR; v1.1 branch ships green."
    - "Phase 19 boot target is Docker compose locally — NO OCI deploy in Phase 19; OCI is Phase 24."
  artifacts:
    - path: "apps/chat-app/librechat.yaml"
      provides: "Single-provider Hive config + FX zero-leak interface block."
      contains: "endpoints"
    - path: "apps/chat-app/Dockerfile"
      provides: "Multi-stage build for the chat-app container, ARM64 compatible (target Phase 24 OCI Ampere)."
      contains: "FROM"
    - path: "deploy/docker/docker-compose.yml"
      provides: "`chat-app` service definition with depends_on edge-api+control-plane and Mongo env wiring."
      contains: "chat-app:"
    - path: ".planning/v1.1-chatapp/LIBRECHAT-VERSION.md"
      provides: "Pinned upstream tag + commit SHA + fork date + verification of `bn-BD` locale presence."
      contains: "v0.7.9"
    - path: ".planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md"
      provides: "Procedure for tracking upstream LibreChat releases and re-applying Hive strip + FX guard."
      contains: "upstream"
    - path: "apps/chat-app/client/src/components/Nav/LanguagePicker/FirstRunLanguageModal.tsx"
      provides: "First-run language picker modal (Recoil-aware, react-i18next-driven)."
      contains: "bn-BD"
    - path: ".github/workflows/chat-app-ci.yml"
      provides: "CI lint + typecheck + build gate for the chat-app workspace."
      contains: "chat-app"
  key_links:
    - from: "apps/chat-app/librechat.yaml"
      to: "http://edge-api:8080/v1"
      via: "endpoints.custom[].baseURL"
      pattern: "edge-api:8080"
    - from: "deploy/docker/docker-compose.yml chat-app service"
      to: "edge-api + control-plane services"
      via: "depends_on with service_healthy condition"
      pattern: "service_healthy"
    - from: "apps/chat-app/client/src/App.tsx"
      to: "FirstRunLanguageModal"
      via: "mount via useFirstRunLocale() hook checking localStorage.hive_locale_v1"
      pattern: "FirstRunLanguageModal"
    - from: "apps/chat-app/librechat.yaml interface:"
      to: "Phase 17 FX zero-leak mandate"
      via: "showCost: false, showTokens: false"
      pattern: "showCost: false"
---

<objective>
Fork `danny-avila/LibreChat@v0.7.9` into `apps/chat-app/`, strip it to a single Hive-only OpenAI-compatible provider, enforce the Phase-17 FX zero-leak mandate at the chat-app surface, wire MongoDB env-driven (Atlas M0 for staging / local Mongo for dev), verify-or-scaffold the `bn-BD` locale skeleton, and add a first-run language-picker modal whose choice persists via a hook ready for Phase 20 to wire to Supabase user prefs. Boot target: `docker compose --profile local up chat-app` only — OCI deploy is explicitly Phase 24's problem; Workers is web-console-only and out of scope.

Purpose: v1.1 ship-gate (per `.planning/v1.1-chatapp/V1.1-MASTER-PLAN.md` §Cross-Phase Concerns) requires a working Hive-branded chat-app with zero customer-visible USD/FX surface. Phase 19 lays the LibreChat baseline + FX guard so Phases 20–25 (auth, tier, RAG, i18n, deploy, UAT) can stack on a stable, MIT-clean fork.

Output:
- `apps/chat-app/` LibreChat v0.7.9 fork (commit SHA recorded)
- `apps/chat-app/librechat.yaml` (single Hive endpoint + FX zero-leak `interface:` block)
- `apps/chat-app/Dockerfile` + `deploy/docker/docker-compose.yml chat-app` service
- `apps/chat-app/client/src/locales/bn-BD/translation.json` skeleton (only if upstream missing)
- `apps/chat-app/client/src/components/Nav/LanguagePicker/FirstRunLanguageModal.tsx` + `useFirstRunLocale.ts` hook
- `.planning/v1.1-chatapp/LIBRECHAT-VERSION.md` + `LIBRECHAT-UPGRADE-PLAYBOOK.md`
- `.github/workflows/chat-app-ci.yml`
- `.planning/REQUIREMENTS.md` updated with `CHATAPP-19-01..09` rows
- `.planning/phases/19-chat-app-fork-strip/19-VERIFICATION.md`
</objective>

<execution_context>
@/home/sakib/.claude/get-shit-done/workflows/execute-plan.md
@/home/sakib/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/STATE.md
@.planning/REQUIREMENTS.md
@.planning/v1.1-chatapp/V1.1-MASTER-PLAN.md
@.planning/v1.1-chatapp/LICENSE-DECISION.md
@.planning/phases/19-chat-app-fork-strip/RESEARCH-LIBRECHAT.md
@.planning/phases/11-verification-cleanup/PLAN.md
@CLAUDE.md
@deploy/docker/docker-compose.yml

<interfaces>
<!-- Locked decisions sourced from .planning/phases/19-chat-app-fork-strip/RESEARCH-LIBRECHAT.md
     and .planning/v1.1-chatapp/LICENSE-DECISION.md. Do NOT re-litigate. -->

Upstream:
- Repo: danny-avila/LibreChat
- Pinned tag: v0.7.9 (pre-Admin-Panel; smaller strip surface than v0.8.x)
- License: MIT (plain — no §1.b commercial clause)
- Stack: Vite + React 18 + Recoil + react-i18next (NOT Next.js)
- Backend: Node Express server + MongoDB (chat history, agent definitions)
- Optional sidecar (Phase 22, NOT Phase 19): librechat-rag-api Python service

Hive integration:
- Single OpenAI-compatible endpoint: http://edge-api:8080/v1
- librechat.yaml `endpoints.custom[]` pattern (NOT deprecated `OPENAI_REVERSE_PROXY` env)
- Per-user endpoint override DISABLED
- MongoDB: Atlas M0 free 512MB for staging/soft-launch; local Mongo container for dev `--profile local`
- FX zero-leak: librechat.yaml `interface.showCost: false`, any token-cost UI toggle off (Phase 17 mandate)

Locale (Phase 19 verifies-or-scaffolds; Phase 23 translates):
- Recommended code: `bn-BD` (NOT plain `bn`)
- bn-BD locale upstream UNCONFIRMED at v0.7.9 — Phase 19 must verify and either accept or scaffold from `en-US`

First-run picker:
- Storage: localStorage key `hive_locale_v1`
- Modal blocks app on first visit only
- On choice: writes localStorage + dispatches react-i18next change + calls hook for Phase 20 to forward to Supabase user prefs
- Hook surface: `useFirstRunLocale()` returns `{ locale, setLocale, isFirstRun }`

Out of scope for Phase 19 (do NOT do these):
- Supabase auth swap (Phase 20)
- Tier limits + invite/referral (Phase 21)
- File upload + RAG sidecar / `librechat-rag-api-dev-lite` ARM64 image (Phase 22)
- Bengali translation strings + Bengali default model (Phase 23)
- OCI deploy + CF DNS + production secrets (Phase 24)
- UAT + soft launch (Phase 25)

ARM64 risk note (Phase 22, NOT Phase 19): librechat-rag-api-dev-lite may lack ARM64 image; fallback is `docker buildx` from source. Phase 19 must NOT block on this.
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Fork LibreChat v0.7.9 into apps/chat-app/, pin SHA, write LIBRECHAT-VERSION.md, verify bn-BD locale presence</name>
  <files>
    apps/chat-app/,
    .planning/v1.1-chatapp/LIBRECHAT-VERSION.md,
    apps/chat-app/client/src/locales/bn-BD/translation.json
  </files>
  <action>
    Step 1 — Fork. From repo root:
    ```
    git clone --branch v0.7.9 --depth 1 https://github.com/danny-avila/LibreChat.git apps/chat-app
    cd apps/chat-app
    git rev-parse HEAD            # capture SHA
    rm -rf .git                   # detach from upstream history; we vendor the fork
    cd -
    ```
    Vendor the tree into the Hive monorepo. Do NOT preserve upstream `.git`. Hive monorepo's git tracks the fork as ordinary source.

    Step 2 — Record version. Write `.planning/v1.1-chatapp/LIBRECHAT-VERSION.md`:
    ```
    ---
    upstream: danny-avila/LibreChat
    pinned_tag: v0.7.9
    commit_sha: <SHA captured above>
    forked_at: <ISO date of fork>
    forked_by: gsd executor (Phase 19)
    rationale: |
      Pre-Admin-Panel release — smaller strip surface than v0.8.x. License plain MIT.
      v0.8.5-rc1 GA imminent at fork date but deferred for v1.1 stability.
    upstream_compat:
      next_admin_panel_release: v0.8.0+ (deferred to v1.2 evaluation)
    ---

    # LibreChat upstream version pin

    Hive chat-app (`apps/chat-app/`) is a vendored fork of LibreChat at the tag + SHA above.
    Upgrade procedure: see LIBRECHAT-UPGRADE-PLAYBOOK.md.
    ```

    Step 3 — Verify `bn-BD` locale. Inspect the cloned tree at the locales path (LibreChat v0.7.9 stores locales under `client/src/locales/<code>/translation.json`). Two outcomes:
    - **Present:** `client/src/locales/bn-BD/translation.json` exists upstream → record `bn_bd_locale_upstream: present` in LIBRECHAT-VERSION.md frontmatter; do NOT modify.
    - **Absent:** copy `client/src/locales/en-US/translation.json` → `client/src/locales/bn-BD/translation.json` (English strings retained as skeleton). Record `bn_bd_locale_upstream: scaffolded_from_en_us` in LIBRECHAT-VERSION.md. Phase 23 owns translation work.

    Also record whichever code(s) upstream uses for Bengali (`bn`, `bn-BD`, both) so Phase 23 has zero ambiguity.

    Constraint: this task only forks + vendors + scaffolds-locale. NO librechat.yaml, NO Dockerfile, NO compose changes here.
  </action>
  <verify>
    <automated>test -d apps/chat-app && test -f apps/chat-app/package.json && test -f .planning/v1.1-chatapp/LIBRECHAT-VERSION.md && grep -q "v0.7.9" .planning/v1.1-chatapp/LIBRECHAT-VERSION.md && grep -qE "bn_bd_locale_upstream:\s*(present|scaffolded_from_en_us)" .planning/v1.1-chatapp/LIBRECHAT-VERSION.md && test -f apps/chat-app/client/src/locales/bn-BD/translation.json</automated>
  </verify>
  <done>
    `apps/chat-app/` exists as a vendored fork of LibreChat v0.7.9 with no nested `.git`. `.planning/v1.1-chatapp/LIBRECHAT-VERSION.md` records pinned tag, commit SHA, fork date, and `bn_bd_locale_upstream` status. `bn-BD/translation.json` exists (upstream-original or scaffolded from en-US).
  </done>
</task>

<task type="auto">
  <name>Task 2: Strip providers + enforce FX zero-leak via librechat.yaml + .env.example</name>
  <files>
    apps/chat-app/librechat.yaml,
    apps/chat-app/.env.example,
    .env.example
  </files>
  <action>
    Step 1 — Write `apps/chat-app/librechat.yaml` with exactly one custom endpoint and the FX zero-leak interface block:
    ```yaml
    version: 1.0.5
    cache: true

    interface:
      # FX/USD ZERO-LEAK MANDATE (Phase 17). DO NOT FLIP TO TRUE.
      # Customer-visible USD/FX is a regulatory blocker for the BD market.
      showCost: false
      showTokens: false        # if upstream key differs at v0.7.9, mirror exact key name
      privacyPolicy:
        externalUrl: ""
      termsOfService:
        externalUrl: ""

    endpoints:
      custom:
        - name: "Hive"
          apiKey: "${HIVE_EDGE_API_KEY}"
          baseURL: "${HIVE_EDGE_API_BASE_URL}"   # default http://edge-api:8080/v1
          models:
            default: ["gpt-4o-mini"]              # placeholder; Phase 23 sets Bengali-strong default
            fetch: false                          # Hive control-plane catalog is the source of truth
          titleConvo: true
          titleModel: "gpt-4o-mini"
          summarize: false
          forcePrompt: false
          modelDisplayLabel: "Hive"
          # LOCK DOWN per-user endpoint override
          userIdQuery: false
          addParams: {}
          dropParams: []
    ```

    Verify against LibreChat v0.7.9 schema at fork time. If `showTokens` is not the exact key name in v0.7.9 (LibreChat key naming may differ — check upstream schema), use the actual upstream key for token-cost display and add a comment naming the original. The contract is: **no customer-visible USD/FX/token-cost rendering anywhere in chat-app UI**.

    Step 2 — Strip non-Hive providers: ensure no `endpoints.openAI`, `endpoints.anthropic`, `endpoints.google`, `endpoints.assistants`, `endpoints.azureOpenAI`, `endpoints.bedrock`, etc. blocks exist. Only `endpoints.custom[]` with the single Hive entry.

    Step 3 — Author `apps/chat-app/.env.example`:
    ```
    # ===== Hive chat-app (LibreChat fork) =====
    # Phase 19 boot target: docker compose --profile local up chat-app

    # Upstream LibreChat env (subset)
    HOST=0.0.0.0
    PORT=3080
    DOMAIN_CLIENT=http://localhost:3080
    DOMAIN_SERVER=http://localhost:3080
    NODE_ENV=development

    # MongoDB — Atlas M0 free 512MB for staging; local mongo container for dev
    # Staging:    MONGO_URI=mongodb+srv://<user>:<pass>@<cluster>.mongodb.net/hive-chat?retryWrites=true&w=majority
    # Local dev:  MONGO_URI=mongodb://mongo:27017/hive-chat
    MONGO_URI=mongodb://mongo:27017/hive-chat

    # Hive edge-api (single OpenAI-compatible provider)
    HIVE_EDGE_API_BASE_URL=http://edge-api:8080/v1
    HIVE_EDGE_API_KEY=                # provisioned per environment

    # Auth — placeholder; Phase 20 swaps to Supabase
    JWT_SECRET=replace-me-phase-19-only
    JWT_REFRESH_SECRET=replace-me-phase-19-only

    # Telemetry
    DEBUG_LOGGING=false
    DEBUG_CONSOLE=false

    # ALLOWED EMAIL/SOCIAL PROVIDERS — Phase 19 leaves email-only signup; Phase 20 owns Supabase
    ALLOW_EMAIL_LOGIN=true
    ALLOW_REGISTRATION=true
    ALLOW_SOCIAL_LOGIN=false
    ```

    Step 4 — Update repo-root `.env.example` to add the chat-app block (commented, mirroring `apps/chat-app/.env.example`) so `deploy/docker/docker-compose.yml --env-file ../../.env` picks up the same vars.

    Constraint: NO code changes under `apps/chat-app/api/` or `apps/chat-app/client/` in this task — config + env only.
  </action>
  <verify>
    <automated>test -f apps/chat-app/librechat.yaml && grep -q "showCost: false" apps/chat-app/librechat.yaml && grep -q "edge-api:8080/v1" apps/chat-app/librechat.yaml && ! grep -qE "^\s*(openAI|anthropic|google|azureOpenAI|bedrock):" apps/chat-app/librechat.yaml && test -f apps/chat-app/.env.example && grep -q "MONGO_URI=" apps/chat-app/.env.example && grep -q "HIVE_EDGE_API_BASE_URL" apps/chat-app/.env.example && grep -q "HIVE_EDGE_API_BASE_URL" .env.example</automated>
  </verify>
  <done>
    `librechat.yaml` declares exactly one `endpoints.custom[]` (Hive) pointing at `http://edge-api:8080/v1`, has `interface.showCost: false` (and the v0.7.9 equivalent token-cost toggle off), and contains zero non-Hive provider blocks. `apps/chat-app/.env.example` documents Atlas M0 + local-Mongo connection strings. Root `.env.example` updated with chat-app block.
  </done>
</task>

<task type="auto">
  <name>Task 3: Add first-run language-picker modal + useFirstRunLocale hook + bn-BD/en-US wiring</name>
  <files>
    apps/chat-app/client/src/components/Nav/LanguagePicker/FirstRunLanguageModal.tsx,
    apps/chat-app/client/src/components/Nav/LanguagePicker/index.ts,
    apps/chat-app/client/src/hooks/useFirstRunLocale.ts,
    apps/chat-app/client/src/App.tsx
  </files>
  <action>
    Goal: on first visit (no `localStorage.hive_locale_v1` set), show a blocking modal with two choices — Bangla (`bn-BD`) or English (`en-US`). Choice writes localStorage, switches react-i18next, and exposes a hook surface so Phase 20 can later forward locale to Supabase user prefs.

    Step 1 — Hook: `client/src/hooks/useFirstRunLocale.ts`
    ```ts
    import { useEffect, useState, useCallback } from 'react';
    import { useTranslation } from 'react-i18next';

    const LOCALE_KEY = 'hive_locale_v1';
    type SupportedLocale = 'bn-BD' | 'en-US';
    const SUPPORTED: SupportedLocale[] = ['bn-BD', 'en-US'];

    export function useFirstRunLocale() {
      const { i18n } = useTranslation();
      const [locale, setLocaleState] = useState<SupportedLocale | null>(() => {
        const v = typeof window === 'undefined' ? null : window.localStorage.getItem(LOCALE_KEY);
        return SUPPORTED.includes(v as SupportedLocale) ? (v as SupportedLocale) : null;
      });

      const isFirstRun = locale === null;

      const setLocale = useCallback((next: SupportedLocale) => {
        if (!SUPPORTED.includes(next)) return;
        window.localStorage.setItem(LOCALE_KEY, next);
        setLocaleState(next);
        void i18n.changeLanguage(next);
        // Phase 20: bridge here — forward to Supabase user prefs once auth swap lands.
        // Intentionally left as TODO; Phase 19 only persists locally.
      }, [i18n]);

      useEffect(() => {
        if (locale && i18n.language !== locale) {
          void i18n.changeLanguage(locale);
        }
      }, [locale, i18n]);

      return { locale, setLocale, isFirstRun };
    }
    ```

    Step 2 — Modal: `client/src/components/Nav/LanguagePicker/FirstRunLanguageModal.tsx`
    Use the LibreChat-native dialog primitive (verify at fork — likely `@radix-ui/react-dialog` already present at v0.7.9 since LibreChat ships shadcn-flavored UI). Modal must:
    - Render only when `isFirstRun === true`.
    - Block backdrop dismiss (no outside-click close, no ESC close).
    - Two large buttons: "বাংলা" (selects `bn-BD`) and "English" (selects `en-US`).
    - Onselect → `setLocale(choice)` → modal unmounts (since `isFirstRun` flips false).
    - No translation strings hard-coded in English elsewhere; the two button labels are intentionally bilingual-native and not i18n-routed (otherwise the user can't read them on first run).

    Step 3 — Index export: `client/src/components/Nav/LanguagePicker/index.ts` re-exports `FirstRunLanguageModal`.

    Step 4 — Mount in `client/src/App.tsx` (or v0.7.9 equivalent root component — verify at fork). Add `<FirstRunLanguageModal />` at root, sibling to existing routes/providers, after the i18n provider is mounted. Minimal-diff mount; do NOT refactor the App tree.

    Step 5 — react-i18next registration: confirm `bn-BD` is registered as a resource in the i18n init (`client/src/locales/i18n.ts` or v0.7.9 equivalent). If absent, register it pointing at the bn-BD/translation.json from Task 1. If upstream uses a different code (`bn`), add `bn-BD` as an alias resource.

    Constraint: NO Recoil atoms touched (LibreChat uses Recoil — modal stays self-contained via `useState` + `useTranslation`). NO Phase 20 Supabase calls. NO Phase 23 translation work.
  </action>
  <verify>
    <automated>test -f apps/chat-app/client/src/hooks/useFirstRunLocale.ts && grep -q "hive_locale_v1" apps/chat-app/client/src/hooks/useFirstRunLocale.ts && test -f apps/chat-app/client/src/components/Nav/LanguagePicker/FirstRunLanguageModal.tsx && grep -q "bn-BD" apps/chat-app/client/src/components/Nav/LanguagePicker/FirstRunLanguageModal.tsx && grep -q "FirstRunLanguageModal" apps/chat-app/client/src/App.tsx</automated>
  </verify>
  <done>
    `useFirstRunLocale.ts` exposes `{ locale, setLocale, isFirstRun }`, persists to `localStorage.hive_locale_v1`, and dispatches react-i18next `changeLanguage`. `FirstRunLanguageModal.tsx` renders only on first run, blocks dismissal, and offers bn-BD/en-US choices with bilingual-native labels. Modal mounted in `App.tsx`. `bn-BD` registered as i18n resource.
  </done>
</task>

<task type="auto">
  <name>Task 4: Dockerfile + docker-compose chat-app service + healthcheck + Mongo wiring</name>
  <files>
    apps/chat-app/Dockerfile,
    deploy/docker/docker-compose.yml
  </files>
  <action>
    Step 1 — `apps/chat-app/Dockerfile`. Multi-stage; ARM64-clean (target Phase 24 OCI Ampere — but Phase 19 only validates locally on whatever host arch).
    ```dockerfile
    # Stage 1 — deps + build
    FROM node:20-alpine AS builder
    WORKDIR /app
    COPY package*.json ./
    COPY client/package*.json ./client/
    COPY api/package*.json ./api/
    COPY packages ./packages
    RUN npm ci --workspaces --include-workspace-root
    COPY . .
    RUN npm run frontend                        # LibreChat client build
    RUN npm prune --omit=dev --workspaces

    # Stage 2 — runtime
    FROM node:20-alpine AS runtime
    WORKDIR /app
    ENV NODE_ENV=production
    COPY --from=builder /app /app
    EXPOSE 3080
    HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
      CMD wget -qO- http://127.0.0.1:3080/api/health || exit 1
    CMD ["node", "api/server/index.js"]
    ```
    Verify exact server entrypoint + healthcheck path against v0.7.9 (`api/server/index.js` is correct as of v0.7.9; `/api/health` reference: LibreChat upstream discussion #5961). If v0.7.9 healthcheck path differs, use the upstream-confirmed path and record in LIBRECHAT-VERSION.md.

    Step 2 — `deploy/docker/docker-compose.yml`. Add a `chat-app` service block. Read existing compose file first to match style (env-file pattern, network names, build context conventions). Skeleton (adjust to existing compose conventions):
    ```yaml
      chat-app:
        build:
          context: ../../apps/chat-app
          dockerfile: Dockerfile
        image: hive/chat-app:dev
        env_file:
          - ../../apps/chat-app/.env.example   # local dev fallback; staging uses ../../.env override
        environment:
          MONGO_URI: ${MONGO_URI:-mongodb://mongo:27017/hive-chat}
          HIVE_EDGE_API_BASE_URL: ${HIVE_EDGE_API_BASE_URL:-http://edge-api:8080/v1}
          HIVE_EDGE_API_KEY: ${HIVE_EDGE_API_KEY}
        depends_on:
          edge-api:
            condition: service_healthy
          control-plane:
            condition: service_healthy
          mongo:
            condition: service_healthy
        ports:
          - "3080:3080"
        healthcheck:
          test: ["CMD", "wget", "-qO-", "http://127.0.0.1:3080/api/health"]
          interval: 30s
          timeout: 5s
          retries: 3
          start_period: 20s
        profiles: ["local", "chat"]
        restart: unless-stopped

      mongo:
        image: mongo:7
        environment:
          MONGO_INITDB_DATABASE: hive-chat
        volumes:
          - mongo-data:/data/db
        healthcheck:
          test: ["CMD", "mongosh", "--quiet", "--eval", "db.adminCommand('ping').ok"]
          interval: 30s
          timeout: 5s
          retries: 3
          start_period: 10s
        profiles: ["local", "chat"]
        restart: unless-stopped
    ```
    And add to top-level `volumes:` section: `mongo-data:`.

    Constraint A: Mongo container is `--profile local` / `--profile chat` only. Staging/prod uses `MONGO_URI` pointing at Atlas M0 (no Mongo container). Mirror Hive's existing pattern (Redis is `--profile local` only; staging uses Upstash via `REDIS_URL`).

    Constraint B: Phase 19 boot target is `docker compose --env-file ../../.env --profile local up chat-app` only. Do NOT add OCI/CF Workers config.

    Constraint C: Compose file changes must NOT break existing `--profile local` boot (edge-api, control-plane, web-console, redis, litellm). Read existing file shape and integrate consistently.
  </action>
  <verify>
    <automated>test -f apps/chat-app/Dockerfile && grep -q "node:20-alpine" apps/chat-app/Dockerfile && grep -q "HEALTHCHECK" apps/chat-app/Dockerfile && grep -q "chat-app:" deploy/docker/docker-compose.yml && grep -q "mongo:" deploy/docker/docker-compose.yml && grep -q "service_healthy" deploy/docker/docker-compose.yml && grep -q "edge-api:8080/v1" deploy/docker/docker-compose.yml</automated>
  </verify>
  <done>
    Multi-stage `apps/chat-app/Dockerfile` builds LibreChat with a healthcheck at the v0.7.9-confirmed path. `deploy/docker/docker-compose.yml` adds `chat-app` and `mongo` services under `--profile local`/`--profile chat` with `depends_on: { edge-api: service_healthy, control-plane: service_healthy, mongo: service_healthy }` and `MONGO_URI` env-driven. Existing services unaffected.
  </done>
</task>

<task type="auto">
  <name>Task 5: CI workflow + LIBRECHAT-UPGRADE-PLAYBOOK + REQUIREMENTS update + 19-VERIFICATION</name>
  <files>
    .github/workflows/chat-app-ci.yml,
    .planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md,
    .planning/REQUIREMENTS.md,
    .planning/phases/19-chat-app-fork-strip/19-VERIFICATION.md
  </files>
  <action>
    Step 1 — CI: `.github/workflows/chat-app-ci.yml`
    ```yaml
    name: chat-app-ci
    on:
      pull_request:
        paths:
          - 'apps/chat-app/**'
          - '.github/workflows/chat-app-ci.yml'
      push:
        branches: [main, 'b/phase-19-*']
        paths:
          - 'apps/chat-app/**'

    jobs:
      lint-typecheck-build:
        runs-on: ubuntu-latest
        defaults:
          run:
            working-directory: apps/chat-app
        steps:
          - uses: actions/checkout@v4
          - uses: actions/setup-node@v4
            with:
              node-version: '20'
              cache: 'npm'
              cache-dependency-path: apps/chat-app/package-lock.json
          - run: npm ci --workspaces --include-workspace-root
          - run: npm run lint --if-present
          - run: npm run type-check --if-present
          - run: npm run frontend                # LibreChat client build (v0.7.9 script)
    ```
    Verify exact npm script names against v0.7.9 `package.json` at fork time. If `frontend` is not the build script name in v0.7.9, use the actual one.

    Step 2 — Upgrade playbook: `.planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md`
    Sections required:
    - **When to upgrade** — security CVEs, must-have feature, ≥6 months stale.
    - **Pre-upgrade checklist** — read upstream changelog, diff `librechat.yaml` schema, check for new provider blocks that must be stripped, confirm FX/cost-display key names unchanged.
    - **Procedure** — clone new tag into scratch dir; 3-way merge against vendored `apps/chat-app/`; re-apply Hive strip (single endpoint, FX off, per-user override locked); rebuild Docker image; run CI + Phase 25-style smoke against staging; only then bump pinned tag in LIBRECHAT-VERSION.md.
    - **Rollback** — git revert; reset `apps/chat-app/` from previous SHA; Mongo schema migrations (if any) run forward-only.
    - **FX guard re-verification** — explicit step: `grep -E "showCost|showTokens" apps/chat-app/librechat.yaml` must show both `false` after every upgrade. Phase 17 mandate.

    Step 3 — Update `.planning/REQUIREMENTS.md`. Append a "v1.1 Requirements" subsection if missing, then add rows for `CHATAPP-19-01..09` per the `requirements:` frontmatter list above. Each row's Evidence column links to `phases/19-chat-app-fork-strip/19-VERIFICATION.md` (which Task 5 also writes). Use the same column shape as Phase 11's REQUIREMENTS.md (ID | Phase | Status | Evidence). Set Status: `Satisfied` once Tasks 1–4 verify; if any verify command fails, mark `Pending` and explain in 19-VERIFICATION.md Blockers.

    Step 4 — Phase verification log: `.planning/phases/19-chat-app-fork-strip/19-VERIFICATION.md`. Same shape as `.planning/phases/11-verification-cleanup/11-VERIFICATION.md` (frontmatter + Must-Have Truth Verification table + Requirement Coverage + Blockers + v1.1.0 Ship-Gate Mapping). Required:
    - Must-Have table: every truth from this PLAN's `must_haves.truths` with the verify command + captured output snippet + pass/fail.
    - Requirement Coverage: rows for `CHATAPP-19-01..09`.
    - Blockers section: must address (a) v0.7.9 healthcheck path confirmation outcome, (b) bn-BD locale upstream presence/scaffold outcome, (c) v0.7.9 build-script name confirmation, (d) FX `showTokens` exact upstream key name. Empty list acceptable; heading required.
    - Ship-Gate Mapping: contributes to v1.1.0 ship-gate "FX/USD audit clean" (chat-app surface — Phase 17 mandate enforcement point) and to Track B Phase 25 boot-target (chat-app boots locally).
    - Boot evidence: paste output of `docker compose --env-file ../../.env --profile local up -d chat-app && docker compose --profile local ps chat-app` showing healthy status. If Docker not available in execution env, mark "deferred to executor host" rather than fabricate.
  </action>
  <verify>
    <automated>test -f .github/workflows/chat-app-ci.yml && grep -q "apps/chat-app" .github/workflows/chat-app-ci.yml && test -f .planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md && grep -q "showCost" .planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md && grep -q "CHATAPP-19-01" .planning/REQUIREMENTS.md && test -f .planning/phases/19-chat-app-fork-strip/19-VERIFICATION.md && grep -q "Must-Have Truth Verification" .planning/phases/19-chat-app-fork-strip/19-VERIFICATION.md && grep -q "Ship-Gate" .planning/phases/19-chat-app-fork-strip/19-VERIFICATION.md</automated>
  </verify>
  <done>
    CI workflow runs lint + typecheck + build for chat-app on PR/push. `LIBRECHAT-UPGRADE-PLAYBOOK.md` documents track-upstream + re-apply-strip + FX-guard-re-verify. `.planning/REQUIREMENTS.md` has rows for CHATAPP-19-01..09. `19-VERIFICATION.md` records pass/fail for each must_have truth, requirement coverage, blockers, and ship-gate mapping.
  </done>
</task>

</tasks>

<verification>
Phase-level verification (run after all 5 tasks):

1. `test -d apps/chat-app && test -f apps/chat-app/librechat.yaml && test -f apps/chat-app/Dockerfile`
2. `grep -q "showCost: false" apps/chat-app/librechat.yaml` — FX guard live.
3. `! grep -qE "^\s*(openAI|anthropic|google|azureOpenAI|bedrock):" apps/chat-app/librechat.yaml` — non-Hive providers stripped.
4. `grep -q "edge-api:8080/v1" apps/chat-app/librechat.yaml` — single Hive endpoint.
5. `bash scripts/verify-requirements-matrix.sh` — exits 0 (Phase 11 validator passes against new CHATAPP-19-* rows). If validator was not yet built/merged at Phase 19 execution, document deferral in 19-VERIFICATION.md.
6. `cd deploy/docker && docker compose --env-file ../../.env --profile local up -d chat-app mongo edge-api control-plane && sleep 30 && docker compose --profile local ps chat-app | grep -q "healthy"` — chat-app boots and reports healthy.
7. `cd apps/chat-app && npm ci --workspaces --include-workspace-root && npm run frontend` — client build green.
8. Manual: open `http://localhost:3080`, confirm first-run language modal appears, choose bn-BD, refresh — modal does not reappear, react-i18next switches resource. (Or run via Playwright once Phase 25 e2e exists; for Phase 19 a curl-asserted HTML snapshot is sufficient evidence.)
9. `git diff --name-only` shows zero changes under `apps/control-plane/`, `apps/edge-api/`, `apps/web-console/`, `packages/`, `supabase/` — Phase 19 strictly additive in chat-app + compose + planning.

Expected:
- (5) prints `OK: N evidence files validated`.
- (6) prints `healthy`.
- (7) exits 0.
- (9) lists only `apps/chat-app/**`, `deploy/docker/docker-compose.yml`, `.env.example`, `.github/workflows/chat-app-ci.yml`, `.planning/**`.
</verification>

<success_criteria>
Definition of Done — also feeds Track B Phase 20 input + v1.1.0 ship-gate:

- [ ] `apps/chat-app/` is a vendored fork of `danny-avila/LibreChat@v0.7.9`; SHA recorded in `.planning/v1.1-chatapp/LIBRECHAT-VERSION.md`.
- [ ] `apps/chat-app/librechat.yaml` declares one custom endpoint pointing at `http://edge-api:8080/v1`; no other provider blocks; `interface.showCost: false` and v0.7.9-equivalent token-cost toggle off (FX zero-leak — Phase 17 mandate enforcement point).
- [ ] `apps/chat-app/.env.example` + repo-root `.env.example` document Atlas M0 + local-Mongo `MONGO_URI` formats.
- [ ] `bn-BD` locale resolved (upstream-present OR scaffolded from `en-US`); status recorded in LIBRECHAT-VERSION.md; Phase 23 owns translations.
- [ ] First-run language-picker modal renders only on first visit, persists choice via `localStorage.hive_locale_v1`, switches react-i18next, and exposes a hook surface for Phase 20 to forward to Supabase prefs.
- [ ] `deploy/docker/docker-compose.yml` adds `chat-app` and `mongo` services under `--profile local`/`--profile chat`; `chat-app` has `depends_on: { edge-api: service_healthy, control-plane: service_healthy, mongo: service_healthy }`; healthcheck against v0.7.9-confirmed `/api/health` (or upstream-corrected) path.
- [ ] `docker compose --env-file ../../.env --profile local up chat-app` boots and reports `healthy`.
- [ ] `.github/workflows/chat-app-ci.yml` runs lint + typecheck + build green for chat-app workspace on PR.
- [ ] `.planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md` documents upstream-track + re-strip + FX-guard-re-verify.
- [ ] `.planning/REQUIREMENTS.md` has rows for `CHATAPP-19-01..09`, each linking to `19-VERIFICATION.md`.
- [ ] `.planning/phases/19-chat-app-fork-strip/19-VERIFICATION.md` records pass/fail for every must_have truth, includes Blockers (or empty), and explicitly maps to v1.1.0 ship-gate items "FX/USD audit clean" + Track B chat-app boot-target.
- [ ] Zero changes outside `apps/chat-app/`, `deploy/docker/docker-compose.yml`, `.env.example`, `.github/workflows/chat-app-ci.yml`, `.planning/**`.
- [ ] Branch `b/phase-19-chat-app-fork-strip` opened with single PR per V1.1-MASTER-PLAN.md branching strategy.
- [ ] OCI deploy NOT attempted (Phase 24 owns it). Cloudflare Workers NOT targeted for chat-app (web-console-only).

Ship-gate mapping:
- Closes a fragment of v1.1.0 ship-gate "FX/USD audit clean" — chat-app surface enforces `showCost`/`showTokens` off via `librechat.yaml`, and `LIBRECHAT-UPGRADE-PLAYBOOK.md` mandates re-verifying after every upstream bump.
- Establishes Track B Phase 19 deliverable per V1.1-MASTER-PLAN.md §Phase 19 (LibreChat-flavored scope).
- Does NOT close: Supabase auth swap (Phase 20), tier limits (Phase 21), RAG (Phase 22), Bengali translations + default model (Phase 23), OCI deploy (Phase 24), UAT (Phase 25).
</success_criteria>

<blockers>
Open questions for the executor — must be answered at fork time, recorded in `LIBRECHAT-VERSION.md` and/or `19-VERIFICATION.md` Blockers section:

1. **bn-BD locale upstream UNCONFIRMED at v0.7.9.** Per RESEARCH-LIBRECHAT.md, LibreChat ships wide i18n coverage but the exact `bn-BD` (vs plain `bn`) status at v0.7.9 was not confirmed pre-fork. Task 1 must verify and pick path A (use upstream as-is) or path B (scaffold from en-US). Record outcome.

2. **`librechat.yaml` v0.7.9 schema key for token-cost display.** Task 2 sets `interface.showCost: false` and `interface.showTokens: false` based on common LibreChat conventions, but the exact key name for token-cost UI rendering at v0.7.9 must be confirmed against upstream schema. The CONTRACT (no customer-visible USD/FX/cost) is non-negotiable; the key names are.

3. **v0.7.9 healthcheck path.** Task 4's Dockerfile + compose use `/api/health` per LibreChat upstream discussion #5961. v0.7.9 may differ — confirm and correct if needed; record in LIBRECHAT-VERSION.md.

4. **v0.7.9 build script name.** Task 5's CI runs `npm run frontend`. v0.7.9 may use `npm run build:client` or similar — confirm against `apps/chat-app/package.json` post-fork; correct workflow if needed.

5. **`scripts/verify-requirements-matrix.sh` availability.** Phase 19 verification step 5 invokes the validator from Phase 11 Task 3. If Phase 11 has not landed by the time Phase 19 executes, the validator step is deferred — document in 19-VERIFICATION.md and skip non-fatally.

6. **ARM64 image risk for `librechat-rag-api-dev-lite`** — per RESEARCH-LIBRECHAT.md, the RAG sidecar may lack ARM64 image; fallback is `docker buildx` from source. **Out of Phase 19 scope (Phase 22)** but flagged here so Phase 19 does NOT introduce the sidecar prematurely.

7. **MongoDB hosting decision.** LICENSE-DECISION.md parks Atlas-vs-self-hosted as an open question for Phase 19. **Decision locked here:** Atlas M0 free 512MB for staging/soft-launch; local Mongo container for dev `--profile local`. Self-hosted-on-OCI option deferred to Phase 24 if Atlas becomes a constraint.

8. **No OCI deploy in Phase 19.** Phase 24 owns deployment to OCI VM.Standard.A1.Flex Always-Free + CF Origin-CA + nginx Full(Strict). Phase 19 boot target is `docker compose --profile local up chat-app` only.
</blockers>

<output>
After completion, create `.planning/phases/19-chat-app-fork-strip/19-01-SUMMARY.md` per the GSD summary template, recording:
- Files created (apps/chat-app/* tree size, librechat.yaml, Dockerfile, modal/hook, compose additions, planning artifacts)
- Files modified (deploy/docker/docker-compose.yml, .env.example, .planning/REQUIREMENTS.md)
- Pinned LibreChat tag + commit SHA
- bn-BD locale resolution (present | scaffolded_from_en_us)
- Healthcheck path confirmation (matches /api/health Y/N — record actual)
- Token-cost UI toggle key name confirmed against v0.7.9 schema
- Boot evidence (compose ps + healthy status)
- CI run link (first PR build)
- Any Blockers carried forward to Phase 20
- Ship-gate fragment closed (FX zero-leak chat-app surface)
</output>
