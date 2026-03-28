# Phase 9: Operational Hardening - Context

**Gathered:** 2026-03-19
**Status:** Ready for planning

<domain>
## Phase Boundary

Register explicit stub routes for all unsupported OpenAI API endpoints so they return informative 404s with OpenAI error format instead of the generic catch-all response. Create GitHub issues for each deferred endpoint group with acceptance criteria. This phase covers route stubs and issue tracking only — actual implementation of any deferred endpoint belongs in a future milestone.

</domain>

<decisions>
## Implementation Decisions

### Stub response format
- Use `sendApiError()` helper (existing, from `apps/api/src/routes/api-error.ts`) with explicit 404 status
- Error type: `not_found_error` (consistent with STATUS_TO_TYPE mapping)
- Error code: `"unsupported_endpoint"` (distinguishes from generic "unknown route" 404)
- Message pattern: `"The /v1/{endpoint} endpoint is not yet supported. Please check our roadmap for availability."`
- Each stub route must be an explicitly registered route handler — not relying on the catch-all `setNotFoundHandler`

### Route organization
- Single consolidated file: `apps/api/src/routes/v1-stubs.ts`
- Export a `registerV1StubRoutes(app)` function following existing naming convention
- Register in `apps/api/src/routes/v1-plugin.ts` alongside other route registrations
- Use wildcard routes where appropriate (e.g., `/v1/audio/*` covers all audio sub-paths)

### Endpoints to stub (OPS-01)
All HTTP methods that OpenAI supports for each path should be stubbed:
- `/v1/audio/speech` — POST
- `/v1/audio/transcriptions` — POST
- `/v1/audio/translations` — POST
- `/v1/files` — GET, POST
- `/v1/files/:file_id` — GET, DELETE
- `/v1/files/:file_id/content` — GET
- `/v1/uploads` — POST
- `/v1/uploads/:upload_id` — GET
- `/v1/uploads/:upload_id/cancel` — POST
- `/v1/uploads/:upload_id/parts` — POST
- `/v1/batches` — GET, POST
- `/v1/batches/:batch_id` — GET
- `/v1/batches/:batch_id/cancel` — POST
- `/v1/completions` — POST (legacy, out of scope per REQUIREMENTS.md)
- `/v1/fine_tuning/jobs` — GET, POST
- `/v1/fine_tuning/jobs/:fine_tuning_job_id` — GET
- `/v1/fine_tuning/jobs/:fine_tuning_job_id/cancel` — POST
- `/v1/fine_tuning/jobs/:fine_tuning_job_id/events` — GET
- `/v1/fine_tuning/jobs/:fine_tuning_job_id/checkpoints` — GET
- `/v1/moderations` — POST

### GitHub issues (OPS-02)
- One issue per endpoint group (not per method): audio, files, uploads, batches, completions, fine_tuning, moderations
- Issue title format: `feat: implement /v1/{endpoint-group} (OpenAI compatibility)`
- Issue body template: endpoint paths + HTTP methods, link to OpenAI API reference, acceptance criteria (endpoint returns valid OpenAI response schema, SDK clients work end-to-end)
- Issues may be created manually or via a script — implementation choice is Claude's discretion
- Issues should reference the stub implementation so they're easy to find

### Claude's Discretion
- Whether to use wildcard route syntax (`/v1/audio/*`) or register each sub-path individually
- Exact wording of the "not yet supported" message
- Whether OPS-02 issues are created via `gh` CLI script or manually documented
- Whether to add a comment in `v1-stubs.ts` linking each stub to its tracking issue

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements
- `.planning/REQUIREMENTS.md` — OPS-01 and OPS-02 requirement definitions, full list of deferred endpoints, acceptance criteria

### Existing implementation patterns
- `apps/api/src/routes/api-error.ts` — `sendApiError()` helper, `STATUS_TO_TYPE` mapping, `OpenAIErrorType`
- `apps/api/src/routes/v1-plugin.ts` — Route registration pattern, existing `setNotFoundHandler` (stubs must be explicit routes, not rely on this)
- `apps/api/src/routes/chat-completions.ts` — Reference for `registerXxxRoute(app, services)` function pattern

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `sendApiError(reply, 404, message, { type: "not_found_error", code: "unsupported_endpoint" })` — drop-in for all stub handlers
- `STATUS_TO_TYPE` record already maps 404 → `"not_found_error"` — consistent error typing

### Established Patterns
- Route registration: `registerXxxRoute(app, services)` exported from each route file, called in `v1-plugin.ts`
- Stubs don't need `services` injection since they don't call any backends — `registerV1StubRoutes(app)` signature (no services arg)
- v1Plugin uses `skip-override` symbol to share error/not-found handlers across the scoped plugin

### Integration Points
- `apps/api/src/routes/v1-plugin.ts` — add `registerV1StubRoutes(app)` call after existing route registrations
- Stub routes must be registered BEFORE the `setNotFoundHandler` would fire (Fastify processes explicit routes first, so order doesn't matter, but stubs must be registered within the v1Plugin scope)

</code_context>

<specifics>
## Specific Ideas

- OPS-01 explicitly requires "coming soon" or "not supported" messaging — the message must be human-readable and informative, not just a status code
- The existing `setNotFoundHandler` returns `"Unknown API route: METHOD /url"` — stubs should return something more helpful like `"The /v1/audio/speech endpoint is not yet supported. Please check our roadmap for availability."`

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 09-operational-hardening*
*Context gathered: 2026-03-19*
