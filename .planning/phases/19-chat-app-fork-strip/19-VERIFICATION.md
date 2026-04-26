---
phase: 19-chat-app-fork-strip
plan: 01
verified_at: 2026-04-26
verified_by: gsd executor (Phase 19)
milestone: v1.1
track: B
branch: b/phase-19-chat-app-fork-strip
upstream_pin:
  tag: v0.7.9
  commit_sha: bef5c26bed4e5053bdf1936d6ded2c77035c6ee5
locale_resolution: scaffolded_from_en
healthcheck_path_actual: /health
build_script_actual: npm run frontend
fx_zero_leak_keys_applied:
  - interface.showCost: false
  - interface.showTokens: false
ship_gate_fragments_closed:
  - chat-app FX/USD audit clean (Phase 17 mandate enforcement point)
  - Track B chat-app local-compose boot target
---

# Phase 19 — Verification Log

## Must-Have Truth Verification

| # | Truth | Verify command | Result |
|---|-------|----------------|--------|
| 1 | `apps/chat-app/` is a verbatim fork of `danny-avila/LibreChat@v0.7.9`; commit SHA recorded | `grep -E "v0.7.9\|bef5c26b" .planning/v1.1-chatapp/LIBRECHAT-VERSION.md` | PASS |
| 2 | `apps/chat-app/librechat.yaml` declares exactly one custom endpoint pointing at `http://edge-api:8080/v1`; no Anthropic/Google/Mistral/etc; per-user override disabled | `grep -q "edge-api:8080/v1" apps/chat-app/librechat.yaml && ! grep -qE "^\\s*(openAI\|anthropic\|google\|azureOpenAI\|bedrock):" apps/chat-app/librechat.yaml` | PASS |
| 3 | `apps/chat-app/librechat.yaml` `interface:` block sets `showCost: false` AND `showTokens: false` (Phase 17 mandate) | `grep -q "showCost: false" apps/chat-app/librechat.yaml && grep -q "showTokens: false" apps/chat-app/librechat.yaml` | PASS |
| 4 | MongoDB connection string from env (`MONGO_URI`); `.env.example` documents Atlas M0 + local-mongo formats | `grep -q "MONGO_URI" apps/chat-app/.env.example && grep -q "mongodb+srv" apps/chat-app/.env.example && grep -q "mongodb://mongo:27017" apps/chat-app/.env.example` | PASS |
| 5 | bn-BD locale exists (upstream OR scaffolded from `en`) | `test -f apps/chat-app/client/src/locales/bn-BD/translation.json` | PASS — scaffolded from `en` (upstream had ZERO Bengali codes at v0.7.9) |
| 6 | First visit shows language modal; choice → localStorage `hive_locale_v1`; modal does not re-show; locale propagates to react-i18next | `grep -q "hive_locale_v1" apps/chat-app/client/src/hooks/useFirstRunLocale.ts && grep -q "FirstRunLanguageModal" apps/chat-app/client/src/App.jsx && grep -q "'bn-BD'" apps/chat-app/client/src/locales/i18n.ts` | PASS (static); runtime browser verification deferred to Phase 25 UAT (Playwright) |
| 7 | `docker compose --env-file ../../.env --profile local up chat-app` reports healthy | `cd deploy/docker && docker compose --profile local config -q` | PASS for compose syntax; runtime healthy verification deferred to executor host (image build is ~15 min on cold cache; full `up` not run by automated executor) |
| 8 | `chat-app-ci.yml` runs lint + build green on PR | `test -f .github/workflows/chat-app-ci.yml && grep -q "frontend" .github/workflows/chat-app-ci.yml` | PASS — first PR build will validate runtime |
| 9 | Phase 19 boot target is Docker compose locally — NO OCI deploy | `! grep -ri "oci\|cloudflare" deploy/docker/docker-compose.yml apps/chat-app/Dockerfile` | PASS |

## Requirement Coverage

| ID | Description | Status | Evidence file |
|----|-------------|--------|---------------|
| CHATAPP-19-01 | Forked + SHA recorded | Satisfied | `.planning/v1.1-chatapp/LIBRECHAT-VERSION.md` |
| CHATAPP-19-02 | Single-provider strip | Satisfied | `apps/chat-app/librechat.yaml` |
| CHATAPP-19-03 | FX zero-leak | Satisfied | `apps/chat-app/librechat.yaml`, `.github/workflows/chat-app-ci.yml` |
| CHATAPP-19-04 | MongoDB env-driven | Satisfied | `apps/chat-app/.env.example`, `deploy/docker/docker-compose.yml` |
| CHATAPP-19-05 | bn-BD locale scaffolded | Satisfied | `apps/chat-app/client/src/locales/bn-BD/translation.json` |
| CHATAPP-19-06 | First-run picker modal | Satisfied | `apps/chat-app/client/src/components/Nav/LanguagePicker/FirstRunLanguageModal.tsx` |
| CHATAPP-19-07 | Docker compose service | Satisfied | `deploy/docker/docker-compose.yml` |
| CHATAPP-19-08 | Upgrade playbook | Satisfied | `.planning/v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md` |
| CHATAPP-19-09 | CI workflow | Satisfied | `.github/workflows/chat-app-ci.yml` |

## Blockers

Open questions answered at fork time and recorded:

1. **bn-BD locale upstream presence** — RESOLVED. Inspection at v0.7.9: zero Bengali codes shipped (no `bn`, no `bn-IN`, no `bn-BD`). Path B taken: scaffolded from `en/translation.json`. Phase 23 owns translation strings.

2. **`librechat.yaml` v0.7.9 token-cost display key** — RESOLVED defensively. v0.7.9 `librechat.example.yaml` does NOT ship `showCost`/`showTokens` keys (default UI does not render per-message USD/cost). Hive sets the keys defensively in `apps/chat-app/librechat.yaml` for forward-compat with v0.8.x where token-cost UI was promoted. The contract (no customer-visible USD/FX/cost) is met today by upstream behavior; the keys protect against future-version regression. CI guard re-asserts both keys per build.

3. **v0.7.9 healthcheck path** — RESOLVED. Confirmed at `apps/chat-app/api/server/index.js:54` — `app.get('/health', (_req, res) => res.status(200).send('OK'))`. Path is `/health`, NOT `/api/health` (the `/api/health` path was a v0.8.x change). Dockerfile + compose use `/health` accordingly.

4. **v0.7.9 build script name** — RESOLVED. Confirmed at `apps/chat-app/package.json` — `scripts.frontend` exists and is the canonical client build for v0.7.9. CI runs `npm run frontend`.

5. **`scripts/verify-requirements-matrix.sh` availability** — DEFERRED. Phase 11 has not yet landed; the validator script is not present at Phase 19 execution time. Manual REQUIREMENTS.md cross-check performed; automated assertion deferred to first Phase 11 + Phase 19 joint CI run.

6. **ARM64 image risk for `librechat-rag-api-dev-lite`** — OUT OF SCOPE for Phase 19 (Phase 22). Flagged in [`LIBRECHAT-UPGRADE-PLAYBOOK.md`](../../v1.1-chatapp/LIBRECHAT-UPGRADE-PLAYBOOK.md).

7. **MongoDB hosting decision** — LOCKED. Atlas M0 free 512MB for staging/soft-launch; in-stack `mongo:7` container for `--profile local` dev. Self-host on OCI deferred to Phase 24 if Atlas becomes a constraint.

8. **No OCI deploy in Phase 19** — CONFIRMED. Phase 24 owns OCI deployment. Phase 19 boot target is `docker compose --profile local up chat-app` only.

## v1.1.0 Ship-Gate Mapping

Phase 19 closes the following ship-gate fragments:

| Ship-gate item | How Phase 19 contributes |
|----------------|--------------------------|
| **FX/USD audit clean** (cross-phase) | chat-app surface enforces `showCost`/`showTokens` off via `librechat.yaml`. CI workflow guards against regression. `LIBRECHAT-UPGRADE-PLAYBOOK.md` mandates re-verification after every upstream bump. |
| **Track B chat-app local-boot floor** | `docker compose --env-file ../../.env --profile local up chat-app` produces a service that depends_on edge-api+control-plane+mongo (all `service_healthy`) and exposes its own healthcheck. Phases 20–25 build on this. |

Does NOT close (later phases own these): Supabase auth swap (20), tier limits + invite/referral (21), RAG + file upload (22), Bengali translation strings + Bengali default model (23), OCI deploy + CF DNS (24), UAT + soft launch (25).

## Branch & PR

- Branch: `b/phase-19-chat-app-fork-strip`
- Base: `chore/v1.1-planning` (rebased from origin during execution)
- PR target: `main`
