# Supabase Option A Backend Simplification Design

## Context and Goal

The team wants to reduce backend reinvention and operational overhead while preserving the existing OpenAI-compatible API contract as a hard requirement.

This design adopts a Supabase-first approach for platform primitives and keeps the current API gateway for differentiated runtime behavior.

## Hard Constraints

- Preserve OpenAI-compatible API behavior and paths, including `/v1/chat/completions` and related response/header semantics.
- Preserve billing and ledger formulas and refund rules.
- Preserve public vs internal provider status security boundaries.
- Minimize migration risk with phased cutover and rollback controls.

## Target Architecture

- Keep `apps/api` as a thin compatibility gateway so clients see no contract changes.
- Move identity and core app data to Supabase:
  - auth identities
  - sessions
  - user profiles/settings
  - API key metadata persistence
  - standard CRUD domain tables
- Keep custom domain logic in gateway services:
  - provider routing/fallback
  - OpenAI-compatible response shaping
  - credit debit/refund formulas
  - webhook verification and idempotency behavior
  - provider status sanitization policy
- Use Supabase Postgres as primary data store with RLS for user-scoped tables.
- Keep Redis (or equivalent managed Redis) for rate limiting/runtime state.

## Replace vs Keep Boundaries

### Replace with managed tools

- Auth/session/OAuth/MFA with `Supabase Auth`.
- User/profile/settings persistence with `Supabase Postgres + RLS`.
- API key metadata persistence in Supabase tables (store only hashed key material and key metadata).
- Migration/ops workflow for app data via Supabase SQL migrations and dashboard observability.

### Keep custom in gateway

- OpenAI-compatible endpoint contract and compatibility behavior.
- Provider adapter orchestration and fallback logic.
- Billing ledger formulas:
  - base top-up conversion
  - refund conversion and eligibility rules
  - promo/non-refundable behavior
- Payment webhook verification/reconciliation domain logic.
- Public/internal provider status split and admin-token protections.

## Migration Design (Phased)

### Phase 1: Supabase introduction and auth trust

- Introduce Supabase alongside current runtime.
- Gateway validates Supabase JWT/session for existing `/v1/users/*` paths.
- Maintain existing route paths and response schemas.

### Phase 2: User domain repository switch

- Move user/profile/settings reads and writes to Supabase-backed repositories.
- Keep existing service interfaces to avoid route-level contract changes.

### Phase 3: API key persistence migration

- Move API key persistence to Supabase tables.
- Keep `x-api-key` gateway middleware and auth context behavior unchanged.

### Phase 4: Billing-adjacent persistence migration

- Migrate balances/usage/top-up/refund tables to Supabase Postgres.
- Keep all billing formulas and eligibility rules in gateway domain layer.

### Phase 5: Legacy auth cleanup

- Remove legacy auth/session storage and dead code only after parity validation.
- Leave provider routing/payment verification/status behavior untouched.

## Operations, Error Handling, and Safety Rails

- Preserve status codes and response behavior for auth and API key failures.
- Keep `/v1/providers/status` sanitized and `/v1/providers/status/internal` token-protected.
- Add per-domain feature flags for migration gates:
  - `supabase_auth_enabled`
  - `supabase_user_repo_enabled`
  - `supabase_api_keys_enabled`
  - `supabase_billing_store_enabled`
- Use dual-write and shadow-read windows on critical migrations.
- Use idempotency keys and unique transaction references for payment and ledger writes.

## Verification Plan

Before each phase rollout:

- Run API contract tests for OpenAI-compatible endpoints.
- Run auth regression tests (register/login/oauth/session/API key revoke).
- Run billing regression tests (top-up, consume, refund eligibility).
- Verify provider status security:
  - public status has no internal detail leakage
  - internal status returns `401` without valid admin token
- Run full API test suite and API build.

## Success Criteria

- No client-facing API contract changes.
- No billing formula drift.
- Equal or better auth/session reliability.
- Reduced custom backend surface area and lower operations burden.
