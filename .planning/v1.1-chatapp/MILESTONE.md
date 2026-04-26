---
milestone: v1.1
status: planning
created: 2026-04-25
scope: dev-api-hardening + chat-app product wedge (folded)
geo: BD soft launch
locales: bn-BD (default), en-US
---

# v1.1 — Hive (Hardened API + Hive Chat)

Single milestone, two tracks shipped together.

- **Track A — API Hardening** (carry-over from v1.0): rate limiting, payments, verification, RBAC, regulatory compliance.
- **Track B — Hive Chat** (new product wedge): Bengali + English chat-app on Hive API, with file-upload RAG, fork of Lobe Chat. 50 BDT trial credits on signup.

## Decisions Locked

| Item | Choice |
|------|--------|
| OSS base | **Lobe Chat** (Next.js 15 + Postgres + Drizzle, Apache-2.0) |
| Milestone | Folded into v1.1 (single milestone, two tracks) |
| Free tier | 50 BDT trial credits on signup + anti-abuse (phone OTP, email verify, IP/device dedupe) |
| Geo | BD soft launch — bn-BD default + en-US fallback |
| Subdomain | TBD (suggested: `chat.hive.bd`) |

## Architecture (Track B)

```
hive/apps/chat-app                ← new, fork of Lobe Chat
  ├── auth: Supabase (shared with web-console SSO)
  ├── default OpenAI provider → hive edge-api
  ├── DB: Postgres (new schema in same Supabase project)
  ├── files: Supabase Storage (hive-files bucket, reused)
  ├── embeddings: hive edge-api /v1/embeddings → pgvector
  ├── billing: per-message metering via existing prepaid ledger
  └── i18n: bn-BD primary, en-US secondary, browser-detected
```

No new infra. Reuses Supabase + Hive edge-api + control-plane.

## Unified Phase Order

Chat-app depends on hardening Phase 12 (rate limit), 13 (console), 14 (payments). Order interleaves so Track B can start as soon as its dependencies clear.

| # | Phase | Track | Depends On |
|---|-------|-------|-----------|
| 11 | Compliance, Verification & Artifact Cleanup | A | — |
| 12 | KEY-05 Hot-Path Rate Limiting | A | — |
| 13 | Console Integration Fixes | A | — |
| 14 | Payments, Invoicing & Budget Integration | A | 13 |
| 15 | Batch success-path settlement (local executor) | A | — |
| 16 | `ensureCapabilityColumns` table fix | A | — |
| 17 | `amount_usd` BD checkout regulatory fix | A | 14 |
| 18 | RBAC + verification-aware authorization | A | 13 |
| 19 | **chat-app: fork-and-strip Lobe Chat** | B | — |
| 20 | **chat-app: auth-bridge (Supabase SSO)** | B | 18, 19 |
| 21 | **chat-app: billing-meter + 50 BDT trial credits + anti-abuse** | B | 14, 20 |
| 22 | **chat-app: file-upload RAG (pgvector)** | B | 19 |
| 23 | **chat-app: bn-BD + en-US i18n + Bengali model defaults** | B | 19 |
| 24 | **chat-app: deploy staging (Workers or Fly.io decision)** | B | 19–23 |
| 25 | **chat-app: UAT + soft launch** | B | 24, 12 (rate limit live) |

Phases 11/12/16/19 can run in **parallel** (no shared deps). Phases 22/23 parallel after 19. Critical path: 13 → 14 → 20 → 21 → 24 → 25.

## Parallelizable Work Streams

| Stream | Phases | Owner agent |
|--------|--------|-------------|
| API hardening backend | 11, 12, 15, 16 | go-reviewer + go-build |
| Console + payments | 13, 14, 17, 18 | typescript-reviewer + database-reviewer |
| Chat-app fork + features | 19, 22, 23 | typescript-reviewer + frontend-design |
| Chat-app integration | 20, 21 | typescript-reviewer + security-reviewer |
| Chat-app launch | 24, 25 | e2e-runner + loop-operator |

## Anti-Abuse for 50 BDT Trial

Layered defense (Supabase has all primitives):

1. **Phone OTP** — Supabase Phone Auth (BD numbers)
2. **Email verify** — required before credit grant
3. **IP rate limit** — max 1 trial per IP per 24h (Redis)
4. **Device fingerprint** — FingerprintJS open-source
5. **Manual review queue** — flagged signups held for review

## Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| Lobe upstream drift | Minimal patches, contribute generic fixes upstream |
| pgvector cold-start | HNSW index migration in phase 22 |
| Bengali tokenization (~3x English tokens) | Default to Gemini Flash / Qwen 2.5 (strong bn) |
| Workers cold-start for Lobe | Phase 24 evaluates Workers vs Fly.io |
| FX leak (regulatory) | Phase 17 + chat-app launch audit |
| Trial-credit abuse | 5-layer anti-abuse stack |

## Definition of Done (v1.1 ships when)

- [ ] Track A: phases 11–18 complete, regulatory fixes live, RBAC enforced.
- [ ] Track B: chat-app deployed at `chat.hive.bd`, Bengali + English UX, RAG works on PDF/DOCX/TXT/MD, 50 BDT trial flow live with anti-abuse, BDT-only billing display.
- [ ] E2E suite green for both tracks.
- [ ] Tag `v1.1.0`.

## Out of Scope (deferred to v1.2)

- MCP marketplace exposure
- Projects (RAG collections per user)
- Web search plugins (Tavily / SearXNG)
- Ads tier (banner + sponsored suggestions)
- Company-hosted private data sources
- Voice (STT/TTS) and image gen in chat
- Multi-tenant private deployments
- South Asia regional rollout

## Next Step

If approved, start in parallel:
- **Hardening track:** spawn `go-build-resolver` + `go-reviewer` agents on phases 11, 12, 16.
- **Chat track:** spawn `Explore` agent to audit Lobe Chat fork target + plan phase 19 in detail.

Both tracks proceed concurrently. User reviews phase plans before each execution.
