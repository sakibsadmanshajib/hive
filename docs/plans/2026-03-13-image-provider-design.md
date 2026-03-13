# Image Provider Integration Design

**Date:** 2026-03-13

## Goal

Replace Hive's mock-backed image generation path with a real provider-backed implementation while preserving an OpenAI-compatible external API and fitting the existing provider-registry, billing, and observability model.

## Design Summary

The external API remains OpenAI-compatible. Internal provider-specific behavior is isolated behind adapters so Hive can support OpenAI-native image APIs, OpenRouter-backed image models, or other hosted providers without changing the public contract.

The recommended design extends the existing provider registry with image-generation support instead of creating a second registry. This keeps circuit breaking, provider status, provider metrics, and fallback behavior consistent across chat and image capabilities while minimizing structural churn.

## Current State

- `POST /v1/images/generations` already exists at `apps/api/src/routes/images-generations.ts`
- `RuntimeAiService.imageGeneration(...)` in `apps/api/src/runtime/services.ts` consumes credits and returns a fabricated `example.invalid` image URL
- `image-basic` in `apps/api/src/domain/model-service.ts` is mapped to `mock`
- The provider layer in `apps/api/src/providers/` currently supports chat only
- `packages/openapi/openapi.yaml` exposes the route but the schema is too minimal for strong OpenAI SDK compatibility

## Requirements

### Functional

- Add at least one real image provider behind the existing route
- Preserve an OpenAI-compatible request and response contract for `/v1/images/generations`
- Keep billing explicit for image requests
- Support provider routing through an adapter pattern
- Preserve public/internal provider status and metrics boundaries

### Non-Functional

- Minimize blast radius by reusing existing runtime and provider patterns
- Avoid leaking provider-specific response bodies through the public API
- Keep provider health and readiness checks zero-token where possible
- Leave room for future support of additional image providers without redesigning the route contract

## Recommended Architecture

### 1. Public API Contract

Hive keeps the public HTTP contract OpenAI-compatible at the route boundary and OpenAPI spec.

For `/v1/images/generations`, the request schema should be expanded to closely match OpenAI expectations, including:

- `model`
- `prompt`
- `n`
- `size`
- `response_format`
- `user` where supported as pass-through metadata

The response should preserve the standard list shape:

- `created`
- `data`
- each item containing either `url` or `b64_json` depending on `response_format`

Provider-specific details should remain outside the response body.

### 2. Adapter Pattern

Provider-specific logic lives behind internal adapters. Hive normalizes the public request into an internal image-generation request, passes that to the provider registry, and the selected provider adapter translates it to the provider-native API.

The provider layer should gain image-specific request and response types, plus an optional provider capability for image generation. This keeps the adapter contract explicit without forcing every current provider to implement image support immediately.

Recommended shape:

- extend `ProviderClient` with optional image-generation capability
- add provider-level image request and response types
- keep chat and image execution paths separate in the registry while sharing candidate-provider selection, fallback order, metrics, and circuit-breaker logic

This allows:

- an OpenAI image adapter
- an OpenRouter-backed image adapter using an OpenAI-compatible transport shape where applicable
- future image providers without changing the route contract

### 3. Registry and Routing

The existing `ProviderRegistry` should gain an image-generation execution path parallel to chat.

Responsibilities:

- resolve the primary provider from explicit model mapping
- build fallback candidates
- skip disabled providers
- respect provider circuit state
- execute provider image generation
- record metrics and provider failures
- return routed provider metadata to the runtime service

Fallback support should exist in the architecture from day one, but the first implementation only needs one real provider plus explicit mock behavior where configured. This keeps the design extensible without requiring multi-provider image support immediately.

### 4. Runtime Service Flow

`RuntimeAiService.imageGeneration(...)` should stop fabricating output directly.

The new flow:

1. resolve the selected image model
2. compute the request credit cost from model configuration
3. reserve or consume credits through the existing credit service
4. invoke provider-registry image generation
5. map provider output back to the OpenAI-compatible response shape
6. record usage
7. return provider metadata through headers, not response body

This keeps billing and provider execution centralized in the runtime service.

### 5. Model and Provider Configuration

`image-basic` should move from `mock` to the first real provider. The provider-native model name should remain configurable through the existing provider model map pattern.

This keeps:

- public model names stable
- provider model IDs replaceable
- future provider swaps or fallbacks localized to configuration and adapters

## Billing Design

Billing remains explicit and model-based for the first implementation.

Recommended behavior:

- image requests use the configured model credits per request
- credits are not silently derived from provider pricing in v1
- successful provider attempts return `x-actual-credits`
- if the request cannot reach a usable provider result, the system should avoid charging as though generation succeeded

The implementation should be careful about when credits are consumed relative to provider execution so failed provider calls do not create confusing overcharges. If the current credit service only supports immediate consumption, the implementation plan should include the smallest safe approach and regression coverage around failure cases.

## Error Handling

Public API behavior should remain generic and OpenAI-compatible where practical.

Principles:

- authentication and authorization failures stay unchanged
- insufficient credits returns `402`
- provider failures map to gateway-style failures without leaking raw provider diagnostics in the body
- unsupported provider capability is treated as provider unavailability, not a public schema change

Provider detail belongs in:

- internal provider status
- internal provider metrics
- server logs
- optional routing headers already used by the platform

## Observability and Safety

The existing public/internal boundary must remain intact.

Requirements:

- `/v1/providers/status` remains sanitized
- `/v1/providers/status/internal` remains admin-token protected
- `/v1/providers/metrics` remains public-safe
- `/v1/providers/metrics/internal` and Prometheus internal metrics remain protected

Image support should not add provider diagnostic detail to public responses or public metrics payloads.

Provider readiness checks for image models should use provider metadata endpoints where possible rather than billable generation calls.

## OpenAI Compatibility Notes

Compatibility with the OpenAI SDK and generated clients should be treated as an explicit deliverable.

That means:

- expand `packages/openapi/openapi.yaml` so the image endpoint schema is no longer placeholder-level
- verify request field compatibility for common OpenAI client usage
- verify response body shape matches OpenAI image generation expectations
- review whether Hive should support `Authorization: Bearer <key>` in addition to `x-api-key` for stronger SDK drop-in compatibility

The provider adapter layer must not shape the public contract around any one provider's quirks.

## Testing Strategy

### API and Route Tests

- request validation and OpenAI-compatible request parsing
- response shape for URL and base64 formats where supported
- credit-insufficient behavior
- provider failure behavior

### Registry and Adapter Tests

- primary provider routing
- fallback behavior where configured
- unsupported image capability handling
- provider metrics and circuit-breaker recording for image calls
- provider request/response translation

### Boundary Verification

- public provider status remains sanitized
- internal status and metrics require admin token
- provider headers remain correct where applicable

## Documentation Updates

The implementation should update:

- `README.md`
- `packages/openapi/openapi.yaml`
- roadmap or future implementation docs that still describe images as placeholder-only
- any relevant operator runbooks if image-provider env vars or readiness expectations are added

## Recommended First Provider

The first implementation should target a hosted adapter shape that cleanly supports OpenAI-style image generation semantics. That keeps OpenAI-native integration straightforward and leaves room for OpenRouter-backed image models later if their behavior matches the adapter contract closely enough.

The key design decision is not the brand of the first provider. The key decision is keeping the provider-specific transport behind a stable internal image adapter while the public API stays OpenAI-shaped.
