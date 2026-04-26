---
upstream: danny-avila/LibreChat
pinned_tag: v0.7.9
commit_sha: bef5c26bed4e5053bdf1936d6ded2c77035c6ee5
forked_at: 2026-04-25
forked_by: gsd executor (Phase 19)
rationale: |
  Pre-Admin-Panel release — smaller strip surface than v0.8.x.
  License plain MIT (no LobeHub-style §1.b commercial clause).
  v0.8.5-rc1 GA imminent at fork date but deferred for v1.1 stability.
upstream_compat:
  next_admin_panel_release: v0.8.0+ (deferred to v1.2 evaluation)
locale_resolution:
  bn_bd_locale_upstream: scaffolded_from_en
  bengali_codes_upstream: none (no `bn`, no `bn-IN`, no `bn-BD` shipped at v0.7.9)
  scaffold_source: client/src/locales/en/translation.json
  english_codes_upstream: en (NOT en-US — Hive picker uses canonical `en-US` alias for clarity but resource keyed under `en`)
  phase_23_owns_translations: true
healthcheck:
  path: /health
  upstream_route: apps/chat-app/api/server/index.js:54 — `app.get('/health', (_req, res) => res.status(200).send('OK'));`
  note: NOT `/api/health` (that path was a v0.8.x change; v0.7.9 mounts plain `/health`).
build_scripts:
  client_build_root: npm run frontend
  client_build_inside_client_pkg: npm run build
  api_start: node api/server/index.js
  lint: npm run lint
  format: npm run format
fx_zero_leak_keys:
  applied:
    - interface.showCost: false
    - interface.showTokens: false
  upstream_default_behavior: |
    v0.7.9 librechat.example.yaml does NOT ship `showCost` / `showTokens` keys.
    The default UI does not render per-message USD cost or token-cost metadata to end-users.
    Hive sets these keys defensively (forward-compat with v0.8.x where token-cost UI was promoted).
---

# LibreChat upstream version pin

Hive chat-app (`apps/chat-app/`) is a vendored fork of LibreChat at the tag + SHA above.

Upgrade procedure: see [`LIBRECHAT-UPGRADE-PLAYBOOK.md`](./LIBRECHAT-UPGRADE-PLAYBOOK.md).

## Fork procedure used (recorded for reproducibility)

```bash
git clone --branch v0.7.9 --depth 1 https://github.com/danny-avila/LibreChat.git apps/chat-app
cd apps/chat-app && git rev-parse HEAD     # bef5c26bed4e5053bdf1936d6ded2c77035c6ee5
rm -rf .git                                 # detach upstream history
cd ../..
# Bengali skeleton from English source
cp -r apps/chat-app/client/src/locales/en apps/chat-app/client/src/locales/bn-BD
```

## Verification at fork time

| Probe | Expected | Actual |
|------|----------|--------|
| `apps/chat-app/package.json` `scripts.frontend` | exists | yes |
| `apps/chat-app/api/server/index.js` health route | `/health` GET 200 OK | `/health` (line 54) |
| `apps/chat-app/client/src/locales/` | wide i18n | 32 codes, NO Bengali |
| `apps/chat-app/client/src/locales/i18n.ts` `resources` map | uses bare `en` (NOT `en-US`) | confirmed |
| `apps/chat-app/librechat.example.yaml` `interface` block | exists, no `showCost`/`showTokens` keys | confirmed; Hive adds keys defensively |
| Upstream LICENSE | plain MIT | preserved at `apps/chat-app/LICENSE` |
