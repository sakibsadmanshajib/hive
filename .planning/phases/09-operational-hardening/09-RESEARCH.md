# Phase 9: Operational Hardening - Research

**Researched:** 2026-03-19
**Domain:** Fastify route stubs, OpenAI error format, GitHub issue automation
**Confidence:** HIGH

## Summary

Phase 9 is a straightforward implementation phase: register explicit Fastify route handlers for all known-but-unsupported OpenAI endpoints so they return informative 404s in OpenAI error format, and create GitHub issues to track future implementation. All building blocks already exist -- the `sendApiError()` helper, the `v1-plugin.ts` registration pattern, and the OpenAI error format are all proven from Phases 1-8.

The primary risk is completeness (missing a route or HTTP method), not complexity. The `sendApiError` helper already handles the exact error shape needed. The `gh` CLI is available for automated issue creation.

**Primary recommendation:** Create a single `v1-stubs.ts` file with a `registerV1StubRoutes(app)` function, register it in `v1-plugin.ts`, test with static assertions on error shape and status code, then script issue creation with `gh issue create`.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Use `sendApiError()` helper with 404 status, type `not_found_error`, code `"unsupported_endpoint"`
- Message pattern: `"The /v1/{endpoint} endpoint is not yet supported. Please check our roadmap for availability."`
- Each stub must be an explicitly registered route handler (not relying on catch-all)
- Single consolidated file: `apps/api/src/routes/v1-stubs.ts`
- Export `registerV1StubRoutes(app)` function (no services arg needed)
- Register in `apps/api/src/routes/v1-plugin.ts` alongside other route registrations
- Use wildcard routes where appropriate (e.g., `/v1/audio/*` covers all audio sub-paths)
- Full endpoint list specified in CONTEXT.md (audio, files, uploads, batches, completions, fine_tuning, moderations)
- GitHub issues: one per endpoint group (7 groups), title format and body template specified
- Issues reference stub implementation for traceability

### Claude's Discretion
- Whether to use wildcard route syntax (`/v1/audio/*`) or register each sub-path individually
- Exact wording of the "not yet supported" message
- Whether OPS-02 issues are created via `gh` CLI script or manually documented
- Whether to add a comment in `v1-stubs.ts` linking each stub to its tracking issue

### Deferred Ideas (OUT OF SCOPE)
None -- discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| OPS-01 | Stub endpoints for unsupported OpenAI APIs returning 404 with proper error format and "coming soon" message | `sendApiError()` helper exists with exact signature needed; `v1-plugin.ts` registration pattern established; all endpoint paths enumerated in CONTEXT.md |
| OPS-02 | GitHub issues created for each deferred endpoint group with acceptance criteria | `gh issue create` CLI available; 7 endpoint groups identified (audio, files, uploads, batches, completions, fine_tuning, moderations) |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| fastify | (existing) | HTTP framework, route registration | Already in use throughout project |
| sendApiError | (existing helper) | OpenAI-format error responses | Project's own helper from `api-error.ts` |
| gh CLI | (system) | GitHub issue creation | Standard GitHub CLI, already available |

### Supporting
No new dependencies needed. This phase uses only existing project code and system tools.

## Architecture Patterns

### Recommended Project Structure
```
apps/api/src/routes/
  v1-stubs.ts              # NEW - all stub route handlers
  v1-plugin.ts             # MODIFIED - add registerV1StubRoutes(app) call
  __tests__/
    v1-stubs-compliance.test.ts  # NEW - stub response validation
```

### Pattern 1: Stub Route Handler
**What:** A single function that registers all unsupported endpoint routes with informative 404 responses
**When to use:** For every unsupported OpenAI endpoint
**Example:**
```typescript
// Source: Existing api-error.ts + v1-plugin.ts patterns
import type { FastifyInstance } from "fastify";
import { sendApiError } from "./api-error";

function stubHandler(endpoint: string) {
  return (_request: any, reply: any) => {
    sendApiError(reply, 404,
      `The ${endpoint} endpoint is not yet supported. Please check our roadmap for availability.`,
      { code: "unsupported_endpoint" }
    );
  };
}

export function registerV1StubRoutes(app: FastifyInstance): void {
  // Audio endpoints
  app.post("/audio/speech", stubHandler("/v1/audio/speech"));
  app.post("/audio/transcriptions", stubHandler("/v1/audio/transcriptions"));
  app.post("/audio/translations", stubHandler("/v1/audio/translations"));

  // Files endpoints
  app.get("/files", stubHandler("/v1/files"));
  app.post("/files", stubHandler("/v1/files"));
  app.get("/files/:file_id", stubHandler("/v1/files/:file_id"));
  app.delete("/files/:file_id", stubHandler("/v1/files/:file_id"));
  app.get("/files/:file_id/content", stubHandler("/v1/files/:file_id/content"));

  // ... etc for uploads, batches, completions, fine_tuning, moderations
}
```

### Pattern 2: Registration in v1-plugin.ts
**What:** Add the stub registration call alongside existing route registrations
**Example:**
```typescript
// In v1-plugin.ts, after existing route registrations:
import { registerV1StubRoutes } from "./v1-stubs";

// Inside v1Plugin function, after other registerXxxRoute calls:
registerV1StubRoutes(app);
```

### Pattern 3: Wildcard vs Individual Routes
**Recommendation:** Use individual routes, NOT wildcards.

Rationale:
- Fastify wildcards (`/audio/*`) would catch ALL sub-paths including future real implementations, creating a maintenance hazard
- Individual routes are explicit and self-documenting
- When a real endpoint is implemented later, just remove the corresponding stub -- no wildcard conflict
- The endpoint list is finite and small (20 route registrations total)
- Individual routes allow per-endpoint messages if desired

### Anti-Patterns to Avoid
- **Wildcard catch-alls:** `/v1/audio/*` would conflict with future real endpoint implementations and require careful ordering
- **Middleware-based stubs:** Don't use onRequest hooks to intercept paths -- explicit routes are clearer and work with Fastify's router
- **Dynamic route registration from config:** Over-engineering; the endpoint list is static and small

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| OpenAI error format | Custom JSON construction | `sendApiError()` | Already handles the full `{ error: { message, type, param, code } }` shape |
| GitHub issue creation | Manual issue authoring | `gh issue create` CLI | Scriptable, consistent, includes labels/templates |

## Common Pitfalls

### Pitfall 1: Route Path Prefix
**What goes wrong:** Registering `/v1/audio/speech` inside the v1 plugin scope which already prefixes `/v1`
**Why it happens:** The v1Plugin is registered with prefix `/v1` in the app setup
**How to avoid:** Register as `/audio/speech` (without `/v1` prefix) inside the plugin scope -- consistent with how `registerChatCompletionsRoute` registers `/chat/completions` not `/v1/chat/completions`
**Warning signs:** Routes returning the generic not-found handler response instead of stub response

### Pitfall 2: Error Code vs Error Type Confusion
**What goes wrong:** Setting `type: "unsupported_endpoint"` instead of `code: "unsupported_endpoint"`
**Why it happens:** Confusing the `type` field (which must be a standard OpenAI error type) with the `code` field (which is freeform)
**How to avoid:** Use `sendApiError(reply, 404, message, { code: "unsupported_endpoint" })` -- the helper auto-maps 404 to `type: "not_found_error"` via `STATUS_TO_TYPE`

### Pitfall 3: Missing HTTP Methods
**What goes wrong:** Only stubbing POST for `/v1/files` but forgetting GET
**Why it happens:** OpenAI endpoints support multiple HTTP methods
**How to avoid:** Cross-reference the full method list from CONTEXT.md; test each method explicitly

### Pitfall 4: gh CLI Authentication
**What goes wrong:** `gh issue create` fails because CLI is not authenticated
**Why it happens:** Running in CI or fresh environment
**How to avoid:** Verify `gh auth status` before scripting; if not authenticated, document issues in a markdown file instead

## Code Examples

### Complete Stub Handler with sendApiError
```typescript
// Source: Existing api-error.ts signature
import { sendApiError } from "./api-error";

// sendApiError signature:
// sendApiError(reply, status, message, opts?: { type?, param?, code? })

// For stubs, we only need status=404, code="unsupported_endpoint"
// type auto-resolves to "not_found_error" via STATUS_TO_TYPE[404]
sendApiError(reply, 404,
  "The /v1/audio/speech endpoint is not yet supported. Please check our roadmap for availability.",
  { code: "unsupported_endpoint" }
);

// Expected response body:
// {
//   "error": {
//     "message": "The /v1/audio/speech endpoint is not yet supported. ...",
//     "type": "not_found_error",
//     "param": null,
//     "code": "unsupported_endpoint"
//   }
// }
```

### GitHub Issue Creation Script
```bash
#!/bin/bash
# Create tracking issues for each endpoint group

gh issue create \
  --title "feat: implement /v1/audio (OpenAI compatibility)" \
  --body "$(cat <<'BODY'
## Endpoints
- `POST /v1/audio/speech`
- `POST /v1/audio/transcriptions`
- `POST /v1/audio/translations`

## Reference
- [OpenAI Audio API](https://platform.openai.com/docs/api-reference/audio)
- Stub implementation: `apps/api/src/routes/v1-stubs.ts`

## Acceptance Criteria
- [ ] Endpoint returns valid OpenAI response schema
- [ ] SDK clients (Python, Node, Go) work end-to-end
- [ ] Stub route removed after implementation
BODY
)"
```

### Test Pattern for Stub Compliance
```typescript
// Source: Existing compliance test pattern from Phase 5-8
import { describe, it, expect } from "vitest";
import { sendApiError } from "../../api-error";

describe("OPS-01: Stub endpoint error format", () => {
  it("returns 404 with OpenAI error shape", () => {
    // Test the exact error shape produced by sendApiError
    // Using a mock reply object to capture the response
    const captured: any = {};
    const mockReply = {
      code(n: number) { captured.status = n; return this; },
      send(body: any) { captured.body = body; return this; },
    };

    sendApiError(mockReply as any, 404,
      "The /v1/audio/speech endpoint is not yet supported. Please check our roadmap for availability.",
      { code: "unsupported_endpoint" }
    );

    expect(captured.status).toBe(404);
    expect(captured.body.error).toMatchObject({
      message: expect.stringContaining("not yet supported"),
      type: "not_found_error",
      param: null,
      code: "unsupported_endpoint",
    });
  });
});
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Generic catch-all 404 | Explicit stub routes per endpoint | This phase | Users get actionable "not yet supported" messages instead of "Unknown API route" |
| No tracking of deferred work | GitHub issues per endpoint group | This phase | Future milestones have clear backlog items |

## Open Questions

1. **Route prefix inside v1Plugin scope**
   - What we know: Existing routes like `chat-completions.ts` register without `/v1` prefix (e.g., `/chat/completions`)
   - What's unclear: Need to verify the prefix is `/v1` in the app registration call
   - Recommendation: Check `server.ts` or wherever `v1Plugin` is registered to confirm prefix; register stubs without `/v1` prefix

2. **gh CLI availability and authentication**
   - What we know: `gh` is a standard tool, likely installed
   - What's unclear: Whether it's authenticated in the current environment
   - Recommendation: Test with `gh auth status` before scripting; fall back to documenting issues in markdown if not available

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest (used throughout project) |
| Config file | None project-level -- vitest runs via `pnpm -r test` with defaults |
| Quick run command | `cd apps/api && npx vitest run src/routes/__tests__/v1-stubs-compliance.test.ts` |
| Full suite command | `pnpm test` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| OPS-01 | Each stub returns 404 with OpenAI error format, code=unsupported_endpoint | unit | `cd apps/api && npx vitest run src/routes/__tests__/v1-stubs-compliance.test.ts -x` | Wave 0 |
| OPS-01 | Stub message contains endpoint path and "not yet supported" | unit | Same test file | Wave 0 |
| OPS-01 | All specified endpoints covered (audio, files, uploads, batches, completions, fine_tuning, moderations) | unit | Same test file | Wave 0 |
| OPS-02 | GitHub issues created for 7 endpoint groups | manual-only | `gh issue list --label openai-compat` | N/A (manual verification) |

### Sampling Rate
- **Per task commit:** `cd apps/api && npx vitest run src/routes/__tests__/v1-stubs-compliance.test.ts`
- **Per wave merge:** `pnpm test`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/api/src/routes/__tests__/v1-stubs-compliance.test.ts` -- covers OPS-01 stub error format and completeness

## Sources

### Primary (HIGH confidence)
- `apps/api/src/routes/api-error.ts` -- exact `sendApiError` signature and `STATUS_TO_TYPE` mapping
- `apps/api/src/routes/v1-plugin.ts` -- route registration pattern, error/not-found handler setup
- `apps/api/src/routes/__tests__/differentiators-headers.test.ts` -- compliance test pattern using vitest
- `.planning/phases/09-operational-hardening/09-CONTEXT.md` -- locked decisions, full endpoint list

### Secondary (MEDIUM confidence)
- Fastify route registration behavior (explicit routes take priority over notFoundHandler) -- standard Fastify behavior

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH -- all tools already exist in the project, no new dependencies
- Architecture: HIGH -- follows established patterns from Phases 1-8, single new file
- Pitfalls: HIGH -- known issues (route prefix, method coverage) with clear mitigations

**Research date:** 2026-03-19
**Valid until:** 2026-04-19 (stable -- no external dependencies to change)
