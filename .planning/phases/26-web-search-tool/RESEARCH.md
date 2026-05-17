# Phase 26 — Web Search Tool — Research

**Researched:** 2026-05-17
**Domain:** SearXNG self-host, Open WebUI native web-search, OpenAI function-tool spec, edge-api endpoint integration, MCP wrapper viability
**Confidence:** HIGH (OpenAI function-tool shape, OWUI native web-search support, SearXNG JSON API) · MEDIUM (engine allowlist for BD locale, SearXNG hardening defaults) · LOW (exact OWUI pinned tag — confirmed at Phase 19 Plan 03 fork day)

**Execution position:** Phase 26 is append-numbered but should execute after Phase 21 credit/quota primitives and before Phase 24/25 packaging/cutover when included in v1.1.

---

## 1. SearXNG — backend choice

**Why SearXNG vs alternatives:**

| Engine | License | Self-host | Provider-blind | BDT-friendly | Notes |
|--------|---------|-----------|----------------|--------------|-------|
| **SearXNG** | AGPL-3.0 | yes | yes (meta-search aggregates 70+ engines) | free per-query | Default; OWUI first-class support |
| Tavily | proprietary | no | no | $0.001-0.01 USD/call | Recurring USD bill — regulatory FX risk |
| Serper | proprietary | no | no | $0.001-0.005 USD/call | Same |
| Brave Search API | proprietary | no | partial | $3-9 USD/1k | Same |
| DuckDuckGo (direct) | n/a (ToS-grey) | yes (scrape) | no | free | Brittle; engine blocks scrapers |
| Self-built crawler | n/a | yes | yes | infra cost | Out of v1.1 budget |

**Decision (locked):** SearXNG self-host. Provider-blind by construction (meta-search hides upstream engine identity). Free per-query. Eliminates customer-surface USD via Phase 17 mandate.

**Hardening checklist (mirrors `searxng/searxng-docker` security defaults):**
- Bind only to docker network — no host port exposed externally.
- Admin secret (`SEARXNG_SECRET`) loaded from env, rotated 90-day.
- Disable engines that scrape PII without consent (LinkedIn-people, etc).
- `safe_search` default: `moderate`.
- Rate-limit at edge-api boundary before SearXNG sees the call (Redis token bucket from Phase 12).

## 2. Open WebUI native web-search

**Confirmed (OWUI source, v0.4+):**
- Env vars: `ENABLE_RAG_WEB_SEARCH`, `RAG_WEB_SEARCH_ENGINE`, `SEARXNG_QUERY_URL`, `RAG_WEB_SEARCH_RESULT_COUNT` (default 3), `RAG_WEB_SEARCH_CONCURRENT_REQUESTS` (default 10).
- Engines supported: `searxng`, `google_pse`, `brave`, `kagi`, `mojeek`, `serpstack`, `serper`, `serply`, `searchapi`, `tavily`, `jina`, `bing`, `exa`, `perplexity`, `sougou`, `duckduckgo`.
- UI: per-chat "Web Search" toggle (no global on/off for end users; admin-only).
- Flow: user toggles search → OWUI fetches SearXNG JSON → top-N results loaded into context window as RAG-style citations.

**Gate to verified+:** OWUI doesn't ship tier-aware enable. Implementation = pipeline filter rejects `/api/v1/retrieval/process/web/search` for guest/unverified.

## 3. OpenAI function-tool shape (SDK caller path)

**Reference:** OpenAI Chat Completions API tool calling spec.

**Tool declaration shape (caller sends in `tools`):**
```json
{
  "type": "function",
  "function": {
    "name": "web_search",
    "description": "Search the public web for fresh information. Returns top-N results with title, url, and snippet.",
    "parameters": {
      "type": "object",
      "properties": {
        "query": {"type": "string", "description": "The search query."},
        "max_results": {"type": "integer", "minimum": 1, "maximum": 10, "default": 5},
        "safe_search": {"type": "string", "enum": ["off","moderate","strict"], "default": "moderate"}
      },
      "required": ["query"]
    }
  }
}
```

**Model emits (in `choices[0].message.tool_calls`):**
```json
{
  "id": "call_abc123",
  "type": "function",
  "function": {"name": "web_search", "arguments": "{\"query\": \"...\", \"max_results\": 5}"}
}
```

**Caller invokes `POST /v1/tools/web_search` with the arguments, gets back results, returns to model as `tool` role message:**
```json
{
  "role": "tool",
  "tool_call_id": "call_abc123",
  "content": "{\"results\": [...]}"
}
```

**Hive extension catalog (`GET /v1/tools`):** returns array of tool declarations the model can use. Compatible with the OpenAI tool-discovery pattern but additive — vanilla `/v1/chat/completions` flow unchanged.

## 4. MCP wrapper (optional, v1.2 candidate)

**Reference:** Anthropic Model Context Protocol — `@modelcontextprotocol/sdk` (Node) supports stdio + Streamable HTTP transports.

**Why:** MCP is the forward-looking standard for tool discovery across Claude Desktop, Cursor, Continue, etc. Wrapping `web_search` once gives Hive a foothold in that ecosystem without an OpenAI-spec-specific contract.

**Risk:** adds an extra service (`hive-tools-mcp`) and a second auth surface (MCP doesn't yet have a settled auth standard; OAuth bearer most common).

**Decision (current):** ship a `SPEC.md` stub in Phase 26; defer implementation to v1.2 unless schedule allows.

## 5. Billing primitive reuse

**Existing ledger:** prepaid BDT ledger from v1.0 + Phase 14 expansion. Reservation-then-commit pattern (already used for chat completions).

**New rate:** `pricing.web_search_bdt_per_query` — initial value `0.05` BDT (~$0.0005 USD internal). Configurable via `tenant_settings` for per-tenant override. Free under verified-tier daily quota; over-quota → credit consumption (credited tier only).

## 6. Provider-blind error sanitisation

**Pattern (mirrors `apps/edge-api/internal/auth/sanitize.go`):**
- Internal error includes SearXNG details for log/metrics: `searxng timeout: dial tcp 172.20.0.5:8080: i/o timeout`.
- Customer-facing error stripped: `{"error":{"code":"upstream_unavailable","message":"Search service temporarily unavailable."}}`.

**Audit script extension:** `lint-no-customer-usd.mjs` extended to also blocklist `searxng|searx|brave-api|kagi|mojeek|serper|tavily|jina|exa|perplexity` across customer-facing surfaces.

## 7. Open questions feeding plan refinement

1. Billing per-call vs daily-quota — decision in PLAN.md expansion.
2. MCP wrapper Phase 26 vs v1.2 — default v1.2.
3. Image/news verticals — default v1.2.
4. Result cache — default bypass; reconsider post-launch.
5. SearXNG secret rotation cadence — default 90-day OCI Vault.

## 8. References

- OpenAI Chat Completions function-tool spec (caller emits → model fills → caller invokes).
- Open WebUI source: `backend/open_webui/retrieval/web/searxng.py`.
- SearXNG docs: `https://docs.searxng.org/` (verified via Context7 prior to plan expansion).
- MCP spec: `https://modelcontextprotocol.io/` (Streamable HTTP transport).
- Hive Phase 17 FX zero-leak pattern — `.planning/phases/17-fx-usd-zero-leak/PLAN.md` (template for provider-blind audit walk).
