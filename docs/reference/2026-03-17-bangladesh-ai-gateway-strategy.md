# Bangladesh AI Gateway Opportunity

**Type:** Strategy memo
**Date:** 17 March 2026
**Prepared for:** Internal strategy discussion
**Bottom line:** The strongest version of this business is not "a Bangladesh clone of ChatGPT." It is a Bangladesh-first AI access and orchestration platform: local payments, local invoicing, predictable prepaid credits, OpenAI-compatible API for developers, and later a web app with Bangla UX, business workflows, and selected verticals. The moat is operations and distribution, not raw model capability.

---

## 1. Executive Summary

The opportunity is real, but the commercially attractive version is narrower and more operational than a pure engineering-first "build a better ChatGPT" thesis. Bangladesh now has material digital scale, very large mobile-wallet reach, and meaningful gaps between global AI providers and local commercial reality. That creates room for a company that makes frontier AI easier to buy, easier to budget, and easier to use in Bangladesh.

The recommended entry wedge is an OpenAI-compatible API gateway with BDT-denominated prepaid credits, local payments, usage controls, and model routing across OpenAI, Anthropic, Gemini, and aggregators such as OpenRouter. The web app should be treated as a second surface, not the first product. It should have its own backend contract, richer analytics, and product-specific controls rather than simply mirroring the public API.

**Key conclusions at a glance:**

| Question | Conclusion | Why it matters |
|----------|------------|----------------|
| Is there a market? | Yes, but not mainly as a mass-market chatbot | The strongest early buyers are developers, startups, freelancers, agencies, and SMEs that need local billing and predictable spend |
| Main moat | Local payments, invoicing, distribution, Bangla UX, and workflow packaging | Global labs can undercut on model access; they are slower to localize payment rails and sector workflows |
| First product | API gateway first | Faster to ship, easier to meter, and better aligned with the payment-friction problem |
| Web UI strategy | Custom app protocol, not public API shape | Lets you separate analytics, pricing logic, credits, and anti-abuse controls |
| Embeddings | Start with managed embeddings; benchmark one self-hosted multilingual model in parallel | Shipping speed matters more than early embedding purity |
| Corporate structure | Bangladesh operating company plus foreign procurement/infrastructure company, likely Singapore | Cleaner for local payments, VAT, and foreign SaaS/vendor payments |

---

## 2. Market Reality in Bangladesh

- **77.7 million internet users** (44.5% penetration) as of early 2025 [S1]
- **185 million cellular mobile connections** [S1]
- **bKash: 82 million customers**, 350,000+ agents, 900,000+ merchants as of December 2025 [S2]

The commercial question is not "can Bangladesh use AI?" but "for which buyer segment does local access materially improve conversion, retention, or trust?"

**Most plausible demand:**
- Developers, startups, agencies wanting one locally-billable API endpoint
- SMEs needing predictable BDT-denominated prepaid usage
- Freelancers and teams wanting invoices, team controls, and credit caps without foreign-card dependency
- Later: e-commerce, customer support, tutors, agricultural information (vertical UX required)

**Least attractive near-term bet:** broad consumer promise of "everything ChatGPT has, but cheaper" — global labs can compress that margin quickly.

---

## 3. Competitive Pressure

Global providers are pushing down consumer pricing (ChatGPT Go at $8/month as of January 2026 [S3][S4]), but still do not universally localize billing. The Bangladesh window exists while global providers are slow to localize payment UX, BDT-denominated pricing, and sector workflows.

The moat is **operational, not foundational-model based.** The company can be undercut on raw tokens; it is harder to undercut quickly on local merchant setup, wallet integrations, invoice flows, top-up behavior, Bangla onboarding, support response, and domestic workflow design.

---

## 4. Business Model

**Recommended sequencing:**
1. OpenAI-compatible API gateway with BDT prepaid credits, model routing, spend controls, dashboards, local payments
2. Web app for selected business/prosumer use cases, with separate analytics and custom backend protocol
3. Workflow products and verticals (business copilot, customer support, file/knowledge projects, Bangla vertical assistants)
4. Cost-down levers: self-hosted open models, batched workloads, selective owned inference

**Pricing design:**
- Default: prepaid credit wallet, **not** subscription-first
- Credits denominated in BDT value / internal platform credit — not stored USD
- API customers: token/request metering
- Web users: simplified per-action credit consumption (messages, images, tool runs)
- **Do not publish internal cost-plus-margin to end users** — publish a clean tariff

---

## 5. Operating Model and Legal Structure

| Entity | Primary Role | What it should do | Why |
|--------|-------------|-------------------|-----|
| Bangladesh Co. | Operating company | Local payments, VAT, invoicing, sales, support, distribution | Needed for merchant rails and domestic contracting |
| Singapore Co. | Procurement / infra | Cloud/vendor contracts, upstream model payments, optional IP holding later | Cleaner for foreign SaaS payments and future financing |
| Founder personally | Temporary bridge only | Short-term testing expense payer | Should not remain core operating flow past MVP |
| Future infra SPV (optional) | Owned inference | GPU/accelerator assets once economics justify | Useful only after clear volume thresholds |

**VAT caution:** Bangladesh NBR states reverse charge applies at import level for imported services, including software imported through the internet as a service [S5]. Any BD-to-foreign-company service flow must be documented and reviewed with a Bangladesh tax adviser before scale.

---

## 6. Compliance, Privacy, and Regulatory

**SOC 2:** Medium-term target; not day-one. Build controls first (RBAC, secrets management, audit logs, incident handling, retention policies, customer data separation), then pursue certification.

**Logging split:**

| Surface | Default logging posture | Reason |
|---------|------------------------|--------|
| Public API | Minimal request metadata, billing logs, abuse logs; no raw prompt retention by default | Enterprise expectations; easier to defend commercially |
| Web app | Conversation analytics, tool traces, product events, content moderation telemetry | Needed for iteration, UX improvement, routing quality, credit economics |
| Sensitive org modes | Optional zero-retention or short-retention mode | Needed later for higher-trust accounts |

**Telephony/WhatsApp regulatory caution:** BTRC publishes licensing guidance for IP telephony and telecom value-added services [S6][S7]. Voice/telecom-like services must not be launched at scale without regulatory review. **Rollout order:** API → web → messaging channels → voice notes → voice calling (if business case justifies).

---

## 7. Product Architecture

**Two-front-door system: one internal core, two external entry points.**

- **Public API edge:** Fastify service exposing `/v1/chat/completions`, `/v1/models`, `/v1/embeddings`, and later image/audio
- **Web app edge:** Custom Fastify BFF for chat streams, projects, files, tools, wallet state, conversation metadata — NOT forced into public API shapes
- **Shared internal core:** Auth, org/user entitlements, credit ledger, model registry, routing engine, usage accounting, policy engine, provider adapters
- **Persistence:** Supabase/Postgres for accounts/wallets/chats/files/vector data; object storage for uploads; Redis for caching and rate limits
- **Observability:** App telemetry separated from public API retention; infra logs and security logs separated from user-content logs

**Key design rule:** The frontend must never be the source of truth for pricing, routing, or entitlement. Those belong in backend services.

---

## 8. Frontend Strategy

Stop scratch-building a custom frontend. The bottleneck is backend (credits, routing, analytics separation, pricing enforcement, anti-abuse). Adopt/fork an existing OSS frontend base.

**Options evaluated:**

| Option | Best use | Strengths | Cautions |
|--------|----------|-----------|----------|
| Chatbot UI | Ownable product base | Clean, popular, easy to customize [S8] | Evaluate code quality and roadmap before committing |
| LibreChat | Feature-rich platform base | Supports custom OpenAI-compatible endpoints [S9] | Heavier, more opinionated |
| Open WebUI | Fast feature baseline or internal client | OpenAI-compatible APIs, built-in RAG patterns [S10] | Harder to bend into tightly custom commercial UX |
| AI SDK components | Utility layer | OpenAI-compatible provider support exists outside Vercel [S11] | Don't let a library dictate product boundaries |

**Recommendation:** Keep hosting decisions independent, treat frontend libraries as replaceable, adopt a reusable frontend codebase only to accelerate product delivery.

---

## 9. OpenAI-Compatible API: MVP Subset

**Minimum useful public API:**
- `GET /v1/models` — required by many clients and UIs for discovery
- `POST /v1/chat/completions` — highest-value compatibility endpoint
- `POST /v1/embeddings` — important for RAG, indexing, semantic search
- Streaming support for chat completions — mandatory for good UX and broad compatibility
- Usage reporting and rate-limit headers where feasible
- Image/audio endpoints deferred until billing engine and abuse controls are mature

**Reference:** OpenAI's official OpenAPI spec [S12]; embeddings reference [S13]; API pricing page [S14].

---

## 10. Embeddings Strategy

- **Internal app and paid default:** `text-embedding-3-small` unless retrieval quality proves insufficient
- **R&D track:** Benchmark one self-hosted multilingual embedding model for Bangla/English/mixed/transliterated inputs
- **Public API:** Expose clean embeddings endpoint; abstract provider through model registry
- **Storage:** Supabase/Postgres with pgvector first; add dedicated vector DB only if proven necessary

OpenRouter also offers a unified embeddings API [S15][S16] but adds a dependency layer.

---

## 11. Economics and Cost-Down Roadmap

Initially a reseller/orchestrator — margin comes from pricing discipline, payments, support value, usage smoothing, and selective routing.

**Cost-down levers (in order of maturity):**
1. Route low-value/high-volume traffic to cheaper models
2. Batch inference for non-real-time enterprise workloads
3. Cache and deduplicate repeated prompts/tool responses where safe
4. Selective self-hosting of open models for embeddings, summarization, classification
5. Negotiate upstream pricing once volume becomes meaningful

**Critical:** Owned inference infrastructure follows, not precedes, validated demand.

---

## 12. Implementation Stages

| Stage | Duration | Scope |
|-------|----------|-------|
| 0 | 2–4 weeks | Design freeze: product thesis, payment rails, legal structure, system boundaries, logging/retention policy |
| 1 | 6–10 weeks | API gateway spine: model registry, provider adapters, API keys, wallet, credit reservation/finalization, OpenAI-compatible endpoints, admin/user dashboards, abuse controls |
| 2 | 4–8 weeks (can overlap) | Payment hardening: local payment integration, order ledger, credit issuance, refund policy, tax invoices, reconciliation, support ops |
| 3 | 6–12 weeks after gateway stability | Web app beta: adopt/customize existing frontend, custom app endpoints, separate analytics/observability, Bangla + English UX, narrow initial workflows |
| 4 | After validated demand | Verticalization: high-value workflow products, self-hosted embedding benchmarks, batch processing offers, owned inference evaluation |

---

## 13. Key Risks and Falsifiable Hypotheses

**Core assumptions:**
- A meaningful set of Bangladeshi customers will prefer local payments and prepaid BDT credits over direct foreign-card billing
- Developers and SMEs are a more realistic early revenue base than mass consumers
- Global labs will remain slower than a focused local operator in payments and workflow localization
- A two-entrypoint architecture will materially reduce abuse and improve product clarity

**Main risks:**
- Global providers add local billing or stronger localized pricing quickly
- Payment or tax integration is more complex than expected
- Margins become too thin if competing mainly on price
- Abuse, fraud, or scraping costs outrun support and revenue
- Team spends too long polishing broad consumer UI before proving gateway demand

**What should be validated early:**
- What percentage of potential customers say local payment is a real blocker today
- Which pricing frame converts best: prepaid wallet, monthly packs, or hybrid
- Whether developers actually want one unified local endpoint vs going direct to providers
- Whether Bangla UX is a conversion advantage or mainly a retention advantage
- Whether one or two vertical workflows outperform a broad generic assistant for paid retention

---

## Appendix A: Suggested MVP Scope

- **Public API:** models, chat completions, embeddings, streaming, API key management, spend caps
- **Billing:** prepaid wallet, top-up flow, credits ledger, tariff table, reconciliation, invoices
- **Admin:** provider health, routing overrides, wallet support tools, abuse monitoring
- **Docs:** copyable OpenAI-compatible examples for cURL, Python, JS, and popular SDKs
- **Deferred:** image/video/audio parity, complex agent frameworks, domestic voice calling

---

## Appendix B: Source Notes

- **[S1]** DataReportal, "Digital 2025: Bangladesh" — internet-user, penetration, and mobile-connection figures
- **[S2]** bKash official "About" page — customer, agent, and merchant scale claims
- **[S3]** OpenAI, "Introducing ChatGPT Go, now available worldwide," 16 Jan 2026
- **[S4]** OpenAI Help Center, "What is ChatGPT Go?" — availability and billing notes
- **[S5]** Bangladesh NBR VAT FAQ — reverse-charge treatment for software imported through the internet as a service
- **[S6]** BTRC Internet Protocol Telephony Service Provider Guideline
- **[S7]** BTRC Telecommunication Value Added Services guideline
- **[S8]** Chatbot UI GitHub repository
- **[S9]** LibreChat documentation / repository
- **[S10]** Open WebUI docs / repository
- **[S11]** AI SDK documentation for OpenAI-compatible providers
- **[S12]** OpenAI openai-openapi repository and official API reference
- **[S13]** OpenAI embeddings API reference — model names, input shape, limit details
- **[S14]** OpenAI API pricing page
- **[S15]** OpenRouter embeddings documentation
- **[S16]** OpenRouter API overview / model listing docs

---

## Appendix C: OpenAI API-Surface Gaps to Keep in Mind

Even if the initial gateway mirrors only a small subset of OpenAI's API, the design must leave room for future surfaces (responses-style workflows, tool-use metadata, images, audio, richer usage objects).

- Keep internal usage accounting richer than the public response payload
- Do not hard-code a single provider's view of models or modalities into the ledger
- Assume future need for feature flags by org, model, or endpoint
- Separate provider adapters from public endpoint handlers
