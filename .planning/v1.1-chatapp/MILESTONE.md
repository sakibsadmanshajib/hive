---
milestone: v1.1
status: planning
created: 2026-04-25
revised: 2026-05-17 (v4 - Open WebUI pivot; Phase 26 web search added)
scope: dev-api-hardening + Hive Chat product wedge
geo: BD soft launch
locales: bn-BD (default), en-US
---

# v1.1 - Hive (Hardened API + Hive Chat)

Single milestone, two tracks shipped together.

- **Track A - API Hardening:** rate limiting, payments, verification, RBAC, and regulatory compliance.
- **Track B - Hive Chat:** Open WebUI-based chat on Hive API, personal RAG through OWUI, shared tenant KBs through Hive, admin pages in web-console, and optional v1.1 web search through SearXNG.

## Decisions Locked (v4)

| Item | Choice |
|------|--------|
| Chat base | **Open WebUI upstream image**, pinned by digest. No fork. |
| License/branding gate | Current Open WebUI releases carry branding-preservation terms. Phase 19 must preserve Open WebUI branding, use a BSD-3-Clause-only pin, or get written permission before full Hive rebranding. |
| Customisation | Environment, Supabase OIDC, OWUI pipeline filters, Caddy path blocking, and Hive Go services. |
| Deprecated base | LibreChat fork plan is superseded; active files are pointers/decisions and legacy details live in `.DEPRECATED.md` files. |
| Free tier | No auto-grant on signup. Verified users get bounded free usage. Credits are issued through tenant/admin controls and Phase 21 rules. |
| Anti-abuse | Verification, captcha on signup, Redis IP limits, tenant quotas, and audit log. No device fingerprinting in v1.1. |
| Milestone | Folded into v1.1 as a single two-track milestone. |
| Geo | BD soft launch; bn-BD default with en-US fallback. |
| Chat hosting | Open WebUI runs in Docker locally and in EnterpriseEdge packaging; Hive Cloud deployment is finalised in Phase 25. |
| New infra | Open WebUI data volume, Supabase pgvector, optional SearXNG service if Phase 26 stays in v1.1. No MongoDB fork requirement. |
| Subdomain | TBD. |

## Architecture (Track B)

```
Open WebUI
  -> Supabase OIDC
  -> OWUI pipeline filter forwards JWT
  -> edge-api validates JWT / API key
  -> LiteLLM/provider routes
  -> control-plane for tenant settings, provider catalog, credits, audit, admin APIs
  -> Supabase Postgres + pgvector for tenant data, audit evidence, traces, and RAG metadata
  -> SearXNG internal service for Phase 26 web search
```

## Unified Phase Order

| # | Phase | Track | Depends On |
|---|-------|-------|------------|
| 11 | Compliance, Verification & Artifact Cleanup | A | - |
| 12 | KEY-05 Hot-Path Rate Limiting | A | - |
| 13 | Console Integration Fixes | A | - |
| 14 | Payments, Invoicing & Budget Integration | A | 13 |
| 15 | Batch success-path settlement (local executor) | A | - |
| 16 | `ensureCapabilityColumns` table fix | A | - |
| 17 | FX/USD zero-leak hardening | A | 14 |
| 18 | RBAC + verification-aware authorization | A | 13, 17 |
| 19 | **Foundation slice: tenant settings, identity bridge, Open WebUI, audit** | B | 18 |
| 20 | **Provider catalog + LiteLLM config reload** | B | 19 |
| 21 | **Credit and quota engine** | B | 12, 14, 19 |
| 22 | **Shared tenant knowledge-base RAG** | B | 19, 20, 21 |
| 23 | **Admin console pages** | B | 19, 20, 21, 22 |
| 26 | **Web search tool (SearXNG + `/v1/tools/web_search`)** | B | 12, 14, 19, 20, 21 |
| 24 | **EnterpriseEdge self-host packaging** | B | 19, 20, 21, 22, 23, 26 if included |
| 25 | **Payments tenant-gating + Hive Cloud cutover** | B | 17, 21, 23, 24, 26 if included |

Phase 26 is append-numbered to avoid renumbering existing Phase 23-25 references. If it remains in v1.1 launch scope, execute it after Phase 21 and before Phase 24/25 so packaging and cutover include SearXNG deliberately.

## Parallelizable Work Streams

| Stream | Phases | Owner agent |
|--------|--------|-------------|
| API hardening backend | 11, 12, 15, 16 | go-reviewer + go-build |
| Console + payments | 13, 14, 17, 18 | typescript-reviewer + database-reviewer |
| Open WebUI foundation | 19 | go-reviewer + typescript-reviewer + e2e-runner |
| Provider / credit / RAG | 20, 21, 22 | go-reviewer + database-reviewer + security-reviewer |
| Admin + launch | 23, 24, 25 | typescript-reviewer + e2e-runner |
| Web search addition | 26 | go-reviewer + security-reviewer + e2e-runner |

## Definition of Done

- [ ] Track A complete, with Phase 17 FX/USD zero-leak and Phase 18 RBAC gates still green.
- [ ] Track B complete through the chosen launch scope: Open WebUI chat, tenant-aware auth, provider catalog, credit/quota controls, shared KB RAG, admin pages, packaging, and payment gates.
- [ ] If Phase 26 is included in v1.1, SearXNG is packaged intentionally and `/v1/tools/web_search` is provider-blind, tier-gated, rate-limited, billed in BDT, and covered by SDK/E2E tests.
- [ ] Master E2E, SDK tests, Go tests, and regulatory lint gates pass.
- [ ] Tag `v1.1.0`.

## Out of Scope (v1.2)

- MCP marketplace exposure beyond an optional web-search stub.
- Search verticals beyond general web results.
- Ads tier.
- Voice features.
- South Asia regional rollout.

## Next Step

Finish Phase 19 Plan 03/04 on the Open WebUI foundation path, including the license/branding gate in `LICENSE-DECISION.md`, then plan Phase 20 and Phase 21 before any Phase 26 implementation starts.
