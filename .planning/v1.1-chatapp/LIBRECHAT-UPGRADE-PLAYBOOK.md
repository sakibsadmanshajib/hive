# LibreChat Upgrade Playbook (Hive chat-app)

**Owner:** chat-app maintainers (Phase 19 → Phase 25 + ongoing).
**Vendored at:** `apps/chat-app/` (no nested `.git`).
**Pin:** see [`LIBRECHAT-VERSION.md`](./LIBRECHAT-VERSION.md).

This document is the canonical procedure for tracking upstream `danny-avila/LibreChat` releases, re-applying the Hive strip + FX zero-leak guard, and re-verifying ship-gate compliance.

---

## When to upgrade

Trigger an upgrade evaluation when ANY of:

1. **Security CVE** disclosed against the pinned version (highest priority).
2. **Must-have feature** in upstream (e.g., MCP marketplace UX, RAG improvement, Bengali translation completeness).
3. **Stale ≥6 months** — even without specific need, take a fresh stable point release to reduce upgrade-debt.
4. **Hive provider feature** requires upstream support (e.g., new `endpoints.custom[]` field added in v0.8.x).
5. **Bug fix** — upstream-fixed issue blocking Hive users.

Do NOT upgrade because "latest is newer" alone. Cost = re-strip + re-test + Mongo schema-migration risk.

---

## Pre-upgrade checklist

Before starting any merge work:

- [ ] Read upstream changelog from current pin → target tag. https://www.librechat.ai/changelog
- [ ] Identify provider-block additions in `librechat.example.yaml` (new `endpoints.foo:` keys to strip).
- [ ] Identify schema-key changes in `interface:` block — especially anything cost/token/USD-related.
- [ ] Identify Mongo migrations in `api/models/` and `api/strategies/` directories.
- [ ] Identify changes to `client/src/locales/` (could affect `bn-BD` skeleton compatibility).
- [ ] Identify changes to `api/server/index.js` healthcheck route name (current: `/health` at v0.7.9; v0.8.x may have moved to `/api/health`).
- [ ] Identify changes to top-level npm scripts (`frontend`, `backend`, `lint`).

---

## Upgrade procedure

### 1. Scratch fork

```bash
git clone --branch <target-tag> --depth 1 \
  https://github.com/danny-avila/LibreChat.git /tmp/librechat-<target-tag>
cd /tmp/librechat-<target-tag>
git rev-parse HEAD                  # capture target SHA
rm -rf .git
```

### 2. Three-way merge into Hive worktree

Strategy: produce a clean diff between scratch fork and `apps/chat-app/`, manually merge non-Hive-overridden files; preserve Hive overrides:

| Path | Treatment |
|------|-----------|
| `apps/chat-app/librechat.yaml` | **Hive-owned** — do NOT take upstream. |
| `apps/chat-app/.env.example` | **Hive-owned** (curated subset). |
| `apps/chat-app/.env.upstream.example` | Refresh from upstream `.env.example` for diff'ing. |
| `apps/chat-app/.gitignore` | Apply upstream changes; preserve Hive `!librechat.yaml` and `!**/.env.upstream.example` overrides. |
| `apps/chat-app/Dockerfile` | Take upstream; re-apply Hive `HEALTHCHECK` directive. |
| `apps/chat-app/client/src/locales/i18n.ts` | Take upstream `resources` map; re-apply Hive `bn-BD` registration. |
| `apps/chat-app/client/src/locales/bn-BD/` | **Hive-owned** — do NOT take upstream unless upstream now ships its own `bn-BD`. If upstream ships `bn-BD`, diff carefully and prefer upstream strings only where Hive's skeleton has not been translated by Phase 23. |
| `apps/chat-app/client/src/App.jsx` | Take upstream; re-apply `<FirstRunLanguageModal />` mount. |
| `apps/chat-app/client/src/hooks/useFirstRunLocale.ts` | **Hive-owned**. |
| `apps/chat-app/client/src/components/Nav/LanguagePicker/` | **Hive-owned**. |
| Everything else | Prefer upstream. |

### 3. Re-apply Hive strip

After merge, walk the produced `librechat.yaml` and confirm:

- Exactly one `endpoints.custom[]` entry (Hive).
- ZERO `endpoints.openAI`, `endpoints.anthropic`, `endpoints.google`, `endpoints.azureOpenAI`, `endpoints.bedrock`, `endpoints.assistants` blocks.
- `interface.showCost: false`.
- `interface.showTokens: false` (or upstream-renamed equivalent — see FX guard re-verification step).
- `endpoints.custom[].apiKey` references `${HIVE_EDGE_API_KEY}`, NOT `user_provided`.

### 4. Rebuild + smoke

```bash
cd deploy/docker
docker compose --env-file ../../.env --profile local build chat-app
docker compose --env-file ../../.env --profile local up -d chat-app mongo edge-api control-plane
sleep 60
docker compose --profile local ps chat-app | grep -q "(healthy)"
curl -fsS http://localhost:3080/health
```

Then run a manual chat probe through the Hive endpoint to confirm the single-provider lock is active end-to-end.

### 5. CI green

Push branch; confirm `chat-app-ci.yml` workflow passes (lint + frontend build + FX zero-leak guard).

### 6. Bump pinned tag

Only after all above pass:

- Update [`LIBRECHAT-VERSION.md`](./LIBRECHAT-VERSION.md) — new `pinned_tag`, new `commit_sha`, new `forked_at`.
- Update `LIBRECHAT-UPGRADE-PLAYBOOK.md` if any healthcheck path / build script / interface key changed.
- Open PR titled `chore(chat-app): upgrade LibreChat <old> → <new>`.

---

## Rollback

If an upgrade introduces a regression:

```bash
git revert <upgrade-merge-sha> --no-edit
docker compose --env-file ../../.env --profile local build --no-cache chat-app
docker compose --env-file ../../.env --profile local up -d chat-app
```

Mongo migrations in LibreChat are forward-only (no down-migrations shipped). If the upstream upgrade introduced a destructive Mongo migration, restore from Atlas backup snapshot taken pre-upgrade. Atlas M0 keeps automatic snapshots; production tier (M2+) keeps PITR.

---

## FX guard re-verification (MANDATORY after every upgrade)

After every upgrade, run BOTH of these checks. They MUST pass:

```bash
# 1. librechat.yaml has the FX zero-leak keys.
grep -E "^  showCost: false$" apps/chat-app/librechat.yaml
grep -E "^  showTokens: false$" apps/chat-app/librechat.yaml

# 2. No customer-visible USD/cost rendering surfaces.
#    If upstream renamed `showCost`/`showTokens` to a new key, find the new key
#    in upstream's `librechat.example.yaml` and update apps/chat-app/librechat.yaml
#    AND this playbook accordingly. The CONTRACT is non-negotiable; key names are.
```

This guard is also enforced by `.github/workflows/chat-app-ci.yml`. CI failure on these grep checks = MUST FIX before merge. Phase 17 mandate; regulatory blocker for the BD market.

---

## Open questions tracked across upgrades

- **MongoDB hosting:** Phase 19 = Atlas M0 free + local mongo container. If 512MB Atlas ceiling is hit, decide self-host on OCI vs. Atlas paid M2+. Re-evaluate annually.
- **bn-BD locale:** Phase 19 scaffold from `en`; Phase 23 owns translations. If a future upstream ships its own `bn-BD`, prefer upstream strings only where Phase 23 has not translated; never silently overwrite Phase 23 work.
- **Admin Panel evaluation (v0.8.0+):** Hive control-plane handles RBAC. Admin Panel is duplicative; defer evaluation to v1.2.

---

## Quick reference

- Pinned-tag history: [`LIBRECHAT-VERSION.md`](./LIBRECHAT-VERSION.md)
- License: MIT (preserved at `apps/chat-app/LICENSE`).
- FX zero-leak audit (Phase 17): [`MILESTONE.md`](./MILESTONE.md), [`V1.1-MASTER-PLAN.md`](./V1.1-MASTER-PLAN.md)
- Phase 19 verification: [`../phases/19-chat-app-fork-strip/19-VERIFICATION.md`](../phases/19-chat-app-fork-strip/19-VERIFICATION.md)
