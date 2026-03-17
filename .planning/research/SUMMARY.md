# Research Summary: OpenAI API Compliance Hardening

**Domain:** OpenAI-compatible inference API proxy
**Researched:** 2026-03-16
**Overall confidence:** MEDIUM

## Executive Summary

Hive's existing `/v1/*` endpoints have the correct URL structure but lack schema-level compliance. The current implementation uses hand-rolled TypeScript types with no runtime validation, returns non-standard error formats (`{ error: "string" }` instead of `{ error: { message, type, param, code } }`), has no streaming in the route layer, and the `/v1/models` response includes custom fields while missing required OpenAI fields (`created`, `owned_by`). This is a common state for early-stage OpenAI-compatible proxies -- the routing works, but SDK compatibility breaks on edge cases.

The compliance stack is straightforward: generate TypeScript types from OpenAI's own OpenAPI spec (73K lines, local at `docs/reference/openai-openapi.yml`) using `openapi-typescript`, validate incoming requests with TypeBox (Fastify's native schema library, compiles to JSON Schema for Fastify's built-in Ajv validation), and test everything with the official `openai` npm SDK as an integration test client. This pattern aligns with Fastify's architecture -- TypeBox schemas feed directly into Fastify's validation pipeline with zero additional middleware.

The hardest part is not the tooling but the surface area. Hive implements 5 endpoints but the OpenAI spec covers dozens of schemas. The strategy must be surgical: generate all types (zero-runtime) but only write TypeBox schemas for implemented endpoints. Response builders typed against generated types catch drift at compile time without runtime overhead.

Error format standardization and streaming compliance (SSE with `usage` in final chunk) are the two areas most likely to cause SDK compatibility issues and should be addressed first. The existing PITFALLS.md documents 12 specific pitfalls with codebase line references.

## Key Findings

**Stack:** `openapi-typescript` for type generation, `@sinclair/typebox` + `@fastify/type-provider-typebox` for request validation (Fastify-native), `openai` npm SDK for integration testing. Three new production deps, two new dev deps.
**Architecture:** Dual-surface Fastify plugin separation (`/v1` public API vs `/web` proprietary). Scoped auth hooks, error handlers, and schemas per surface. Domain layer stays surface-neutral.
**Critical pitfall:** Error format is broken on every error path (flat string instead of nested object). This is the single most visible SDK incompatibility.

## Implications for Roadmap

Based on research, suggested phase structure:

1. **Foundation: Types + Errors + Models** - Generate types, set up TypeBox + Fastify type provider, standardize error format, fix `/v1/models` schema
   - Addresses: Schema infrastructure, error format compliance (Pitfalls 1, 4, 5, 10, 11, 12)
   - Avoids: Building on untyped foundation
   - Low risk, high leverage -- everything else depends on this

2. **Chat Completions Compliance** - Harden `/v1/chat/completions` request/response against spec
   - Addresses: Response schema (TS-1), request parameter pass-through (TS-2), usage object (TS-5)
   - Order by SDK usage frequency: chat/completions is the most-exercised endpoint

3. **Streaming + Telemetry** - Implement SSE streaming in route layer, `usage` in final chunk
   - Addresses: Streaming compliance (TS-3, TS-4), Pitfall 3
   - Most complex phase -- requires provider stream consumption, chunk transformation, usage aggregation

4. **Surface Expansion** - Add `/v1/embeddings`, harden `/v1/images/generations` and `/v1/responses`
   - Addresses: Feature gap (D-4), remaining endpoint compliance (TS-10, TS-11)
   - Lower risk since patterns are established in phases 1-3

5. **SDK Integration Testing** - Full test suite using `openai` npm package as client
   - Addresses: End-to-end compliance verification
   - Can start in phase 1 but full coverage comes last

**Phase ordering rationale:**
- Error format must come first -- every error from Hive currently crashes official SDKs
- Types must come early because every other phase depends on them
- Existing endpoints before new endpoints (fix what exists before adding more)
- Streaming is complex and benefits from having the non-streaming path solid first
- Testing is continuous but the full suite validates after all endpoints are compliant

**Research flags for phases:**
- Phase 3 (Streaming): Needs deeper research on OpenRouter's streaming behavior and which metadata they include in stream chunks
- Phase 1 (Foundation): May need to re-download a newer OpenAI spec if the local copy (v2.3.0) is stale
- Phase 4 (Responses API): Large schema surface -- may need its own research spike

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | MEDIUM | Core libraries are well-known but exact latest versions need verification (WebSearch unavailable) |
| Features | HIGH | Derived directly from the OpenAI OpenAPI spec v2.3.0 (local) |
| Architecture | HIGH | Fastify plugin encapsulation is a documented core pattern |
| Pitfalls | HIGH | Identified with codebase line references and spec cross-references |

## Gaps to Address

- Exact latest versions of `openapi-typescript`, `@sinclair/typebox`, `@fastify/type-provider-typebox`, `openai` SDK -- run `npm view <pkg> version` before installing
- OpenRouter's streaming metadata completeness (does it return `usage` in streaming? which models?)
- Whether the local `openai-openapi.yml` (v2.3.0) matches the latest OpenAI spec version
- nanoid v5 ESM compatibility with the API project's tsconfig

---
*Research summary: 2026-03-16*
