---
phase: 26-web-search-tool
plan: 01
type: plan-scaffold
wave: 1
status: drafted-2026-05-17
depends_on:
  - phase: 12-key05-rate-limiting
  - phase: 14-payments-invoicing-budget-integration
  - phase: 19-foundation-slice       # Open WebUI compose, JWT/API-key selector, audit primitive
  - phase: 20-provider-catalog       # model/tool catalog coherence with LiteLLM reloads
  - phase: 21-credit-quota-engine    # tier, quota, and BDT billing primitives
branch: b/phase-26-web-search-tool
milestone: v1.1
track: B
execution_slot: after Phase 21, before Phase 24/25 if included in v1.1 launch scope
files_modified:
  - services/searxng/                                # NEW — SearXNG config + settings.yml
  - deploy/docker/docker-compose.yml                 # activate searxng + open-webui search env
  - apps/edge-api/internal/tools/                    # NEW — tools package
  - apps/edge-api/internal/tools/websearch/          # NEW — SearXNG-backed implementation
  - apps/edge-api/cmd/edge-api/main.go               # mount /v1/tools router
  - apps/control-plane/internal/catalog/tools.go     # NEW — tool catalog source of truth
  - apps/control-plane/internal/catalog/http.go      # NEW — GET /v1/tools
  - packages/openai-contract/overlays/hive-tools.yaml  # NEW — Hive-extension surface
  - packages/openai-contract/scripts/lint-no-customer-usd.mjs  # add SearXNG to provider-blind audit
  - .planning/v1.1-chatapp/SEARCH-TOOL.md            # NEW — engines, limits, billing, MCP status
  - .planning/REQUIREMENTS.md                        # SEARCH-26-01..N rows
  - .planning/v1.1-chatapp/V1.1-MASTER-PLAN.md       # v4 sequence + dependency notes
  - .planning/STATE.md                               # progress counters
---

# Phase 26 — Web Search Tool (SearXNG + Edge-API Function-Tool Endpoint) — PLAN

> **Status:** scaffold. Spawn `/gsd:plan-phase 26-web-search-tool` to expand into
> a full GSD execution PLAN with per-task evidence frames once roadmap is approved.

## Objective

Ship a self-hosted SearXNG search engine plus an **OpenAI-function-tool-compatible** `/v1/tools/web_search` endpoint on `edge-api`, advertised in a new Hive tool catalog so SDK callers (and Open WebUI itself via the same surface) can invoke web search via standard `tools:[{type:"function",function:{name:"web_search"}}]` calls. Optional MCP server wrapper documented as v1.2 follow-up.

## Why

- v1.1 ship-gate (per V1.1-MASTER-PLAN v4 §Cross-Phase Concerns) requires the BD chat-app to ground answers in current web content for verified+ users without leaking provider identity or USD.
- SDK users on the developer API want a first-class `web_search` tool with predictable BDT pricing, not a per-customer external API contract.
- OWUI ≥ 0.4 has native multi-engine web search; SearXNG is the only engine that keeps the search backend self-hosted, provider-blind, and free at the per-query layer.

## Architecture

```text
                       ┌─────────────────────────────────────────────────┐
                       │                       hive                       │
                       │                                                  │
  ┌────────┐  OIDC     │  ┌────────────┐    ┌───────────────────────┐    │
  │ user   ├──────────►│  │ open-webui │───►│ edge-api              │    │
  │ in BD  │  (Phase 19)│ │ (Phase 19) │    │  /v1/chat/completions │    │
  └────────┘            │ │            │    │  /v1/tools/web_search │◄───┘
                        │ │ pipeline   │    │  /v1/tools (catalog)  │
                        │ │  filter:   │    └────────┬──────────────┘
                        │ │  JWT       │             │
                        │ │  forward + │             │  SearXNG JSON
                        │ │  tier gate │             ▼
                        │ └─────┬──────┘    ┌─────────────────┐
                        │       │           │ searxng         │
                        │       │ native    │ self-host       │
                        │       │ web-search│ (services/searxng)
                        │       ▼           └─────────────────┘
                        │ ENABLE_RAG_WEB_SEARCH=true
                        │ RAG_WEB_SEARCH_ENGINE=searxng
                        │ SEARXNG_QUERY_URL=http://searxng:8080/search?q=<query>&format=json
                        └─────────────────────────────────────────────────┘
```

Two consumer paths share the SearXNG backend:

1. **OWUI native web-search toggle** (UI-driven, RAG-style — results injected as context).
2. **Edge-API `/v1/tools/web_search`** (OpenAI function-tool — model invokes via tool calls).

Both flow through the same tier gates + rate limits + billing primitives.

## Scope

### In-scope

1. **SearXNG self-host** — new `services/searxng/` directory with `settings.yml`, instance secret env, JSON output enabled, engine allowlist audited for non-PII-scraping engines.
2. **Docker compose** — activate the `searxng` service stub left in `deploy/docker/docker-compose.yml` from Phase 19 Plan 03; wire OWUI's `ENABLE_RAG_WEB_SEARCH`, `RAG_WEB_SEARCH_ENGINE=searxng`, `SEARXNG_QUERY_URL`.
3. **Edge-API endpoint** `POST /v1/tools/web_search`:
   - Auth: existing JWT/API-key selector (Phase 19-02).
   - Body: `{"query": string, "max_results"?: integer (1-10), "safe_search"?: "off"|"moderate"|"strict", "locale"?: string}`.
   - Calls SearXNG `/search?q=...&format=json` server-side; normalises to `{"results":[{"title","url","snippet","engine"}], "metadata": {"query","took_ms","result_count"}}`.
   - Tier gate: guest/unverified → 403. Verified → daily-quota gated. Credited → BDT per-call debit.
   - Per-key rate-limit override (reuses Phase 12 column `rate_limit_rpm`).
   - Provider-blind error sanitisation: customer-facing errors carry no `searxng`, no upstream engine names, no internal IPs.
4. **Tool catalog** — new `apps/control-plane/internal/catalog/` package; `GET /v1/tools` lists Hive-supported function-tools with full OpenAI function shape; `web_search` is the first entry.
5. **OpenAPI overlay** — `packages/openai-contract/overlays/hive-tools.yaml` adds the Hive-extension paths; generated spec re-emitted; lint-no-customer-usd extended to walk the new package.
6. **OWUI tier gate** — Phase 19 pipeline filter plus Phase 21 tier claims reject OWUI `/api/v1/retrieval/process/web/search` for non-verified tiers with `X-Hive-Error: tier_required`.
7. **Billing** — per-call BDT debit via prepaid ledger reservation (initial cost `0.05 BDT/query`, configurable via `tenant_settings` key `pricing.web_search_bdt_per_query`). Free under verified-tier daily quota (default 20/day, configurable per-key).
8. **SDK integration test** in `packages/sdk-tests/` — Python OpenAI SDK roundtrip against the real Hive stack: register tool → `/v1/chat/completions` with `tool_choice` forcing `web_search` → model emits `tool_calls` → caller hits `/v1/tools/web_search` → caller returns `tool` role message → final assistant message references results. No mocked Supabase, OWUI, LiteLLM, provider, or pgvector path.
9. **Provider-blind audit** — security-reviewer agent walk of every customer-bound surface for SearXNG/engine string leaks (mirrors Phase 17 FX audit pattern).
10. **Docs** — `.planning/v1.1-chatapp/SEARCH-TOOL.md` with engine list, default rate-limits, billing rates, MCP wrapper status; `.planning/REQUIREMENTS.md` rows `SEARCH-26-01..N`.

### Optional (gate-flagged behind `ENABLE_MCP=true`)

11. **MCP server wrapper** at `services/hive-tools-mcp/` (Node + `@modelcontextprotocol/sdk`, Streamable HTTP transport). Exposes `web_search` to MCP clients (Claude Desktop, Cursor, Continue). Implementation deferred to v1.2 unless schedule allows.

### Out of scope (v1.2+)

- Additional search engines beyond SearXNG (Tavily, Serper, Brave-API direct).
- MCP marketplace exposure beyond a single `web_search` stub.
- Image / news / video vertical search (SearXNG supports them; advertise in v1.2).
- Per-query LLM-based result ranking.
- Long-term result caching (initial: passthrough; cache-tier in v1.2 if cost justifies).

## Tasks

| # | Task | Owner | Files |
|---|------|-------|-------|
| 1 | SearXNG settings.yml — engine allowlist, JSON output, admin secret, no public bind | infra | `services/searxng/settings.yml`, `.env.example` |
| 2 | docker-compose: activate `searxng` block; wire OWUI env vars | infra | `deploy/docker/docker-compose.yml` |
| 3 | Engine allowlist security audit (no engines scraping PII without consent; safe_search default `moderate`) | security-reviewer | `services/searxng/ENGINE-AUDIT.md` |
| 4 | Edge-API package `internal/tools/websearch` — types, validation, SearXNG client | go-reviewer | `apps/edge-api/internal/tools/websearch/*.go` |
| 5 | Edge-API handler `POST /v1/tools/web_search` — JWT/API-key auth, tier gate, rate-limit, ledger debit, provider-blind errors | go-reviewer | `apps/edge-api/internal/tools/websearch/http.go` |
| 6 | Edge-API mount `/v1/tools/*` in `cmd/edge-api/main.go` | go-reviewer | `apps/edge-api/cmd/edge-api/main.go` |
| 7 | Control-plane tool catalog `internal/catalog/tools.go` — first entry: `web_search` | go-reviewer | `apps/control-plane/internal/catalog/*.go` |
| 8 | Control-plane `GET /v1/tools` listing endpoint | go-reviewer | `apps/control-plane/internal/catalog/http.go` |
| 9 | OpenAPI overlay `hive-tools.yaml` + regenerated spec | typescript-reviewer | `packages/openai-contract/overlays/hive-tools.yaml`, `packages/openai-contract/generated/hive-openapi.yaml` |
| 10 | Extend `lint-no-customer-usd.mjs` to walk `internal/tools/*` + overlays (provider-blind scope expansion) | typescript-reviewer | `packages/openai-contract/scripts/lint-no-customer-usd.mjs` |
| 11 | OWUI pipeline-filter tier gate for `/api/v1/retrieval/process/web/search` | typescript-reviewer | `apps/edge-api/internal/auth/pipeline_filter.go` |
| 12 | SDK integration test — Python OpenAI SDK function-tool roundtrip | tdd-guide | `packages/sdk-tests/python/test_web_search_tool.py` |
| 13 | Provider-blind audit pass (mirrors Phase 17) — final security-reviewer walk | security-reviewer | `.planning/phases/26-web-search-tool/evidence/SEARCH-26-AUDIT.md` |
| 14 | `SEARCH-TOOL.md` operator doc — engines, defaults, billing, MCP status | doc-updater | `.planning/v1.1-chatapp/SEARCH-TOOL.md` |
| 15 | `REQUIREMENTS.md` `SEARCH-26-01..N` rows + traceability links | doc-updater | `.planning/REQUIREMENTS.md` |
| 16 | (Optional) MCP wrapper at `services/hive-tools-mcp/` if schedule allows | typescript-reviewer | `services/hive-tools-mcp/*` |
| 17 | VERIFICATION.md — evidence frames per requirement | gsd-verifier | `.planning/phases/26-web-search-tool/26-VERIFICATION.md` |

## Success Criteria

- OWUI per-chat web-search toggle returns grounded answers via SearXNG for verified+ users (E2E).
- SDK caller can `tools:[{type:"function",function:{name:"web_search"}}]` and roundtrip works against `/v1/chat/completions` + `/v1/tools/web_search` (integration test).
- Guest/unverified tier blocked at both pipeline filter AND edge-api handler (defense in depth).
- BDT debits land in prepaid ledger; no USD on customer surface (lint guard).
- Provider-blind audit clean: zero `searxng`, engine names, upstream IPs in customer-bound responses or errors.
- Rate-limit headers present on every response (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`).
- SearXNG instance hardened: no open relay, no admin surface bound on public interface.

## Risks

| Risk | Severity | Mitigation |
|------|----------|-----------|
| SearXNG instance abuse (open relay) | High | Bind to docker network only; admin secret; rate-limit at edge-api before SearXNG. |
| Provider-blind regression | High | Mirror Phase 17 pattern: lint guard, security-reviewer audit, automated grep in CI. |
| Function-tool catalog regresses strict OpenAI compat for vanilla `/v1/chat/completions` | Medium | OpenAPI overlay scoped to `/v1/tools/*` paths only; spec regression test. |
| OWUI native web-search env-flag drift across OWUI versions | Medium | Pin OWUI tag in `OWUI-VERSION.md`; CI smoke test exercises web-search toggle. |
| SearXNG result quality varies by engine | Low | Engine allowlist tuned during UAT (Phase 25); fallback engines listed. |
| Billing rate too low / too high for BD market | Low | `tenant_settings.pricing.web_search_bdt_per_query` per-tenant override possible. |

## Dependencies

- **Phase 12** — rate-limit infrastructure (Redis token bucket, per-key columns).
- **Phase 14** — prepaid ledger primitives for BDT debit (reservation + commit).
- **Phase 19 Foundation Slice** — Open WebUI compose service, JWT/API-key selector, and pipeline filter.
- **Phase 20 Provider Catalog** — catalog surfaces and LiteLLM config reloads must not drift from tool/model advertisement.
- **Phase 21 Credit and Quota Engine** — resolved tier, quota, and BDT billing primitives.
- (Scheduling) **Phase 26 executes before Phase 24/25** if web search remains in v1.1 launch scope, so EnterpriseEdge packaging and Hive Cloud cutover include or exclude SearXNG deliberately.

## Open Questions

1. **Billing model lock**: per-call BDT debit (recommended) vs daily-quota-then-block (no debit, simpler ops). Decision in PLAN.md expansion via `/gsd:plan-phase`.
2. **MCP wrapper urgency**: ship in Phase 26 or punt to v1.2? Default: punt unless schedule allows.
3. **Image/news verticals**: surface as separate function-tools (`image_search`, `news_search`) now or wait for v1.2? Default: wait.
4. **Result cache**: bypass at v1.1 (provider-fresh always) or short TTL cache to reduce SearXNG load? Default: bypass; reconsider post-launch metrics.
5. **SearXNG instance secret rotation cadence**: 90-day rotate via OCI Vault, or static for v1.1? Default: 90-day.
