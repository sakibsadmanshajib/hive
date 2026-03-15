# Provider-Agnostic Zero-Cost Chat Design

## Goal

Fix the current `guest-free` regressions and replace the mock guest path with a provider-backed zero-cost chat architecture that keeps Hive's public API OpenAI-compatible.

## Problem Summary

Two regressions exist on `main` as of `2026-03-14`:

1. Authenticated `guest-free` requests fail with `failed to consume credits via rpc: invalid credits amount` because the authenticated chat path still attempts to consume `0` credits through the billing RPC.
2. Guest `guest-free` requests still route to the mock provider and return `MVP response: <prompt>` rather than a real provider-backed completion.

The current design also couples public model ids too tightly to concrete providers, which makes it hard to support zero-cost offers from multiple vendors or handle overlapping upstream models cleanly.

## Design Goals

- Keep `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and `/v1/images/generations` OpenAI-compatible.
- Make `guest-free` a real zero-cost chat model backed by provider offers, not a mock/demo response.
- Keep zero-cost routing provider-agnostic so free offers can come from OpenRouter, Groq, OpenAI, Gemini, Anthropic, or future providers.
- Prevent zero-cost traffic from silently falling through to paid providers.
- Keep billing logic explicit and safe: paid models consume credits, zero-cost models do not.
- Prepare five chat providers behind one internal abstraction: OpenRouter, OpenAI, Groq, Gemini, and Anthropic.

## Provider Research Notes

Official provider docs as checked on `2026-03-14`:

- OpenAI chat/models base: `https://api.openai.com/v1`
- Groq OpenAI-compatible base: `https://api.groq.com/openai/v1`
- OpenRouter base: `https://openrouter.ai/api/v1`
- Gemini OpenAI-compatible base: `https://generativelanguage.googleapis.com/v1beta/openai/`
- Anthropic native base: `https://api.anthropic.com/v1`

Anthropic documents an OpenAI compatibility layer, but their docs warn it is not the long-term or production-ready path for most use cases. The safer default is to use Anthropic's native Messages API behind an internal adapter while still returning OpenAI-compatible payloads publicly.

## Core Decisions

### 1. Separate public models from provider offers

Hive should expose stable public model ids such as `guest-free`, `fast-chat`, and `smart-reasoning`. Those ids are product-facing and remain part of the public API contract.

Internal routing should operate on provider offers instead:

- provider instance id
- provider kind
- upstream model id
- capability
- cost class
- transport mode
- readiness and health state

This keeps public ids stable even when providers or upstream model ids change.

### 2. Route by policy, not by vendor

`guest-free` becomes a virtual zero-cost chat model. It can point to any number of eligible zero-cost offers across multiple providers. Guest and zero-cost authenticated traffic both route through the same zero-cost policy.

The policy rule is simple:

- only offers with `costClass = "zero"` are eligible for `guest-free`
- if no healthy zero-cost offer is available, fail closed
- never spill into fixed-cost or variable-cost offers

### 3. Use a shared adapter shape where possible

OpenRouter, OpenAI, Groq, and Gemini can share an OpenAI-compatible upstream chat transport.

Anthropic should use a native adapter. Internally it translates from Hive's normalized chat request into Anthropic Messages format and then translates the result back into Hive's OpenAI-compatible response format.

### 4. Keep billing at the virtual-model layer

Public virtual models continue to define billing-facing metadata such as:

- `costType`
- `pricing`
- capability

Billing should depend on the selected public model, not on the concrete provider offer picked behind the scenes. That keeps pricing policy reviewable in code and prevents hidden billing drift when provider mappings change.

For zero-cost public models:

- authenticated requests bypass credit consumption
- guest requests continue to bypass credit consumption
- usage attribution still records the request with `0` credits

### 5. Handle provider overlap explicitly

Multiple providers can expose the same underlying family or comparable offerings. To avoid duplicating business models for every provider, Hive should separate:

- canonical public model ids
- provider offers

Example:

- public model `guest-free`
- eligible offers `openrouter/free-model-a`, `groq/free-model-b`, `gemini/free-model-c`

Routing chooses an eligible offer at request time. That allows overlap and future changes without reshaping the public contract.

## Proposed Internal Shape

### Public model catalog

Public model entries remain Hive-specific and exposed via `/v1/models`.

Each entry should keep:

- `id`
- `object`
- `capability`
- `costType`
- `pricing`

Optionally, internal-only metadata can attach a list of eligible offer ids without exposing it publicly.

### Provider offer catalog

Provider offers are internal only. Each offer should include:

- `id`
- `provider`
- `capability`
- `transport`
- `upstreamModel`
- `costClass`
- `enabledByConfig`
- `readinessCheck`

For the first slice, the offer catalog can stay code-defined and environment-backed rather than database-driven.

## Request Flow

### Authenticated chat

1. Resolve the requested public model id.
2. Validate capability and policy.
3. If the public model is zero-cost, skip credit consumption.
4. Choose a healthy eligible offer for that public model.
5. Dispatch through the correct provider adapter.
6. Persist usage with the public model id and actual credits for that public model.
7. Return an OpenAI-compatible response.

Current gap:
- Authenticated web chat still shares runtime endpoints with the public API today. Reporting tags that traffic as `web`, but the shared runtime endpoints remain a temporary gap until follow-up issue `#57` separates authenticated web traffic from API-business analytics at the execution boundary.

### Guest chat

1. Resolve the requested public model id.
2. Enforce guest policy: only zero-cost chat models are allowed.
3. Choose a healthy zero-cost offer.
4. Dispatch through the provider adapter.
5. Persist guest usage with `0` credits.
6. Return an OpenAI-compatible response.

## Failure Handling

- Zero-cost requests must never fall through to a paid offer.
- If no healthy zero-cost offer exists, return a controlled availability error.
- Paid models keep explicit billing behavior and existing refund-on-provider-failure protections.
- Provider readiness should continue to use safe metadata endpoints instead of token-spending probes where possible.

## Environment Strategy

Secrets and upstream model ids should live in `.env`.

Planned env inputs:

- `OPENROUTER_API_KEY`
- `OPENROUTER_BASE_URL`
- `OPENROUTER_MODEL`
- `OPENROUTER_FREE_MODEL`
- `OPENROUTER_TIMEOUT_MS`
- `OPENROUTER_MAX_RETRIES`
- `OPENAI_API_KEY`
- `OPENAI_BASE_URL`
- `OPENAI_CHAT_MODEL`
- `OPENAI_IMAGE_MODEL`
- `OPENAI_FREE_MODEL`
- `OPENAI_TIMEOUT_MS`
- `OPENAI_MAX_RETRIES`
- `GROQ_API_KEY`
- `GROQ_BASE_URL`
- `GROQ_MODEL`
- `GROQ_FREE_MODEL`
- `GROQ_TIMEOUT_MS`
- `GROQ_MAX_RETRIES`
- `GEMINI_API_KEY`
- `GEMINI_BASE_URL`
- `GEMINI_MODEL`
- `GEMINI_FREE_MODEL`
- `GEMINI_TIMEOUT_MS`
- `GEMINI_MAX_RETRIES`
- `ANTHROPIC_API_KEY`
- `ANTHROPIC_BASE_URL`
- `ANTHROPIC_MODEL`
- `ANTHROPIC_FREE_MODEL`
- `ANTHROPIC_TIMEOUT_MS`
- `ANTHROPIC_MAX_RETRIES`

The business-policy catalog should stay in code for now so pricing, cost class, and offer eligibility remain reviewable and testable.

## Non-Goals For This Slice

- No dynamic provider/model catalog persisted in Supabase.
- No automatic provider pricing ingestion.
- No public exposure of provider offer ids or raw upstream ids.
- No silent paid fallback for zero-cost traffic.

## Verification Strategy

- Add targeted tests for authenticated zero-cost billing bypass.
- Add targeted tests for guest zero-cost provider-backed responses.
- Add provider-registry tests for zero-cost-only routing and overlap handling.
- Add adapter tests for OpenAI-compatible transports and Anthropic native translation.
- Re-run API tests, API build, web tests, and Docker-local web build after implementation.
