# Milestones

Shipped milestones for Hive API Platform. Details archived under `milestones/`.

---

## v1.0 — developer-api-core

**Shipped:** 2026-04-21
**Phases:** 1–10
**Plans:** 49/49
**Timeline:** 2026-02-23 → 2026-04-21 (58 days)
**Commits:** 580 total, 126 `feat` commits
**Status:** tech_debt — ship-ready with 4 documented deferred items.

**Delivered:** OpenAI-compatible developer API gateway with provider-agnostic routing,
prepaid credit ledger, multi-rail BDT/USD checkout, and a developer console. Drop-in
compatible with official OpenAI JS/Python/Java SDKs for chat/completions/responses/embeddings,
images, audio, files, and batches (failure-path).

**Key accomplishments:**

1. OpenAI contract fidelity with JS/Python/Java SDK smoke tests, golden fixtures, and Swagger docs (Phase 1).
2. Money-safe immutable credit ledger with reservation/finalize/refund for every request lifecycle (Phase 3).
3. Provider-agnostic routing with capability matrix, cache-aware attribution, and provider-blind errors (Phase 4).
4. Full inference + media surface: chat/completions/responses/embeddings, SSE streaming, images, audio, files, batches (Phases 6–7).
5. Multi-rail BDT/USD checkout with `math/big` FX, BD VAT 15%, and payment-intent state machine (Phase 8).
6. Developer console + Prometheus/Grafana/Alertmanager observability (Phase 9).
7. Supabase Storage migration, KEY-04 per-key attribution, cold-start healthcheck stabilization (Phase 10).

**Requirements:** 13 satisfied, 2 partial (API-07 batch success-path + KEY-04 success-path attribution — both blocked by upstream provider capability and deferred to v1.1).

**Known Gaps (deferred to v1.1):**

- Batch success-path terminal settlement — blocked by LiteLLM file-upload provider matrix; OpenRouter + Groq have no native batch API. Failure-path settlement verified live. See `KNOWN-ISSUE-batch-upstream.md`.
- `ensureCapabilityColumns` targets `route_capabilities` instead of `provider_capabilities` — latent (seed path populates required columns).
- `amount_usd` exposed on BD checkout — regulatory risk.
- Formal VERIFICATION.md for Phases 2 & 3 — UAT/VALIDATION artifacts stand as evidence.
- Phases 11–14 (compliance cleanup, KEY-05 hot-path rate limiting, console integration, invoicing + budget integration).

**Archive:**

- `.planning/milestones/v1.0-ROADMAP.md` — full phase + plan breakdown
- `.planning/milestones/v1.0-REQUIREMENTS.md` — requirement traceability at shipping moment
- `.planning/milestones/v1.0-MILESTONE-AUDIT.md` — goal-backward audit
- `.planning/milestones/v1.0-INTEGRATION-CHECK.md` — cross-phase integration report
- `.planning/v1.1-DEFERRED-SCOPE.md` — v1.1 scope definition

**Tag:** `v1.0`
