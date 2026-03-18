# Phase 3: Auth Compliance - Context

**Gathered:** 2026-03-17
**Status:** Ready for planning

<domain>
## Phase Boundary

Make Bearer token authentication and Content-Type headers on `/v1/*` endpoints behave identically to OpenAI across Python, Node, and Go SDKs. Verified with the official `openai` npm SDK against a real test database.

**Out of scope:** Web authentication (separate `/app/*` path, separate phase). Streaming Content-Type enforcement (Phase 6). Any auth mechanism beyond what the OpenAI spec requires.

</domain>

<decisions>
## Implementation Decisions

### Auth architecture — /v1/* is strictly OpenAI-compatible
- `/v1/*` routes accept **only** `Authorization: Bearer <api-key>` as auth
- The Bearer token is resolved as a **Hive API key** — not a Supabase JWT
- The Supabase Auth session lookup (`getSessionPrincipal`) is **removed** from the `/v1/*` auth path
- The `x-api-key` header fallback is **removed** from `/v1/*` routes
- Web app will use a separate path (e.g., `/app/*`) with its own auth, hitting the same backend services

### Token precedence (now moot, but documented for clarity)
- Bearer token is the one and only auth mechanism for `/v1/*`
- No header conflicts possible once x-api-key and JWT paths are removed

### 401 error response
- `code: "invalid_api_key"` for all auth failures — matches OpenAI exactly
- Human-readable `message` differentiates cases:
  - No `Authorization` header present → `"No API key provided"`
  - Token present but not found in DB → `"Incorrect API key provided"`
- Both cases return HTTP 401 with `type: "authentication_error"`

### Content-Type — non-streaming
- A single `onSend` hook in `v1Plugin` explicitly sets `Content-Type: application/json` on every non-streaming reply
- This guarantees compliance regardless of Fastify's default behavior
- Applies to all routes registered under v1Plugin

### Content-Type — streaming contract (enforcement deferred to Phase 6)
- Streaming responses MUST set `Content-Type: text/event-stream`
- Phase 3 documents this contract; Phase 6 implements and enforces it
- No guard code added in this phase

### SDK verification — integration tests with real openai npm SDK
- Tests spin up the Fastify server in test mode
- Use a real Supabase test database instance with a seeded API key
- The `openai` npm SDK is used as the client (not raw fetch/inject)
- Tests cover:
  - Valid Bearer token → 200 from a stub/minimal endpoint
  - Missing Authorization header → 401 with correct error body
  - Invalid token → 401 with correct error body
  - Both `Authorization: Bearer` and `x-api-key` sent → `x-api-key` ignored, Bearer resolved (regression guard)
  - Non-streaming response has `Content-Type: application/json`

### Claude's Discretion
- How to structure the test helper (server bootstrap, DB seeding utilities)
- Whether to create a dedicated `v1-auth` middleware function or inline the simplified logic in `auth.ts`
- Exact Fastify hook type for Content-Type enforcement (`onSend` vs `preSerialization`)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Auth implementation
- `apps/api/src/routes/auth.ts` — Current auth logic; `resolvePrincipal`, `readBearerToken`, `requireApiPrincipal` — this file is the primary target for change

### v1 plugin
- `apps/api/src/routes/v1-plugin.ts` — Where the `onSend` Content-Type hook should be added; also where the scoped error handler lives

### Error format (Phase 1 established)
- `apps/api/src/routes/api-error.ts` — `sendApiError` helper and `STATUS_TO_TYPE` map; use this for all 401 responses

### Requirements
- `.planning/REQUIREMENTS.md` — FOUND-02 (Bearer token compat), FOUND-05 (Content-Type headers)

### OpenAI API spec
- `docs/reference/openai-openapi.yml` — OpenAI spec; check auth error response schema

No external ADRs for this phase — requirements fully captured in decisions above.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `readBearerToken(request)` in `auth.ts:52` — already correct; extracts `Authorization: Bearer <token>` cleanly
- `sendApiError(reply, status, message, { code })` in `api-error.ts` — use for all 401 responses
- `requireApiPrincipal()` in `auth.ts:157` — the entry point called by route handlers; will be simplified

### Established Patterns
- Phase 1/2 test pattern: `FakeApp` with `inject()` for unit tests — SDK integration tests are additive, not a replacement
- `v1Plugin` uses `Symbol.for('skip-override')` for scope control (Phase 1 decision) — do not change this

### Integration Points
- `resolvePrincipal()` in `auth.ts:64` — the function to simplify (remove Supabase JWT path and x-api-key fallback)
- `v1Plugin` in `v1-plugin.ts:10` — add `onSend` hook here for Content-Type enforcement
- All `/v1/*` route handlers call `requireApiPrincipal()` — no per-route changes needed if we fix the root

</code_context>

<specifics>
## Specific Ideas

- `/v1/*` should be a pure OpenAI-compatible surface — "nothing extra, nothing less"
- Web authentication lives on a separate path; same backend services, different auth contract
- The `openai` npm SDK with a real test DB gives the highest confidence — prefer this over mocks for SDK compat tests

</specifics>

<deferred>
## Deferred Ideas

- `/app/*` web authentication path — separate phase, same backend services
- Streaming `Content-Type: text/event-stream` enforcement — Phase 6
- Go SDK and Python SDK integration tests — mentioned in the roadmap success criteria but not in scope for this phase's test work (openai npm SDK covers Node; other SDKs deferred)

</deferred>

---

*Phase: 03-auth-compliance*
*Context gathered: 2026-03-17*
