---
decision_date: 2026-05-17
decision_owner: Sakib (project owner)
phase_blocking: 19
status: active-risk-gate
supersedes:
  - .planning/v1.1-chatapp/LICENSE-DECISION.DEPRECATED.md
---

# Chat-App Base - Open WebUI License and Branding Decision

## Context

The LibreChat fork plan is deprecated. The active Track B plan consumes Open WebUI as an upstream container image and customises it through env, Supabase OIDC, pipeline filters, and Caddy.

Open WebUI's current published license is no longer plain BSD-3-Clause for all current code. The official docs state that code up to and including v0.6.5 was BSD-3-Clause, while v0.6.6+ adds branding-preservation terms. The current GitHub license includes those additional branding clauses.

Sources:

- https://docs.openwebui.com/license
- https://github.com/open-webui/open-webui/blob/main/LICENSE
- https://github.com/open-webui/open-webui/blob/main/LICENSE_NOTICE

## Decision

Phase 19 Plan 03 must choose one of these paths before pinning the Open WebUI image digest:

1. **Default path:** use a current Open WebUI image and preserve visible Open WebUI branding in the UI, docs, and about/footer surfaces. Hive branding may be adjacent/subordinate, not a replacement.
2. **Full-rebrand path:** pin or fork from the last BSD-3-Clause-only point (`v0.6.5`) and accept the older feature surface, or obtain written permission/commercial terms for branding changes.
3. **Alternative path:** reject Open WebUI and re-open the chat-base decision.

Until that decision is made, do not set `WEBUI_NAME` or related UI branding to `Hive Chat` in executable config. Use `Open WebUI for Hive` or leave upstream defaults during local testing.

## Consequences

- The v4 master plan keeps Open WebUI as the active base, but the image pin is license-gated.
- Caddy/admin stripping and provider configuration are still valid; the issue is branding, not runtime integration.
- The deprecated LibreChat files stay on disk only as rejected historical context.
