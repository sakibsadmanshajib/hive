---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
stopped_at: Completed 07-04-PLAN.md
last_updated: "2026-04-10T08:30:00.000Z"
progress:
  total_phases: 9
  completed_phases: 6
  total_plans: 31
  completed_plans: 31
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-03-28)

**Core value:** Developers can switch from OpenAI to Hive with only a base URL and API key change, while keeping predictable prepaid billing and provider-agnostic operations.
**Current focus:** Phase 07 — media-file-and-async-api-surface

## Current Position

Phase: 07 (media-file-and-async-api-surface) — EXECUTING
Plan: 4 of 4 (COMPLETE)

## Performance Metrics

| Execution | Duration | Scope | Files |
|-----------|----------|-------|-------|
| Phase 05 P04 | 73min | 2 tasks | 8 files |
| Phase 05 P01 | 10min | 2 tasks | 9 files |
| Phase 06 P03 | 10min | 2 tasks | 12 files |
| Phase 06 P04 | 12min | 2 tasks | 17 files |

**Velocity:**

- Total plans completed: 18
- Average duration: 19.1min
- Total execution time: 5.74 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-contract-compatibility-harness | 4/4 | 40min | 10min |
| 02-identity-account-foundation | 7/7 | 93min | 13.3min |
| 03-credits-ledger-usage-accounting | 3/3 | 87min | 29min |
| 04-model-catalog-provider-routing | 3/3 | 51min | 17min |
| 05-api-keys-hot-path-enforcement | 2/6 | 83min | 41.5min |

**Recent Trend:**

- Last 5 plans: 04-01 (30min), 04-02 (16min), 04-03 (5min), 05-04 (73min), 05-01 (10min)
- Trend: Phase 5 now has both the prior hot-path hardening slice and the API-key lifecycle foundation in place, leaving policy, projection, and limiter follow-up plans.

| Phase 07 P01 | 9min | 3 tasks | 8 files |
| Phase 07 P02 | 22min | 2 tasks | 9 files |
| Phase 07 P03 | 45min | 2 tasks | 17 files |
| Phase 07 P04 | 35min | 2 tasks | 11 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- Launch scope is the developer API, billing control plane, and developer console only.
- Hive must mirror the public OpenAI API surface except org and admin management endpoints.
- Prompt and response bodies must not be stored at rest for the API product.
- Launch monetization is prepaid Hive Credits only; subscriptions are deferred.
- Hosted Supabase is the v1 auth and primary relational data platform; no separate standalone Postgres server is planned initially.
- The developer workflow must run entirely inside Docker containers, including hot reload, builds, codegen, and tests.
- [01-01] Used GOTOOLCHAIN=auto to install air v1.64.5 (requires Go 1.25) on Go 1.24 base image.
- [01-01] Air build command uses absolute paths from /app workspace root for go.work compatibility.
- [01-01] SDK test services use Docker Compose profiles (test) so they only run on demand.
- [01-03] Java fine-tuning test uses raw HTTP to avoid coupling to SDK fine-tuning API surface changes.
- [01-03] Golden fixtures capture minimal expected shapes for regression, not full response bodies.
- [Phase 01]: Published docs are generated from support-matrix.json plus the upstream spec — Keeps runtime support classification as the single source of truth for the served contract and markdown docs.
- [Phase 01]: The generated contract drops top-level upstream x-oaiMeta — Prevents organization and admin documentation metadata from leaking back into Hive's published contract artifact.
- [Phase 01]: The generator entrypoint is POSIX-sh compatible and the toolchain image includes py3-yaml — Ensures Docker verification uses the same generation path as local development instead of a host-only workflow.
- [02-01]: DB connection failure at startup is non-fatal in control-plane — /health responds even without SUPABASE_DB_URL provisioned, enabling phased environment setup.
- [02-01]: token_hash stored (not raw token) in account_invitations — Security best practice to prevent token exposure from DB reads.
- [02-02]: HashToken (SHA-256 hex) is exported for test use — enables pre-computing known hashes in stubRepo tests without exposing private internals.
- [02-02]: X-Hive-Account-ID fallback is silent — invalid or unauthorized account IDs fall back to default membership without erroring the request.
- [02-02]: AcceptInvitation does not alter current-account on same request — switching workspace is an explicit later action.
- [02-03]: Middleware uses named export `middleware` (not default export) per Next.js App Router convention.
- [02-03]: Callback route uses an explicit allowlist for next= redirect targets (/console, /auth/reset-password) — simpler than regex and easier to audit.
- [02-03]: apps/web-console/.gitignore negates root-level Python lib/ gitignore entry so Next.js lib/ source can be committed.
- [02-04]: WorkspaceSwitcher uses HTML form POST to /console/account-switch — works without JS and keeps cookie mutation in the route handler.
- [02-04]: account-switch route validates account_id against viewer.memberships before persisting — prevents unauthorized workspace switching.
- [02-04]: invitations/accept does not set hive_account_id — newly joined workspace appears in switcher only after explicit user selection.
- [02-04]: VerificationBanner in console layout applies to all console routes without per-page logic.
- [Phase 02]: Core profile completion stays limited to owner name, login email, display name, account type, country, and state/province — Keeps billing and tax completeness out of Phase 2 onboarding gates.
- [Phase 02]: Profile writes update public.accounts display_name and account_type alongside public.account_profiles — Keeps viewer and current-account profile data consistent after edits.
- [Phase 02]: Profiles handler resolves the current account from the authenticated viewer context — Avoids trusting client-supplied account identifiers for profile reads and writes.
- [Phase 02]: The setup flow submits the existing login email as a hidden value so onboarding stays limited to the five visible core fields — Keeps initial setup minimal while satisfying the profile API contract.
- [Phase 02]: Profile editing uses shared server-action form handling while email maintenance stays browser-side — Keeps control-plane profile writes server-side and uses Supabase client auth APIs only where they are required.
- [Phase 02]: Dashboard setup guidance is a reminder card instead of a redirect gate — Preserves /console as the landing route after setup completion.
- [Phase 02]: Billing-profile reads fall back to core-profile contact and location data — Lets optional billing settings render useful defaults before the first billing-specific save.
- [Phase 02]: Billing settings redirect unverified users to /console/settings/profile instead of broadening the restricted-console allowlist — Keeps profile maintenance reachable without turning billing into a Phase 2 gate.
- [Phase 02]: The web-console control-plane client now uses explicit JSON decoders instead of assertion-based parsing — Keeps the touched billing/profile surface aligned with the strict TypeScript policy.
- [03-01]: Reservation holds are negative deltas and releases are positive deltas — keeps reserved-credit math derivable from immutable ledger entries without a mutable balance counter.
- [03-01]: Credit mutation idempotency is anchored in Postgres `credit_idempotency_keys` — Redis is runtime plumbing for later hot-path helpers, not the source of financial truth.
- [03-01]: Ledger balance and history routes resolve current account via `accounts.Service` — avoids trusting client-supplied account IDs on credit read APIs.
- [03-02]: Request accounting keeps both `request_id` and `attempt_number` — retries and interrupted executions stay reconcilable without inventing a second wallet model.
- [03-02]: Usage-event metadata is recursively redacted before persistence — prompt, message, input, response, completion, content, and output_text keys never reach durable storage.
- [03-02]: Current-account usage responses omit `provider_request_id` and `internal_metadata` — customer-visible APIs stay provider-blind even when internal records retain diagnostics.
- [03-03]: Reservation lifecycle state is stored durably in Postgres while immutable ledger entries remain the financial source of truth.
- [03-03]: Ambiguous interruptions default to customer-favoring release plus reconciliation instead of assuming full reserve consumption.
- [03-03]: Current-account reservation mutations reuse the control-plane account resolver and reject invalid reservation IDs at the HTTP boundary.
- [04-01]: The control-plane snapshot drives both `/v1/models` and `/catalog/models` so Hive's public model surfaces cannot drift.
- [04-01]: Edge catalog fetch failures return the provider-blind `catalog_unavailable` OpenAI error instead of leaking snapshot or provider detail.
- [04-02]: Alias capability, allowlist, and fallback checks run inside Hive before LiteLLM receives a route handle.
- [04-02]: LiteLLM model groups are keyed by private route handles rather than public alias IDs.
- [04-03]: Cache-aware provider billing is normalized into the existing `cache_read_tokens` and `cache_write_tokens` fields, and zero-value cache fields stay omitted from customer responses.
- [04-03]: Edge upstream errors mirror the provider-blind sanitization rules locally so customer-visible failures never depend on control-plane routing packages.
- [Phase 05]: API-key mutations remain gated by accounts.Service.EnsureViewerContext and CanManageAPIKeys instead of trusting client ownership claims.
- [Phase 05]: API-key list, detail, create, and rotate responses share a customer-safe serializer that applies expiry projection and never re-emits secrets after issuance.
- [06-03]: SelectRouteResult has no SupportsReasoning field; reasoning capability gating uses NeedReasoning bool as proxy for route capability.
- [06-03]: Responses API streaming ends with event: response.completed — no data: [DONE] sentinel — matching OpenAI Responses SDK expectations.
- [06-04]: dimensions gating uses model name heuristic (contains 'embedding-3') rather than capability flag — pragmatic Phase 6 approach; future phase can add SupportsDimensions to routing types.
- [06-04]: EmbeddingObject.Embedding stays json.RawMessage to handle both float arrays and base64 encoding_format without type assertions.
- [Phase 07-01]: minio.Core used instead of *minio.Client for multipart upload access — NewMultipartUpload/PutObjectPart/CompleteMultipartUpload/AbortMultipartUpload are private on Client but public on Core
- [Phase 07-01]: minio-go pinned to v7.0.91 (latest compatible with Go 1.24 — v7.0.100+ requires Go 1.25)
- [07-02]: images.StorageInterface returns (string, error) for PresignedURL — avoids *url.URL dependency in the images package; storageAdapter in main.go bridges the real files.StorageClient
- [07-02]: Audio Handler has no storage field by design — enforces that audio is never stored; no storage parameter means no accidental storage calls possible
- [07-02]: NeedImageGeneration/NeedTTS/NeedSTT as package constants — documents routing capability intent without requiring a full orchestrator in unit tests
- [07-03]: FilestoreClient and BatchstoreClient use plain http.Client with 10s timeout — no shared transport needed at this scale
- [07-03]: Batches package uses adapter layer (accounting, authz, file, storage) to decouple handler from direct service imports — enables clean unit testing
- [07-03]: Asynq selected for batch worker task queue — consistent with control-plane async patterns; simple Redis-backed queue fits polling use case
- [07-03]: All file/upload/batch operations validate account ownership via AuthSnapshot.AccountID before any data access — no cross-account leakage
- [07-04]: handleMultipartAudio gains accountingEndpoint parameter separate from litellmPath — transcription and translation share the same handler but need different endpoint strings for reservation records
- [07-04]: Model rewriting in multipart goroutine uses captured litellmModel local variable — avoids closure-over-loop-variable hazard
- [07-04]: Test doubles (mock Authorizer/RoutingInterface/AccountingInterface) added in _test packages — existing test assertions preserved, only wiring changed to match new NewHandler signatures

### Pending Todos

None yet.

### Blockers/Concerns

- Provider capability gaps must be handled explicitly so unsupported endpoints fail in an OpenAI-style way.
- Payment-tax behavior across Stripe, bKash, and SSLCommerz needs careful validation during Phase 8.

## Session Continuity

Last session: 2026-04-10T08:30:00Z
Stopped at: Completed 07-04-PLAN.md
Resume file: None
