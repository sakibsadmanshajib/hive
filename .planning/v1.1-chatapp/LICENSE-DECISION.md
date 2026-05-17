---
decision_date: 2026-05-17
decision_owner: Sakib (project owner)
phase_blocking: 19
status: resolved
supersedes:
  - .planning/v1.1-chatapp/LICENSE-DECISION.DEPRECATED.md
---

# Chat-App Base — Open WebUI License Risk Note (Decision Resolved)

## Context

The LibreChat fork plan is deprecated. The active Track B plan consumes Open WebUI as an upstream container image and customises it through env, Supabase OIDC, pipeline filters, and Caddy.

Open WebUI's current published license is no longer plain BSD-3-Clause for all current code. The official docs state that code up to and including v0.6.5 was BSD-3-Clause, while v0.6.6+ adds branding-preservation terms. The current GitHub license includes those additional branding clauses.

Sources:

- https://docs.openwebui.com/license
- https://github.com/open-webui/open-webui/blob/main/LICENSE
- https://github.com/open-webui/open-webui/blob/main/LICENSE_NOTICE

## Decision (Resolved — 2026-05-17)

Phase 19 Plan 03 ships the latest Open WebUI image with the chat surface rebranded to **Hive**:

- Use the latest Open WebUI image, pinned by sha256 digest in `deploy/docker/.image-locks.yml`.
- Set `WEBUI_NAME=Hive` and override visible UI/about/footer surfaces with Hive branding.
- The upstream Open WebUI license branding-preservation terms are **risk-accepted by the project owner**. This is a documented business decision, not a compliance position. The risk is logged here so subsequent reviewers can re-open the question with counsel if circumstances change (commercial rollout, partnership, or upstream license action).

Paths explicitly considered and rejected:

- BSD-3-Clause-only pin (`v0.6.5`) — rejected; the older feature surface costs more than the license risk is worth at v1.1 scope.
- Full upstream-branded deploy ("Open WebUI for Hive") — rejected; the product is launched as Hive.
- Alternative chat base — out of scope for v1.1.

## Consequences

- The v4 master plan keeps the latest Open WebUI image; the image pin is no longer license-gated, but every Open WebUI image bump remains a deliberate PR.
- `WEBUI_NAME`, footer/about surfaces, and OWUI pipeline strings ship as `Hive` in Phase 19 Plan 03 and onward.
- Caddy/admin stripping and provider configuration are unchanged; this decision only affects branding strings, not runtime integration.
- The deprecated LibreChat files stay on disk only as rejected historical context.
- If upstream Open WebUI license posture changes, or commercial/partner channels expand, re-open this file rather than silently flipping the branding back.
