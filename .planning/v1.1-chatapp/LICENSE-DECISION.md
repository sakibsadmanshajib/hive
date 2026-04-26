---
decision_date: 2026-04-25
decision_owner: Sakib (project owner)
phase_blocking: 19
status: locked
---

# Chat-App Base — License & Hosting Decision

## Context

Phase 19 research (`.planning/phases/19-chat-app-fork-strip/RESEARCH.md`, 2026-04-25) surfaced two blockers in the original Lobe Chat plan:

1. **Lobe Chat license is not plain Apache-2.0.** It is the *LobeHub Community License* (Apache-2.0 + §1.b commercial-derivative restriction). A Hive-rebranded fork is a derivative work and would require a paid commercial license from LobeHub LLC.
2. **Lobe Chat is not Cloudflare Workers compatible.** Upstream maintainer confirmed (issue #4241). Node-only dependencies (`sharp`, `pg`, `ws`, `pdf-parse`, `mammoth`) block Workers deployment without invasive refactor.

## Decision

### Base — LibreChat (MIT)

- License: **MIT** — clean derivative-work permissions; no commercial-license negotiation needed.
- Stack: Node.js + MongoDB (chat history) + optional Python RAG API (`librechat-rag-api`).
- Features included upstream: file upload, RAG, MCP server support, agents, plugins, multi-LLM provider, multilingual i18n, code interpreter, web search plugin.
- For Hive: lock to single OpenAI-compatible provider pointed at `http://edge-api:8080`.

This supersedes the v2 master plan choice of Lobe Chat. The Lobe research artifacts (`.planning/phases/19-chat-app-fork-strip/RESEARCH.md`) remain on disk as a record of the rejected alternative.

### Hosting — OCI containers + Cloudflare Workers (unchanged web-console)

- **chat-app** → Oracle Cloud Infrastructure (OCI) container instance(s). Node runtime accommodates LibreChat's Node + Mongo + Python RAG API stack natively. Workers compatibility is not pursued for chat-app.
- **web-console** → Cloudflare Workers via `@opennextjs/cloudflare` (unchanged). Existing Hive pattern preserved.
- DNS / TLS via Cloudflare in front of OCI for chat-app subdomain.

### Infra delta

- New: MongoDB (managed — Mongo Atlas free tier or self-hosted on OCI). Required by LibreChat for chat history + agent definitions.
- New (optional): `librechat-rag-api` Python service if Phase 22 uses LibreChat RAG. Alternative is to wire LibreChat file uploads directly to Hive `/v1/embeddings` + Postgres pgvector via custom adapter.
- Unchanged: Hive Postgres (Supabase), Redis, edge-api, control-plane, web-console hosting.

## Consequences for the master plan

All Track B phases (19–25) re-ground on LibreChat. Phase summaries change:

| Phase | Lobe-flavored scope (rejected) | LibreChat-flavored scope (locked) |
|-------|--------------------------------|-----------------------------------|
| 19 | Fork Lobe v1.143.3 + strip 68 providers + 21 Drizzle schemas | Fork LibreChat at pinned tag + lock to single Hive provider + add `bn-BD` locale + first-run language picker |
| 20 | Replace NextAuth+Clerk with Supabase | Replace LibreChat auth (Passport/JWT) with Supabase; map Mongo `users` ↔ Supabase users |
| 21 | Tier limits + invite/referral | Same. Tier resolution against Supabase verification state. |
| 22 | Lobe knowledge base → pgvector | Pick: LibreChat RAG API (Python sidecar on OCI) OR custom file-upload route → Hive embeddings → pgvector. |
| 23 | i18n bn-BD + en-US | Verify LibreChat `bn` locale present (LibreChat has wide i18n coverage). Fill gaps. |
| 24 | Deploy on Workers via OpenNext | Deploy on OCI container (Docker). Provision DNS + TLS via Cloudflare. |
| 25 | UAT + soft launch | Same. |

## Open questions parked here

1. RAG architecture for Phase 22 — LibreChat RAG API sidecar (Python, on OCI) versus custom adapter direct to Hive embeddings + pgvector. Decided in Phase 22 PLAN.md.
2. MongoDB hosting: Atlas free tier (managed, easier) vs self-hosted on OCI (cheaper, more ops). Decided in Phase 19 PLAN.md.
3. LibreChat pinned tag — TBD in re-research.
