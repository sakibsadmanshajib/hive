---
phase: 07-media-file-and-async-api-surface
plan: "04"
subsystem: api
tags: [images-api, audio-api, auth, routing, accounting, go, adapter-pattern, gap-closure]

# Dependency graph
requires:
  - phase: 07-02
    provides: Images and audio handlers (simplified, no auth/routing/accounting)
  - phase: 07-03
    provides: Batches adapter pattern reference implementation
  - phase: 04-02
    provides: RoutingClient.SelectRoute with capability flags
  - phase: 03-02
    provides: AccountingClient reservation lifecycle
provides:
  - images/authz_adapter.go — AuthorizerAdapter bridging authz.Authorizer to images.Authorizer
  - images/routing_adapter.go — RoutingAdapter bridging inference.RoutingClient to images.RoutingInterface
  - images/accounting_adapter.go — AccountingAdapter bridging inference.AccountingClient to images.AccountingInterface
  - audio/authz_adapter.go — AuthorizerAdapter bridging authz.Authorizer to audio.Authorizer
  - audio/routing_adapter.go — RoutingAdapter bridging inference.RoutingClient to audio.RoutingInterface
  - audio/accounting_adapter.go — AccountingAdapter bridging inference.AccountingClient to audio.AccountingInterface
  - Full authorize->route->reserve->dispatch->finalize/release lifecycle in both handlers
affects: [phase-08-payments, verification-gaps, openai-compliance]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Adapter pattern isolating images/audio handlers from direct service dependencies (mirrors batches pattern)
    - Full orchestrator lifecycle: authorize -> select route -> create reservation -> dispatch -> finalize/release
    - Model alias rewriting: user-supplied model alias replaced with LiteLLM route model name before upstream dispatch
    - Reserve-on-entry/finalize-on-success/release-on-error pattern for credit accounting
    - Multipart model field rewriting inside goroutine using captured litellmModel variable

key-files:
  created:
    - apps/edge-api/internal/images/authz_adapter.go
    - apps/edge-api/internal/images/routing_adapter.go
    - apps/edge-api/internal/images/accounting_adapter.go
    - apps/edge-api/internal/audio/authz_adapter.go
    - apps/edge-api/internal/audio/routing_adapter.go
    - apps/edge-api/internal/audio/accounting_adapter.go
  modified:
    - apps/edge-api/internal/images/handler.go
    - apps/edge-api/internal/images/handler_test.go
    - apps/edge-api/internal/audio/handler.go
    - apps/edge-api/internal/audio/handler_test.go
    - apps/edge-api/cmd/server/main.go

# Key decisions
decisions:
  - "[07-04] handleMultipartAudio gains accountingEndpoint parameter (separate from litellmPath) — transcription and translation have different endpoint strings for reservation records but same litellm path prefix"
  - "[07-04] Model rewriting in multipart goroutine uses captured litellmModel local variable — avoids closure-over-loop-variable hazard; goroutine captures string by value"
  - "[07-04] Test doubles implement Authorizer/RoutingInterface/AccountingInterface stubs in _test packages — existing tests updated to use new 7-arg/5-arg NewHandler signatures without changing test intent"

# Metrics
metrics:
  duration: 35min
  completed: "2026-04-10"
  tasks: 2
  files: 11
---

# Phase 07 Plan 04: Images and Audio Auth/Routing/Accounting Gap Closure Summary

**One-liner:** Full orchestrator lifecycle (authorize, route, reserve, dispatch, finalize/release) added to images and audio handlers via six new adapter files, closing all three verification gaps identified in 07-VERIFICATION.md.

## What Was Built

Plan 07-02 shipped images and audio handlers with intentionally simplified LiteLLM dispatch — no API key auth, no routing capability check, no credit accounting. Verification (07-VERIFICATION.md) found three failing truths:

1. Image/audio handlers bypass auth (unauthenticated callers can generate media)
2. Model names pass through to LiteLLM without capability-based route selection
3. No credit reservations are created or finalized for image/audio requests

This plan adds the complete orchestrator lifecycle to both handlers.

## Changes By Package

### images package (3 new files + 2 modified)

**New adapter files:**
- `authz_adapter.go` — wraps `authz.Authorizer` → `images.Authorizer` interface
- `routing_adapter.go` — wraps `inference.RoutingClient` → `images.RoutingInterface`; maps `NeedImageGeneration`/`NeedImageEdit` capability flags
- `accounting_adapter.go` — wraps `inference.AccountingClient` → `images.AccountingInterface`; delegates `CreateReservation`, `FinalizeReservation`, `ReleaseReservation`

**handler.go:** Added `Authorizer`, `RoutingInterface`, `AccountingInterface` interface definitions and supporting types (`AuthResult`, `RouteInput`, `RouteResult`, `ReservationInput`, `FinalizeInput`). Handler struct gains three new fields. `NewHandler` now takes 7 parameters. Both `handleGeneration` and `handleEdit` call `authorize()` first, then `SelectRoute`, then `CreateReservation`, then dispatch with model-rewritten body, then `FinalizeReservation` on success or `ReleaseReservation` on any error path.

### audio package (3 new files + 2 modified)

**New adapter files:** Same pattern as images, with `NeedTTS`/`NeedSTT` capability flags for routing.

**handler.go:** Same interface/type additions. `handleSpeech` gains the full lifecycle. `handleMultipartAudio` gains an `accountingEndpoint` parameter (passed from `handleTranscription`/`handleTranslation`) and implements the full lifecycle with model-field rewriting inside the multipart goroutine.

### main.go (wiring)

Instantiates `imagesAuthorizer`, `imagesRouting`, `imagesAccounting` and passes them to `images.NewHandler`. Instantiates `audioAuthorizer`, `audioRouting`, `audioAccounting` and passes them to `audio.NewHandler`. All three instances use the existing `authorizer`, `routingClient`, and `accountingClient` already initialized for the inference handler.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated handler_test.go files to compile with new NewHandler signatures**
- **Found during:** Task 1 (after rewriting handler.go signatures)
- **Issue:** Both `images/handler_test.go` and `audio/handler_test.go` called the old `NewHandler` with the simplified signature (no auth/routing/accounting). These would fail to compile with the new 7-arg/5-arg signatures.
- **Fix:** Added `mockAuthorizer`, `mockRouting`, `mockAccounting` stub implementations to each test file. Updated `buildHandler`/`buildAudioHandler` helpers to pass stubs. All existing test assertions preserved unchanged — only the wiring changed.
- **Files modified:** `images/handler_test.go`, `audio/handler_test.go`
- **Commit:** 7f99647

## Verification Results

All three gaps now closed:

| Check | Result |
|-------|--------|
| `h.authorizer.AuthorizeRequest` in images/handler.go | Found (line 123, called from authorize() helper) |
| `h.authorizer.AuthorizeRequest` in audio/handler.go | Found (line 110, called from authorize() helper) |
| `h.routing.SelectRoute` in images/handler.go | Found (lines 179, 321 — handleGeneration and handleEdit) |
| `h.routing.SelectRoute` in audio/handler.go | Found (lines 165, 291 — handleSpeech and handleMultipartAudio) |
| `route.LiteLLMModelName` model rewriting | Found in both images (lines 213, 348) |
| `h.accounting.CreateReservation` | Found in all 4 handler methods |
| `h.accounting.FinalizeReservation` | Found on all success paths |
| `h.accounting.ReleaseReservation` | Found on all error paths |
| `imagesAuthorizer/imagesRouting/imagesAccounting` in main.go | Found (lines 91-97) |
| `audioAuthorizer/audioRouting/audioAccounting` in main.go | Found (lines 107-113) |
| `go build ./apps/edge-api/...` | EXIT:0 |

## Self-Check: PASSED

Files exist:
- apps/edge-api/internal/images/authz_adapter.go — FOUND
- apps/edge-api/internal/images/routing_adapter.go — FOUND
- apps/edge-api/internal/images/accounting_adapter.go — FOUND
- apps/edge-api/internal/audio/authz_adapter.go — FOUND
- apps/edge-api/internal/audio/routing_adapter.go — FOUND
- apps/edge-api/internal/audio/accounting_adapter.go — FOUND

Commits exist:
- 7f99647 — feat(07-04): add auth/routing/accounting adapters and update images/audio handlers
- 7addd95 — feat(07-04): wire images and audio adapters in main.go
