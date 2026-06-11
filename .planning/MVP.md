# Hive MVP Definition, 2026-06-11

Decision by orchestrator with owner mandate. Owner sketch: chat UI plus OpenAI spec API plus RAG, cloud and hardware versions from one script, voice dictation if cheap. This doc locks scope.

## Market read (short)

No Bangladesh-localized ChatGPT-class product exists with BDT prepaid billing (bKash, SSLCommerz). Global products price in USD with foreign cards, a hard barrier for BD consumers and SMEs. Developers in BD lack an OpenAI-compatible API billable in BDT. RTX Spark class hardware (fall 2026) and DGX Spark (shipping now) create a near-term enterprise self-host story no local player serves. Window: ship cloud MVP before global players localize payments, ship EnterpriseEdge before local system integrators assemble their own.

## Capability read (what already exists)

| MVP ingredient | Status |
|---|---|
| OpenAI-compatible API (/v1 chat, embeddings, files, images, audio) | Shipped v1.0 |
| Prepaid BDT billing (bKash, SSLCommerz, Stripe), math/big FX | Shipped v1.0 |
| Chat UI (Open WebUI fork, bn-BD plus en-US, admin stripped) | Phase 19, merged |
| Personal RAG (file upload, doc Q&A) | Open WebUI built in, ships with chat |
| Signup abuse protection | PR #166 in review |
| Tool calls for agentic clients | Explicit 400 now (PR #162), real routing lands Phase 20 |
| Provider catalog, model management | Phase 20, plans being written |
| One deploy script cloud plus enterprise | Compose profiles exist, EnterpriseEdge profile needs verification on real hardware |

## MVP scope (locked)

**Product name framing: one product, two SKUs. Hive Cloud (hosted, BDT prepaid) and Hive EnterpriseEdge (self-hosted, same compose).**

1. **Chat workstation**: Open WebUI chat with histories, file upload RAG (OWUI native), image input on multimodal models, Bangla and English UI.
2. **Developer API**: OpenAI spec surface as shipped in v1.0, plus capability-based tool call routing (Phase 20) so coding agents and SDK tool use work against OpenRouter tool-capable models.
3. **Billing**: prepaid BDT credits as shipped. No new billing features.
4. **Deploy**: single compose, `--profile local|cloud|enterprise`. EnterpriseEdge verified on one real GPU box. LiteLLM gains optional Ollama backend entry so an EnterpriseEdge box with a local model serves inference without cloud keys (config only, no new code).
5. **Stretch (only if zero schedule risk)**: voice input in chat via Open WebUI built-in STT pointed at a server-side faster-whisper container (Whisper large v3 turbo, covers Bangla). Config plus one compose service, size S.

## Explicitly NOT in MVP

Web search tool (Phase 26), shared tenant RAG (Phase 22), credit buckets (Phase 21), full admin console pages (Phase 23 beyond existing), Anthropic API surface, MCP connectors, router LLM, model advisor, mobile and desktop apps, on-device capability suite. All tracked in roadmap issues and v1.2/v1.3 docs.

## Critical path to MVP launch

1. Merge in-flight PRs (#161 to #167 train).
2. Phase 19 closeout: C4 live JWT verification (needs running stack), M12 CI decision.
3. Phase 20 execution: 5 plans drafted from the Phase 20 brief, plus plan 20-06: capability-based tool call passthrough (issue #118 medium term).
4. Phase 25 chat app re-audit (existing ship gate).
5. EnterpriseEdge verification on real hardware plus Ollama backend config.
6. Stretch: whisper STT container.

## Open asks for owner

1. Which hardware exists today for EnterpriseEdge verification (DGX Spark? RTX desktop? specs)?
2. Production domain name (needed for Caddy, Turnstile widget domains, OWUI public URL).
3. Confirm MVP scope lock, anything above the line you would cut, anything below the line you cannot live without.
