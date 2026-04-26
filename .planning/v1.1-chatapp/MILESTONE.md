---
milestone: v1.1
status: planning
created: 2026-04-25
revised: 2026-04-25 (v3 — chat-app base pivoted from Lobe Chat to LibreChat per LICENSE-DECISION.md; hosting locked to OCI; auto-trial removed in favor of owner-discretionary credits)
scope: dev-api-hardening + chat-app product wedge (folded)
geo: BD soft launch
locales: bn-BD (default), en-US
---

# v1.1 — Hive (Hardened API + Hive Chat)

Single milestone, two tracks shipped together.

- **Track A — API Hardening** (carry-over from v1.0): rate limiting, payments, verification, RBAC, regulatory compliance.
- **Track B — Hive Chat** (new product wedge): Bengali + English chat-app on Hive API, with file-upload RAG, fork of **LibreChat (MIT)**. **No auto-trial**; owner-discretionary credit grants only.

## Decisions Locked (v3)

| Item | Choice |
|------|--------|
| OSS base | **LibreChat** (MIT — `danny-avila/LibreChat`, pinned tag). Replaces v1's Lobe Chat (rejected: LobeHub Community License §1.b commercial-derivative clause + Cloudflare Workers incompatibility). |
| Free tier | **No auto-grant on signup.** Verified users get a free chat tier with tier-based rate limits (see `V1.1-MASTER-PLAN.md` §Tier Model). Credits are issued only via the owner-discretionary credit grant tool (Phase 14). |
| Anti-abuse | Phone OTP + email verify (already required for verified tier), captcha on signup, Redis IP rate-limit on signup endpoint. No device-fingerprint or IP/device dedupe — that scope was dropped with the auto-trial. |
| Milestone | Folded into v1.1 (single milestone, two tracks) |
| Geo | BD soft launch — bn-BD default + en-US fallback |
| Chat-app hosting | **Oracle Cloud Infrastructure (OCI) container instance(s)** with Cloudflare DNS/TLS in front. Cloudflare Workers is **NOT** a chat-app deploy target (LibreChat is Vite + Express + WebSocket — not Workers compatible). Web-console hosting on Workers is unchanged. |
| New infra | **MongoDB** (LibreChat is Mongo-native — Atlas M0 vs OCI self-host decided in Phase 19). Postgres + Supabase Storage + pgvector retained. |
| Subdomain | TBD (suggested: `chat.hive.bd`) |

## Architecture (Track B)

```
hive/apps/chat-app                ← new, fork of LibreChat (pinned tag)
  ├── auth: Supabase (shared with web-console SSO)
  ├── default OpenAI-compatible provider → hive edge-api (http://edge-api:8080/v1)
  ├── primary DB: MongoDB (LibreChat-native, Atlas or OCI self-host)
  ├── secondary DB: Postgres (chat_app schema in same Supabase project) for tier/credit/referral state
  ├── files: Supabase Storage (hive-files bucket, reused) — verified-tier+ only
  ├── embeddings: hive edge-api /v1/embeddings → Supabase pgvector (HNSW index)
  ├── billing: per-message metering via existing prepaid ledger (credited tier only)
  ├── deploy: OCI container instance, CF DNS/TLS in front
  └── i18n: bn-BD primary, en-US secondary, first-run language picker
```

New infra: **MongoDB** (LibreChat persistence) and **OCI compute** (chat-app host).
Reused: Supabase Postgres + Storage + pgvector, Hive edge-api, control-plane, Cloudflare DNS/TLS.

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
| 19 | **chat-app: fork-and-strip LibreChat (pinned tag) + first-run language picker** | B | — |
| 20 | **chat-app: auth-bridge (Supabase SSO) + tier resolution** | B | 18, 19 |
| 21 | **chat-app: tier limits + invite/referral system** (no auto-trial; owner-discretionary credits only) | B | 14, 20 |
| 22 | **chat-app: file-upload RAG (pgvector)** | B | 19 |
| 23 | **chat-app: bn-BD + en-US i18n + Bengali model defaults** | B | 19 |
| 24 | **chat-app: deploy staging on OCI** (CF DNS/TLS in front; Workers NOT a target) | B | 19–23 |
| 25 | **chat-app: UAT + soft launch** | B | 24, 12 (rate limit live), 17 (FX audit clean) |

Phases 11/12/16/19 can run in **parallel** (no shared deps). Phases 22/23 parallel after 19. Critical path: 13 → 14 → 20 → 21 → 24 → 25.

## Parallelizable Work Streams

| Stream | Phases | Owner agent |
|--------|--------|-------------|
| API hardening backend | 11, 12, 15, 16 | go-reviewer + go-build |
| Console + payments | 13, 14, 17, 18 | typescript-reviewer + database-reviewer |
| Chat-app fork + features | 19, 22, 23 | typescript-reviewer + frontend-design |
| Chat-app integration | 20, 21 | typescript-reviewer + security-reviewer |
| Chat-app launch | 24, 25 | e2e-runner + loop-operator |

## Anti-Abuse (auto-trial removed)

Auto-grant on signup was rejected in v3 (see `V1.1-MASTER-PLAN.md` §v2 Revisions). Without an automatic credit grant, the abuse surface collapses to bot-signup throttling and tier integrity:

1. **Phone OTP** — Supabase Phone Auth (BD numbers); required for `verified` tier.
2. **Email verify** — required for `verified` tier.
3. **Captcha on signup** — hCaptcha or Cloudflare Turnstile.
4. **Redis IP rate limit on signup endpoint** — throttle bot signups (existing infra).
5. **Owner-grant audit log** — every credit grant is logged with `granted_by_user_id`, amount, timestamp, optional note (Phase 14). Non-owner accounts cannot grant.

Device fingerprint and manual review queue from v1's plan are **dropped** with the auto-trial.

## Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| LibreChat upstream drift | Pin to fork-time tag; minimal patches; documented upgrade procedure (`LIBRECHAT-UPGRADE-PLAYBOOK.md`); 3-way-merge protocol re-verifies the FX-leak guard each upgrade. |
| Bengali tokenization cost | Accepted (dropped as a v1.1 risk per v3 plan). |
| MongoDB ops complexity | Atlas M0 free tier for MVP; OCI self-host evaluated in Phase 19 if Atlas constraints hit. |
| OCI quota / image build / multi-arch (ARM64) | Phase 19/24 validate `VM.Standard.A1.Flex` ARM shape and image platform target. |
| FX leak (regulatory) | Phase 17 zero-tolerance audit gates v1.1.0; chat-app re-audited in Phase 25 + CI grep guard. |
| Bot signups | Captcha + Redis IP rate-limit on signup endpoint (no auto-credit means low payoff for bots). |

## Definition of Done (v1.1 ships when)

- [ ] Track A: phases 11–18 complete, regulatory fixes live, FX/USD audit clean (Phase 17), RBAC + tier enforced.
- [ ] Track B: chat-app deployed at `chat.hive.bd` (OCI host, CF DNS/TLS in front), Bengali + English UX with first-run picker, RAG works on PDF/DOCX/TXT/MD, four-tier model live (guest / unverified / verified / credited), invite/referral system live, owner-discretionary credit grant tool live, BDT-only billing display.
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
- **Chat track:** Phase 19 LibreChat fork-and-strip executed on branch `b/phase-19-chat-app-fork-strip` (PR #131). See `.planning/phases/19-chat-app-fork-strip/RESEARCH-LIBRECHAT.md` and `PLAN.md` for the active execution artifacts.

Both tracks proceed concurrently. User reviews phase plans before each execution.
