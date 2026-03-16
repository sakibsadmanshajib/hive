# Issue 13 Design: Usage Analytics and Support Snapshot

## Goal

Deliver a focused first slice of issue #13 that improves usage visibility for end users and support visibility for operators without expanding into a general-purpose admin platform.

## Scope

This design adds:

- richer user-scoped analytics on `/v1/usage`
- one admin-only user troubleshooting snapshot endpoint
- matching documentation and access-control verification

This design does not add:

- cross-user search or listing for operators
- background rollups or pre-aggregated analytics tables
- public exposure of internal support or provider diagnostics

## Current constraints

The current `/v1/usage` route returns only recent raw usage events for the authenticated user. The developer panel consumes that route only to count usage rows. Existing operator-only diagnostics already use an `x-admin-token` boundary on `/v1/providers/status/internal` and `/v1/providers/metrics/internal`, and that security model should remain consistent.

## Recommended approach

### User-scoped analytics

Extend `/v1/usage` so it still returns recent raw events, but also returns a `summary` object computed from the same underlying `usage_events` rows. The summary should include:

- total credits spent across the returned analysis window
- total request count
- daily trend for a fixed recent window
- model split
- endpoint split

This keeps the existing `usage` permission boundary unchanged and upgrades the response into something the developer panel can present directly.

### Admin-only support snapshot

Add a new operator route, protected with `x-admin-token`, to return a single-user troubleshooting snapshot. A path like `/v1/support/users/:userId` is sufficient for the first slice and aligns with the issue's emphasis on support tooling with explicit security boundaries.

The response should include:

- basic user identity data already available through the user service
- current credit balance
- recent usage summary matching the `/v1/usage` aggregation shape
- recent raw usage events
- managed API key metadata
- recent API key lifecycle events

This gives support a targeted investigation surface without introducing a broad admin search feature.

## Service and route design

Move aggregation logic into the usage service rather than composing it in route handlers. That shared service should produce a reusable usage analytics snapshot from recent usage rows. Both `/v1/usage` and the new support route should consume the same aggregation method so the user-facing and admin-facing summaries remain structurally aligned.

The existing raw usage table remains the source of truth. The first implementation should compute summaries on read. That keeps blast radius low and avoids schema or scheduler work before there is evidence that read-time aggregation is insufficient.

## Security boundaries

- `/v1/usage` remains user-scoped and authenticated exactly as today.
- `/v1/support/users/:userId` is admin-only and must return `401` without a valid `x-admin-token`.
- Public provider status and metrics routes remain sanitized and unchanged.
- No provider internal diagnostics are added to user or support analytics payloads.

## Testing

Add or update tests for:

- usage service aggregation behavior
- enriched `/v1/usage` route payload shape
- admin support route happy path
- admin support route `401` behavior without a valid token
- preservation of existing user snapshot compatibility where reused

## Documentation

Update:

- OpenAPI for the enriched usage route and new support route
- changelog for the new analytics and support surface
- operator-facing runbook with safe usage guidance for the support snapshot

## Alternatives considered

### Option 1: Expand only `/v1/usage`

This is lowest risk, but it leaves the operator workflow unresolved and does not satisfy the support-tooling portion of the issue.

### Option 2: Build a broader admin analytics/search API

This is more powerful, but it expands scope sharply and creates privacy and maintenance risks. It also conflicts with the issue note to avoid turning this into a giant admin-platform umbrella.

### Option 3: Recommended targeted pair

Enrich `/v1/usage` and add a single-user admin snapshot. This covers the highest-signal user and operator workflows with the smallest additional surface area.
