# Free Tier Zero-Cost Access Design (2026-02-24)

## Goal

Design a free/guest plan that guarantees zero direct inference spend, prevents abuse, and preserves API stability. Free and unauthenticated users must never get API-key inference access. Paid users may optionally route low-effort requests through free resources to reduce cost.

## Chosen Direction

- Primary approach: **A (Hard Segmented Free Pool)** with selected **C (Virtual Product-Tier Models)** elements.
- User segments in scope: both `guest` and `signed-in free`.
- Primary optimization target: strict cost ceiling via no-cost-only routing for free traffic.
- Paid traffic optimization: automatic low-effort classification can free-first route, then fallback.

## Section 1: Access and Security Boundaries

1. Free and unauthenticated users use web-session flows only; no API-key inference access.
2. API key chat endpoints enforce paid-only eligibility (`plan != free`) and return `403` on violation.
3. Guest traffic enters through controlled backend web-session routes with tighter limits than paid API usage.
4. Free/guest contexts can request only `free-*` virtual models.
5. Server-side policy enforcement is authoritative; client model claims are never trusted.
6. Every request logs routing decisions for audit and abuse investigations.

## Section 2: Provider and Model Label System

### Provider Catalog Metadata

For each provider+model entry, maintain:

- `capabilities` (chat, reasoning, tool_use, vision)
- `cost_class` (`zero`, `low`, `paid`)
- `pool_membership` (`free_pool`, `paid_pool`, or both)
- `stability_tier` (`experimental`, `beta`, `stable`)
- `limits_profile` (rpm/tpm/concurrency constraints)
- `trust_level` (policy sensitivity suitability)

### Virtual Model Layer

- `free-fast`: low-latency free pool routing
- `free-balanced`: higher-quality free pool routing
- `paid-fast`: paid default low-latency tier
- `paid-smart`: paid default higher-reasoning tier

### Hard Policy Matrix

- `guest_session` and `free_account_session`: only `free-*`
- `paid_account` and `api_key_paid`: `paid-*` (plus optional free-first optimization)
- `api_key` channel is paid-only

### Cost Isolation Invariant

Free/guest dispatch must assert `cost_class=zero` before call execution. If no healthy zero-cost candidate exists, fail closed with controlled availability messaging; do not spill into paid providers.

## Section 3: Routing and Abuse Control Plane

1. Resolve context (`principal_type`, `entry_channel`).
2. Apply eligibility gates (including free API-key denial).
3. Run low-effort classifier for paid traffic only.
4. Select candidates by pool and policy.
5. Re-assert `cost_class=zero` for free/guest traffic.
6. Apply fallback policy:
   - free/guest: free->free only, then graceful deny
   - paid: free->paid or paid->paid depending class and SLA
7. Enforce abuse limits (IP/session/account buckets, burst controls, concurrency caps, fingerprint heuristics).
8. Apply adaptive mitigations (cooldown, challenge, temporary block, manual review flag).
9. Persist decision trace fields (`user_tier`, `entry_channel`, `virtual_model`, `provider_selected`, `fallback_reason`).

## Section 4: Rollout and Verification

### Delivery Phases

1. Phase 0 (shadow mode): evaluate policies without changing live routing outcomes.
2. Phase 1 (hard free boundaries): enforce no free API-key access and zero-cost-only free dispatch.
3. Phase 2 (paid optimization): enable classifier-driven free-first for paid low-effort traffic.
4. Phase 3 (adaptive abuse controls): tune and enforce score-based mitigations.

### Acceptance Criteria

- Free users cannot create or use inference API keys.
- Unauthenticated users cannot access API-key inference path.
- Free/guest requests never dispatch to non-zero-cost providers.
- Free pool health failure returns controlled degradation, never paid spillover.
- Routing headers remain contract-correct (`x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits`).
- Public provider status stays sanitized; internal status remains admin-token protected.

## Docs and Tracking Updates

When implementation starts, update:

- `docs/architecture/system-architecture.md` for pool split and policy gate flow.
- `README.md` for public free-tier behavior expectations.
- Optional GitHub tracking issue(s) under existing taxonomy labels (`kind:*`, `area:*`, `priority:*`, lifecycle label).

## Inputs Used

- User constraints from brainstorming thread.
- Existing architecture and boundary docs in `README.md`, `AGENTS.md`, and `docs/README.md`.
- Existing provider/status security requirements in current API docs and tests.
