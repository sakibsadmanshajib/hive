# Carl.sh — Sovereign Workspace Leadership Charter

> ## Amendment 2026-07-07
>
> This block was added on 2026-07-07 and governs on any conflict with the historical charter text below. The original text is preserved for the record and retains its historical naming; the historical sections are not rewritten.
>
> **Product naming.** The product is now **Hive** (cloud, us-hosted) and **Hive Enterprise** (customer-hosted). The "Carl" and "Carl.sh" names are retired. The one-command installer remains `scripts/install.sh` and carries no product branding. All "Carl" and "Carl.sh" naming below is historical.
>
> **Sovereignty posture.** The locked decision "zero external API keys at the edge, `install --sovereign` refuses external provider keys" (decision 2 plus the Meeting 1 sovereign egress hardening) is superseded by three inference postures: cloud (external providers); enterprise default (local models, where an admin may knowingly opt in to add external provider keys); and strict-sovereign lockdown (the `HIVE_SOVEREIGN` guard from #245, now an optional toggle for buyers who need a provable air gap, rather than an always-on default). The Grok and free-model test-only rule (decision 8) is unchanged: test-time conveniences, never shipped runtime dependencies.
>
> **Tenancy.** A single organization is a single tenant. Departments are separated by RBAC inside that one tenant, not by additional tenants. The multi-tenant schema exists for extensibility only.
>
> **Chat client.** Open WebUI is the v1 chat client, per the PR #291 decision, which supersedes the Meeting 1 choice of Lobe Chat recorded below. PR #291 was open at the time of this amendment.

Initiative: a single one-command tool (`Carl.sh`) that stands up a fully self-hosted AI workspace
(a co-work ChatGPT with RAG) on a target machine, exposing OpenAI-compatible and
Anthropic-compatible APIs over the existing Hive control plane. Built by gap-closing the existing
`fundmoreai/hive` repository, not greenfield. The product is a sovereign alternative to OpenAI and
Anthropic: it speaks both API dialects but runs entirely on the customer's own server with no
external SaaS dependency and no external API keys.

Established 2026-06-25 by the orchestrator acting as the leadership team, per owner directive
"You are the leadership team, create any roles you need." Owner retains veto on every decision.

## Roles and responsibilities

| Role | Held by | Mandate |
|------|---------|---------|
| CEO / Orchestrator | main agent (this thread) | Vision, segment strategy, go/no-go, resource asks, merge calls, ledger and memory upkeep. Never writes product code. |
| CTO | `architect` agent + orchestrator synthesis | Architecture review and design of the deltas, technology selection validated against live sources, design docs. |
| Head of Product | `planner` agent | Requirements traceability, GitHub board and issues, phase slicing, roadmap. |
| CMO | `ecc:marketing-agent` | Positioning for the sovereign pivot, two marketing sites already live, regulatory talking points. |
| Security & Compliance Lead | `security-reviewer` agent | Data sovereignty, US CLOUD Act avoidance wedge, secrets, audit logging, all auth and money paths. |
| Build Leads | `go-reviewer` / `typescript-reviewer` / `database-reviewer` + builder agents | Edge-api and control-plane (Go), workspace and console (TS), PGVector and migrations (SQL). |
| QA / E2E | `e2e-runner` agent | End-to-end verification of installer, RAG, voice, relay, co-work flows before any ship claim. |

Each role is realized as a dispatched subagent with an explicit library `subagent_type`. The
orchestrator only coordinates, reviews pushed diffs, and keeps memory. Independent reviewer per PR.

## Locked decisions (2026-06-25, revised after owner clarification)

1. Scope: gap-close on the existing `fundmoreai/hive` repo. The enterprise edge box is the primary
   product. The cloud platform is a demo for now, improved later.
2. Fully self-hosted. Zero external SaaS dependency and zero external API keys at the edge. The
   product is a sovereign alternative to OpenAI and Anthropic.
3. API surfaces: OpenAI-compatible (already shipped) plus an Anthropic-compatible `/v1/messages`
   surface that translates to our own local and open models (issue #168). We never call real
   Anthropic. The Anthropic API key idea is dropped.
4. Relay: self-hosted only. Default is LAN serve via the box's own Caddy. Remote access without
   opening firewall ports is via self-hosted WireGuard, with Headscale as an optional coordination
   server. No Tailscale SaaS and no external relay keys.
5. Voice and STT: NVIDIA Parakeet, self-hosted. Owner provides a Parakeet host or download.
6. Embeddings and RAG: PGVector on Postgres (Supabase provides the extension) plus local
   embeddings. Add document upload and vector search endpoints to edge-api.
7. Cloud co-work workspace: build first as a self-contained isolated container component. Explore
   online sandbox services (Daytona or similar) later. The OpenCode coding agent is deferred and is
   not the main target.
8. Testing only: Grok (xAI) as a test LLM and free OpenAI-compatible local models. These are
   test-time conveniences, never shipped runtime dependencies.

## Gap priority (edge first)

1. Anthropic-compatible `/v1/messages` surface (#168). Completes the dual-dialect promise.
2. RAG document upload and vector search endpoints on edge-api.
3. Parakeet voice and STT integration, exposed as an OpenAI-compatible audio endpoint.
4. Self-hosted relay (WireGuard, optional Headscale) for port-less edge remote access.
5. Containerized co-work workspace (demo), after the edge core is solid.

## Resources provided by owner

Parakeet voice-model host or download, free local OpenAI-compatible models, Grok for testing.
Not provided and not needed: Tailscale key, Daytona account, Anthropic API key.

## Open clarifications (CTO defaults stand until owner vetoes)

- Lead segment: finance and legal in Canada / Ontario (WEtech Alliance, OSFI B-10, Quebec Law 25).
- First runnable milestone: edge box installs via Carl.sh and serves both API dialects against a
  local model selected by the hardware advisor, with RAG upload and query working.

## Meeting 1 outcomes (2026-06-25)

Leadership team aligned on the following binding decisions:

### Web client and user-facing shell
The shipped web client shell is Lobe Chat (MIT license). Open WebUI is dropped as the standard
shipped shell due to a post-2025 branding retention clause that creates resale risk for regulated
government buyers. Lobe Chat is MIT-licensed, open-source, and has no external branding obligations.

### Edge data plane and storage
The self-hosted edge data plane is a Supabase stack (Postgres with pgvector extension, GoTrue
auth, Storage on local filesystem). MinIO is rejected due to AGPL licensing; Supabase Storage
(S3-compatible) backed by the edge box's own filesystem is the standard object storage backend.
All data stays on-premise with zero external storage dependency.

### Tenant feature flags and revocation
Tenant feature flags (RAG enabled, voice enabled, relay enabled, cowork enabled) are NOT
embedded in the JWT. Instead, they resolve lazily at the edge through a feature gate middleware
with a 30-second cache, enabling feature revocation to take effect in under 60 seconds without
redeployment. Issue #238 defines the gate.go middleware and control-plane settings endpoint.

### SSO and Active Directory procurement blocker
Single Sign-On and Active Directory integration (SAML, OIDC, LDAP) are promoted to v1 MUST status.
Regulated buyers in finance and legal (OSFI B-10, Quebec Law 25) require enterprise authentication
to be deployable. This was blocking every pilot discussion. Issue #237 covers enterprise auth.

### Sovereign egress hardening
The edge stack hardens egress to ensure no telemetry or audit leaks to external providers:

- LiteLLM telemetry is disabled globally.
- All external audit sinks (if any) are gated per tenant, controlled via control-plane settings.
- A new `install --sovereign` flag in Carl.sh refuses any external LLM provider keys (OpenRouter,
  Groq, etc.) and fails if attempted. Operator must provide a local model via Ollama or equivalent.

### Audit taxonomy and regulatory compliance
RAG retrieval must emit a `RAG_CHUNK_RETRIEVED` audit event per chunk returned to the model,
as required by Quebec Law 25 and PHIPA audit trails. Issue #239 extends the audit schema with
new event types: `LLM_RESPONSE`, `RAG_DOCUMENT_UPLOAD`, `RAG_DOCUMENT_DELETE`, `RAG_SEARCH`,
`RAG_CHUNK_RETRIEVED`, `FILE_ACCESS`, `DATA_SUBJECT_REQUEST`. Issue #241 adds a cron job to
archive audit logs older than 90 days to cold storage with 10-year retention per PHIPA.

### Pre-ship license clearance gate
Every shipped dependency must pass a pre-release license clearance check. Issue #242 defines an
SBOM generation and verification workflow that blocks any release containing AGPL or GPL
dependencies. This ensures customers can deploy Carl.sh without open-source licensing obligations
that would conflict with selling the edge as a proprietary product or integrating it into closed
systems.

### Client phasing recommendation (pending owner ratification)
Based on the feature set and shipping timeline:

- **Ship in v1 (Wave 1–2)**: Full-featured web console (Lobe Chat), thin Word plugin.
- **Defer to v1.x**: Mobile apps, LibreOffice/Google Docs integrations, desktop client, multi-user cowork workspace.

Rationale: the web client covers the largest user population and enables all core features (chat,
RAG, voice, relay). Mobile and offline integrations are valuable but not blocking the first
regulated deployment. Cowork (multi-user collaboration) is a nice-to-have after the core sovereign
edge product is solid and proven in the field.

### New GitHub issues tracking decisions
Five new issues created, labeled `carl` and added to milestone 7 (Carl.sh edge-first v1) and the
Hive Roadmap board:

- **#237**: SSO and Active Directory enterprise auth (SAML/OIDC/LDAP), Wave 2.
- **#238**: Feature gate enforcement middleware (per-tenant flags at the edge), Wave 1.
- **#239**: Audit taxonomy extension for inference, RAG, and data subject events, Wave 1.
- **#241**: Audit retention and cold archive cron (PHIPA 10 year, Law 25), Wave 2.
- **#242**: Pre-ship license clearance gate (verified SBOM, block AGPL and GPL), Wave 3 release gate.

Issue #232 (RAG) updated with new dependencies on #238 and #239, and now includes requirement
to emit `RAG_CHUNK_RETRIEVED` and use bge-m3 1024-dim embeddings with tenant RLS on the schema.
