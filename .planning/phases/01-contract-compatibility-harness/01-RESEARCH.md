# Phase 1: Contract & Compatibility Harness - Research

**Researched:** 2026-03-28
**Domain:** OpenAI contract import, Go OpenAPI codegen, SDK compatibility testing, Docker-only development workflow
**Confidence:** HIGH

## Summary

Phase 1 is a foundational phase that establishes Hive's compatibility contract before any business logic exists. The core work is: (1) importing and versioning the official OpenAI OpenAPI spec, (2) generating Go server types from it, (3) building a support matrix that classifies every public non-org/admin endpoint, (4) creating a compatibility harness that proves official SDK behavior against the implemented subset, (5) ensuring unsupported endpoints return strict OpenAI-style errors, (6) publishing Swagger/OpenAPI docs, and (7) containerizing the entire developer workflow.

The OpenAI spec lives at `github.com/openai/openai-openapi` on the `manual_spec` branch as a ~1.3MB `openapi.yaml` file. There are no recent formal releases (last tagged 2.0.0 in June 2023), so Hive should pin to a specific commit SHA. The Go codegen ecosystem offers two strong options: `ogen` (v1.20.2, high-performance, strongly typed) and `oapi-codegen` (v2.6.0, simpler, more flexible). The OpenAI spec is large and uses complex oneOf/anyOf patterns extensively, so codegen tooling must be validated against the actual spec early -- partial generation with overlays is the pragmatic path.

**Primary recommendation:** Import the OpenAI spec pinned to a commit SHA, create Hive overlay documents to mark support status and trim unsupported operations, generate Go server stubs with `ogen` (falling back to `oapi-codegen` for problematic endpoints), build SDK compatibility tests using official OpenAI JS/Python/Java SDKs pointed at a local Hive stub server, and containerize everything with Docker Compose `develop.watch`.

<user_constraints>

## User Constraints (from CONTEXT.md)

### Locked Decisions

- Publish an endpoint-by-endpoint matrix for the full public non-org/admin OpenAI surface rather than a partial or family-only summary.
- Treat the long-term launch target as a near-full public mirror, even though the `supported now` subset in Phase 1 will remain narrow.
- Use four explicit statuses in the public matrix: `supported now`, `planned for launch`, `explicitly unsupported at launch`, and `out of scope` for org/admin endpoints.
- The support matrix must distinguish future launch intent from current implementation status; Phase 1 must not blur those together.
- Any public endpoint marked as not currently supported must return strict OpenAI-style unsupported errors rather than best-effort fallbacks or generic placeholder failures.
- Unsupported parameters, modes, or feature combinations on otherwise-supported endpoints must also fail explicitly with OpenAI-style errors instead of being silently ignored.
- Error messaging should be customer-clear but provider-blind: explain what capability is unavailable without exposing upstream provider identity or internal routing constraints.
- The published support matrix is authoritative for runtime behavior; if the matrix says a capability is unsupported or only planned, the runtime must reject it consistently until the matrix changes.
- Phase 1 should use a deep compatibility verification standard for official OpenAI JavaScript/TypeScript, Python, and Java SDKs rather than minimal smoke tests.
- Streaming compatibility must be proven with golden regression cases that cover event ordering, chunk shape, terminal events, and interruption or failure behavior.
- Compatibility proof must include error-path and unsupported-path fidelity, including HTTP status behavior, error object shape, compatibility headers, and explicit unsupported responses.
- A failing compatibility harness blocks Hive from claiming the affected endpoint or status as supported.
- Public documentation should expose an endpoint-by-endpoint reference table rather than relying on prose or family-only summaries.
- Each matrix row should include the endpoint or method, current status, brief support notes, and later-phase linkage when full implementation belongs to a later phase.
- Endpoint support and model support should be treated as separate views; model-level readiness or health must not be mixed into the endpoint matrix.
- Swagger/OpenAPI is the source for request and response shape, but the support matrix is the authoritative source of support status.

### Claude's Discretion

- No additional product-scope decisions were delegated during discussion.
- Downstream agents may choose the exact codegen tools, test harness structure, documentation rendering approach, and internal implementation details as long as they preserve the matrix/status model, provider-blind unsupported behavior, and the high compatibility proof bar above.

### Deferred Ideas (OUT OF SCOPE)

- Add a provider-blind per-model health or support view separate from the endpoint matrix. This is valuable, but it is a separate capability from Phase 1's endpoint contract and documentation work.

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| COMP-01 | Developer can use official OpenAI JS/TS, Python, and Java SDKs against Hive by changing only base URL and API key for supported endpoints. | OpenAI SDKs (Node v6.33.0, Python v2.30.0, Java v4.30.0) require only `base_url` override. Codegen from official spec ensures request/response shape fidelity. SDK compatibility harness validates drop-in behavior. |
| COMP-02 | Hive returns OpenAI-style HTTP status codes, error objects, and compatibility headers for both supported requests and explicit unsupported-feature responses. | OpenAI error format is `{"error": {"message": "...", "type": "...", "code": "..."}}` with standard HTTP status codes (400, 401, 403, 404, 429, 500). Unsupported endpoints should return 404 or 400 with clear messages. Compatibility headers include `openai-organization`, `openai-processing-ms`, `openai-version`, `x-request-id`. |
| COMP-03 | Developer can browse Swagger/OpenAPI documentation that matches the Hive public API contract and supported launch surface. | Swagger UI can be embedded in Go using `swaggest/swgui` or static embed. The Hive overlay spec (not raw OpenAI spec) should be the doc source, showing only what Hive exposes with correct support annotations. |
| API-08 | Public non-org/admin endpoints outside the initial launch subset are explicitly classified and return OpenAI-style unsupported responses until implemented. | The support matrix with four statuses drives a catch-all middleware that intercepts requests to classified-but-unsupported paths and returns structured OpenAI error responses. |

</phase_requirements>

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go | 1.24+ (current stable) | Edge API server, codegen host | Stack decision from project research; verify exact version at implementation time |
| OpenAI OpenAPI spec | `manual_spec` branch, pinned SHA | Canonical contract source | Official spec from `github.com/openai/openai-openapi`; ~1.3MB YAML; no recent tagged releases, pin to commit SHA for reproducibility |
| ogen | v1.20.2 | Primary Go server/client codegen from OpenAPI v3 | Generates strongly-typed handlers with no reflect/interface{}, sum types for oneOf, high performance routing and validation; actively maintained (released 2026-03-27) |
| oapi-codegen | v2.6.0 | Secondary/fallback Go codegen | Simpler generation path, supports chi/echo/net-http servers, useful for endpoints where ogen's strict typing is harder to overlay; released 2026-02-27 |
| Docker Compose | Current stable | Local orchestration | `develop.watch` feature provides sync/rebuild/restart actions for hot-reload without host toolchain |
| air | v1.64.5 | Go hot-reload inside containers | Watches Go files and recompiles on change; pairs with Docker Compose watch for the outer sync layer |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| OpenAI Node SDK | v6.33.0 | JS/TS compatibility testing | Point at local Hive stub with `baseURL` override for SDK regression harness |
| OpenAI Python SDK | v2.30.0 | Python compatibility testing | Point at local Hive stub with `base_url` override for SDK regression harness |
| OpenAI Java SDK | v4.30.0 | Java compatibility testing | Point at local Hive stub with `baseUrl` override for SDK regression harness |
| swaggest/swgui | v5 | Embedded Swagger UI for Go | Serve Hive's OpenAPI spec as browsable documentation inside the edge API |
| OpenAPI Overlay Spec | v1.1.0 | Spec augmentation without forking | Apply Hive-specific annotations (support status, custom descriptions) on top of imported OpenAI spec |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| ogen (primary codegen) | oapi-codegen only | oapi-codegen is simpler but generates less type-safe code with more interface{} usage; ogen's strict typing is better for contract fidelity but may struggle with edge cases in the large OpenAI spec |
| OpenAPI Overlay Spec | Manual spec fork/edit | Overlays keep the upstream spec untouched and diffs reviewable; manual edits create merge conflicts on spec updates |
| air (Go hot reload) | Docker Compose watch sync+restart only | air recompiles Go on file change inside the container; Compose watch alone only syncs files but does not trigger Go rebuild |
| swaggest/swgui | Standalone Swagger UI container | Embedded approach is simpler for a single Go binary; standalone container adds operational complexity for dev but may be useful in production |

**Installation (all containerized):**
```bash
# No host installs needed -- everything runs in Docker
docker compose up --watch    # Start full dev stack with file sync
docker compose run --rm toolchain go generate ./...  # Run codegen
docker compose run --rm sdk-tests npm test           # JS SDK tests
docker compose run --rm sdk-tests-py pytest           # Python SDK tests
docker compose run --rm sdk-tests-java ./gradlew test # Java SDK tests
```

## Architecture Patterns

### Recommended Project Structure (Phase 1 scope)

```
platform/
├── packages/
│   ├── openai-contract/
│   │   ├── upstream/
│   │   │   └── openapi.yaml          # Pinned copy of OpenAI spec (commit SHA tracked)
│   │   ├── overlays/
│   │   │   ├── hive-support-status.yaml  # Overlay: marks support status per endpoint
│   │   │   └── hive-descriptions.yaml    # Overlay: Hive-specific descriptions
│   │   ├── generated/
│   │   │   └── openapi-hive.yaml     # Merged spec after overlays applied
│   │   ├── matrix/
│   │   │   └── support-matrix.json   # Machine-readable endpoint classification
│   │   └── scripts/
│   │       ├── import-spec.sh        # Fetch + pin upstream spec
│   │       ├── apply-overlays.sh     # Merge overlays into generated spec
│   │       └── generate-matrix.sh    # Extract matrix from annotated spec
│   └── sdk-tests/
│       ├── js/                       # Node SDK compatibility tests
│       ├── python/                   # Python SDK compatibility tests
│       ├── java/                     # Java SDK compatibility tests
│       └── fixtures/
│           ├── golden/               # Golden response fixtures for regression
│           └── streaming/            # SSE event sequence fixtures
├── apps/
│   └── edge-api/
│       ├── cmd/server/main.go        # Entry point
│       ├── internal/
│       │   ├── generated/            # ogen/oapi-codegen output
│       │   ├── handler/              # Business logic (stub responses for Phase 1)
│       │   ├── middleware/
│       │   │   ├── unsupported.go    # Catch-all for unsupported endpoints
│       │   │   └── compat_headers.go # OpenAI compatibility headers
│       │   └── errors/
│       │       └── openai.go         # OpenAI error object builder
│       ├── docs/
│       │   └── swagger.go            # Embedded Swagger UI handler
│       └── go.mod
├── deploy/
│   └── docker/
│       ├── docker-compose.yml        # Full dev stack
│       ├── docker-compose.override.yml # Watch/dev overrides
│       ├── Dockerfile.edge-api       # Go build + air for dev
│       ├── Dockerfile.toolchain      # Codegen tools (ogen, oapi-codegen, overlay tools)
│       ├── Dockerfile.sdk-tests-js   # Node + OpenAI SDK
│       ├── Dockerfile.sdk-tests-py   # Python + OpenAI SDK
│       └── Dockerfile.sdk-tests-java # Java + OpenAI SDK
└── docs/
    └── support-matrix.md            # Human-readable endpoint reference table
```

### Pattern 1: Contract-First with Overlay

**What:** Import the upstream OpenAI spec verbatim, apply Hive overlays to annotate support status and customize descriptions, then generate server types from the merged result.
**When to use:** Every time the upstream spec is updated or Hive's support scope changes.
**Example:**
```yaml
# overlays/hive-support-status.yaml (OpenAPI Overlay v1.1.0)
overlay: 1.1.0
info:
  title: Hive Support Status
  version: 0.1.0
actions:
  - target: "$.paths['/v1/chat/completions'].post"
    update:
      x-hive-status: "supported_now"
      x-hive-phase: 6
  - target: "$.paths['/v1/images/generations'].post"
    update:
      x-hive-status: "planned_for_launch"
      x-hive-phase: 7
  - target: "$.paths['/v1/fine_tuning/jobs'].post"
    update:
      x-hive-status: "explicitly_unsupported_at_launch"
```

### Pattern 2: Support-Matrix-Driven Middleware

**What:** A middleware reads the machine-readable support matrix at startup and intercepts requests to paths not marked `supported_now`, returning OpenAI-style error responses.
**When to use:** Every request to the edge API passes through this middleware.
**Example:**
```go
// internal/middleware/unsupported.go
func UnsupportedEndpointMiddleware(matrix *SupportMatrix) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            status := matrix.Lookup(r.Method, r.URL.Path)
            if status != StatusSupportedNow {
                writeOpenAIError(w, http.StatusNotFound, "unsupported_endpoint",
                    fmt.Sprintf("The endpoint %s %s is not currently supported.",
                        r.Method, r.URL.Path))
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### Pattern 3: OpenAI Error Envelope

**What:** All error responses use the exact OpenAI error JSON shape so SDKs parse them correctly.
**When to use:** Every error path in the edge API.
**Example:**
```go
// internal/errors/openai.go
type OpenAIError struct {
    Error OpenAIErrorBody `json:"error"`
}

type OpenAIErrorBody struct {
    Message string  `json:"message"`
    Type    string  `json:"type"`
    Param   *string `json:"param"`
    Code    *string `json:"code"`
}

func WriteError(w http.ResponseWriter, httpStatus int, errType, message string, code *string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(httpStatus)
    json.NewEncoder(w).Encode(OpenAIError{
        Error: OpenAIErrorBody{
            Message: message,
            Type:    errType,
            Code:    code,
        },
    })
}
```

### Anti-Patterns to Avoid

- **Forking the OpenAI spec directly:** Edit overlays instead; direct edits create unmergeable diffs when the upstream spec updates.
- **Generating code for the entire spec at once:** The OpenAI spec is ~1.3MB with hundreds of endpoints; generate only what Hive needs per phase, using overlays to scope.
- **Mixing support status into runtime config:** The support matrix should be a build-time artifact derived from the spec overlays, not a runtime config file that can drift from the published docs.
- **Testing with curl instead of official SDKs:** curl tests prove HTTP shape but miss SDK-specific parsing, retry behavior, and type validation. Always test with the real SDKs.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| OpenAI request/response types | Hand-written Go structs for 100+ endpoints | `ogen` or `oapi-codegen` from the official spec | Types drift from spec; codegen guarantees structural correctness |
| OpenAPI spec customization | Fork and edit the OpenAI YAML directly | OpenAPI Overlay Specification v1.1.0 | Overlays are additive and composable; forks create merge conflicts |
| Swagger documentation UI | Custom docs page | `swaggest/swgui` embedded or Swagger UI static assets | Battle-tested rendering with try-it-out functionality |
| Go hot reload | Custom file watcher + rebuild script | `air` v1.64.5 inside container | Handles build errors, binary restart, and ignore patterns |
| Dev environment orchestration | Shell scripts for each service | Docker Compose with `develop.watch` | Declarative sync/rebuild/restart with native file watching |

**Key insight:** Phase 1 is almost entirely a codegen, classification, and testing problem. The only custom code is the unsupported-endpoint middleware, the error envelope helper, and the compatibility header middleware. Everything else should be generated or imported.

## Common Pitfalls

### Pitfall 1: ogen Fails on Complex OpenAI Spec Constructs

**What goes wrong:** The OpenAI spec uses deeply nested oneOf/anyOf, polymorphic request bodies, and complex discriminator patterns. ogen may fail to generate valid Go code for some of these.
**Why it happens:** The spec was written for Stainless (OpenAI's codegen tool), not for general-purpose generators.
**How to avoid:** Run ogen against the full spec early in Wave 0. Identify failing endpoints. For those, either: (a) use oapi-codegen as fallback, (b) create a trimmed overlay that removes the problematic constructs, or (c) hand-write types for a small number of complex endpoints. Document which strategy was used per endpoint.
**Warning signs:** ogen exits with errors referencing specific schema paths; generated code has compilation errors.

### Pitfall 2: Support Matrix Drifts from Runtime Behavior

**What goes wrong:** The published matrix says an endpoint is unsupported, but the middleware lets requests through (or vice versa).
**Why it happens:** The matrix is maintained manually and the middleware reads a different source of truth.
**How to avoid:** Generate the middleware's route table from the same machine-readable matrix that produces the documentation. Single source of truth. Test that every matrix entry matches runtime behavior.
**Warning signs:** SDK tests pass for endpoints that should be blocked; documentation shows different status than runtime.

### Pitfall 3: SDK Version Skew Breaks Tests

**What goes wrong:** Compatibility tests pass with one SDK version but fail with the latest because OpenAI added new required fields or changed defaults.
**Why it happens:** SDK versions are not pinned, or golden fixtures were recorded against an older API version.
**How to avoid:** Pin SDK versions in lockfiles. Record the OpenAI API version each golden fixture targets. Re-record fixtures when upgrading SDKs.
**Warning signs:** Tests break after dependency updates without any Hive code changes.

### Pitfall 4: Streaming Tests Are Flaky or Incomplete

**What goes wrong:** SSE event ordering, chunk shape, or terminal event tests pass intermittently or only cover the happy path.
**Why it happens:** Streaming tests are harder to write deterministically; teams skip error/interruption cases.
**How to avoid:** Use golden SSE event sequences as fixtures. Test: (a) normal completion, (b) chunk shape per event, (c) terminal `[DONE]` event, (d) mid-stream error, (e) client disconnect. Use deterministic stub responses, not live upstream calls.
**Warning signs:** Tests pass locally but fail in CI; no tests for error or interruption cases.

### Pitfall 5: Docker Dev Environment Is Slow or Fragile

**What goes wrong:** Go compilation inside Docker is slow; file sync misses changes; containers need manual restart.
**Why it happens:** Volume mounts without proper caching, missing Go module cache persistence, or misconfigured watch paths.
**How to avoid:** Use Docker Compose `develop.watch` with `sync` action for source files and `rebuild` for dependency files. Mount a named volume for the Go module cache. Use air inside the container for fast incremental rebuilds.
**Warning signs:** >10 second rebuild cycle; developers bypass Docker and install Go locally.

## Code Examples

### Docker Compose with Watch for Go Development

```yaml
# deploy/docker/docker-compose.yml
services:
  edge-api:
    build:
      context: ../../
      dockerfile: deploy/docker/Dockerfile.edge-api
    ports:
      - "8080:8080"
    volumes:
      - gomodcache:/go/pkg/mod
      - gobuildcache:/root/.cache/go-build
    develop:
      watch:
        - action: sync
          path: ./apps/edge-api
          target: /app/apps/edge-api
        - action: sync
          path: ./packages/openai-contract/generated
          target: /app/packages/openai-contract/generated
        - action: rebuild
          path: ./apps/edge-api/go.mod

  toolchain:
    build:
      context: ../../
      dockerfile: deploy/docker/Dockerfile.toolchain
    profiles: ["tools"]
    volumes:
      - ../../:/workspace
      - gomodcache:/go/pkg/mod

volumes:
  gomodcache:
  gobuildcache:
```

### Dockerfile for Go Edge API with Air

```dockerfile
# deploy/docker/Dockerfile.edge-api
FROM golang:1.24-alpine AS base
RUN go install github.com/air-verse/air@v1.64.5
WORKDIR /app
COPY go.work go.work.sum ./
COPY apps/edge-api/go.mod apps/edge-api/go.sum ./apps/edge-api/
COPY packages/ ./packages/
RUN cd apps/edge-api && go mod download
COPY apps/edge-api/ ./apps/edge-api/
CMD ["air", "-c", "apps/edge-api/.air.toml"]
```

### SDK Compatibility Test Structure (Node)

```typescript
// packages/sdk-tests/js/tests/errors/unsupported-endpoint.test.ts
import OpenAI from "openai";
import { describe, it, expect } from "vitest";

const client = new OpenAI({
  baseURL: process.env.HIVE_BASE_URL ?? "http://localhost:8080/v1",
  apiKey: "test-key",
});

describe("unsupported endpoint returns OpenAI-style error", () => {
  it("returns 404 with error object for fine-tuning", async () => {
    try {
      await client.fineTuning.jobs.create({
        model: "gpt-4o",
        training_file: "file-abc123",
      });
      expect.unreachable("should have thrown");
    } catch (err) {
      expect(err).toBeInstanceOf(OpenAI.NotFoundError);
      expect(err.status).toBe(404);
      expect(err.error?.error?.type).toBe("unsupported_endpoint");
      expect(err.error?.error?.message).toContain("not currently supported");
    }
  });
});
```

### OpenAI Compatibility Headers Middleware

```go
// internal/middleware/compat_headers.go
func CompatHeaders(requestIDGenerator func() string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            reqID := requestIDGenerator()

            w.Header().Set("x-request-id", reqID)
            w.Header().Set("openai-version", "2020-10-01")

            rec := &responseRecorder{ResponseWriter: w}
            next.ServeHTTP(rec, r)

            w.Header().Set("openai-processing-ms",
                fmt.Sprintf("%d", time.Since(start).Milliseconds()))
        })
    }
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| OpenAI Chat Completions as primary API | Responses API is the new recommended API | 2025 | Hive must support both; Responses API will eventually supersede Chat Completions |
| Assistants API for multi-step workflows | Responses API with tools | Deprecated Aug 2026 | Do NOT implement Assistants API; it is being removed |
| Manual OpenAPI spec editing | OpenAPI Overlay Spec v1.1.0 | Oct 2024 (v1.0.0) | Use overlays instead of forking specs |
| `deepmap/oapi-codegen` import path | `oapi-codegen/oapi-codegen` v2 | May 2024 | Use the new import path `github.com/oapi-codegen/oapi-codegen/v2` |
| Docker volume mounts for dev | Docker Compose `develop.watch` | 2024 GA | Use watch actions (sync/rebuild/restart) instead of raw bind mounts |

**Deprecated/outdated:**
- **Assistants API**: Being removed August 2026. Do not implement.
- **Completions API** (`/v1/completions`): Legacy. Still in spec but deprecated in favor of Chat Completions and Responses.
- **`deepmap/oapi-codegen`**: Old org name. Use `github.com/oapi-codegen/oapi-codegen/v2`.

## Open Questions

1. **How well does ogen handle the full OpenAI spec?**
   - What we know: ogen supports oneOf/anyOf with discriminator inference and generates strongly-typed code. It is actively maintained (v1.20.2, released 2026-03-27).
   - What's unclear: Whether the ~1.3MB OpenAI spec with its complex polymorphic types generates cleanly without errors. No public evidence of ogen being used against this specific spec.
   - Recommendation: Run ogen against the spec in Wave 0 as a validation task. Have oapi-codegen ready as fallback. Document which endpoints need which generator.

2. **What is the exact list of public non-org/admin endpoints to classify?**
   - What we know: Major families include responses, chat/completions, completions, embeddings, images, audio, files, uploads, batches, vector_stores, fine_tuning, realtime, videos, moderations, models.
   - What's unclear: The exact path-by-path inventory needs to be extracted from the spec programmatically.
   - Recommendation: Parse the imported spec YAML to extract all paths and methods. Classify each against the four-status model. This is a plan task, not a pre-research activity.

3. **Which OpenAI response headers do SDKs depend on?**
   - What we know: `x-request-id`, `openai-processing-ms`, `openai-version`, and `openai-organization` are commonly referenced. SDKs use `x-request-id` for error reporting.
   - What's unclear: Whether SDKs fail hard on missing headers or degrade gracefully.
   - Recommendation: Test with missing headers in the SDK compatibility harness. Add headers incrementally based on what breaks.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework (Go) | `go test` with standard library |
| Framework (JS SDK tests) | Vitest 3.x |
| Framework (Python SDK tests) | pytest 8.x |
| Framework (Java SDK tests) | JUnit 5 + Gradle |
| Config file | None yet -- Wave 0 |
| Quick run command | `docker compose run --rm edge-api go test ./... -short` |
| Full suite command | `docker compose run --rm sdk-tests-js npm test && docker compose run --rm sdk-tests-py pytest && docker compose run --rm sdk-tests-java ./gradlew test && docker compose run --rm edge-api go test ./...` |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| COMP-01 | Official SDKs work with base URL change | integration | `docker compose run --rm sdk-tests-js npm test` | No -- Wave 0 |
| COMP-01 | Official SDKs work with base URL change | integration | `docker compose run --rm sdk-tests-py pytest` | No -- Wave 0 |
| COMP-01 | Official SDKs work with base URL change | integration | `docker compose run --rm sdk-tests-java ./gradlew test` | No -- Wave 0 |
| COMP-02 | Error responses match OpenAI shape | unit | `docker compose run --rm edge-api go test ./internal/errors/... -run TestOpenAIError` | No -- Wave 0 |
| COMP-02 | Unsupported endpoints return correct errors | integration | `docker compose run --rm sdk-tests-js npm test -- --grep "unsupported"` | No -- Wave 0 |
| COMP-02 | Compatibility headers present | integration | `docker compose run --rm sdk-tests-js npm test -- --grep "headers"` | No -- Wave 0 |
| COMP-03 | Swagger UI serves and renders spec | smoke | `curl -sf http://localhost:8080/docs/ | grep -q swagger-ui` | No -- Wave 0 |
| API-08 | All non-supported public endpoints return structured errors | integration | `docker compose run --rm sdk-tests-js npm test -- --grep "unsupported"` | No -- Wave 0 |

### Sampling Rate

- **Per task commit:** `docker compose run --rm edge-api go test ./... -short`
- **Per wave merge:** Full suite across all SDK languages + Go unit tests
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `deploy/docker/docker-compose.yml` -- Docker Compose orchestration for all services
- [ ] `deploy/docker/Dockerfile.edge-api` -- Go dev image with air
- [ ] `deploy/docker/Dockerfile.toolchain` -- Codegen tools container
- [ ] `deploy/docker/Dockerfile.sdk-tests-js` -- Node + OpenAI SDK + Vitest
- [ ] `deploy/docker/Dockerfile.sdk-tests-py` -- Python + OpenAI SDK + pytest
- [ ] `deploy/docker/Dockerfile.sdk-tests-java` -- Java + OpenAI SDK + JUnit/Gradle
- [ ] `apps/edge-api/go.mod` -- Go module initialization
- [ ] `packages/sdk-tests/js/package.json` -- JS test project with vitest + openai SDK
- [ ] `packages/sdk-tests/python/pyproject.toml` -- Python test project with pytest + openai SDK
- [ ] `packages/sdk-tests/java/build.gradle` -- Java test project with JUnit + openai SDK
- [ ] `packages/openai-contract/upstream/openapi.yaml` -- Imported spec (pinned SHA)

## Sources

### Primary (HIGH confidence)

- [github.com/openai/openai-openapi](https://github.com/openai/openai-openapi) - Official spec repo; confirmed `manual_spec` branch with `openapi.yaml` (~1.3MB); last tagged release 2.0.0 (2023-06-19) but branch actively maintained
- [github.com/ogen-go/ogen](https://github.com/ogen-go/ogen) - Confirmed v1.20.2 released 2026-03-27; supports oneOf/anyOf with discriminator inference
- [github.com/oapi-codegen/oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) - Confirmed v2.6.0 released 2026-02-27; supports chi/echo/net-http servers
- [github.com/openai/openai-node](https://github.com/openai/openai-node) - Confirmed v6.33.0 released 2026-03-25
- [github.com/openai/openai-python](https://github.com/openai/openai-python) - Confirmed v2.30.0 released 2026-03-25
- [github.com/openai/openai-java](https://github.com/openai/openai-java) - Confirmed v4.30.0 released 2026-03-25
- [github.com/air-verse/air](https://github.com/air-verse/air) - Confirmed v1.64.5 released 2026-02-02
- [Docker Compose Watch docs](https://docs.docker.com/compose/how-tos/file-watch/) - develop.watch with sync/rebuild/restart actions
- [OpenAPI Overlay Spec v1.1.0](https://spec.openapis.org/overlay/latest.html) - Official overlay mechanism for spec augmentation

### Secondary (MEDIUM confidence)

- [OpenAI API error codes guide](https://platform.openai.com/docs/guides/error-codes) - Error object shape `{error: {message, type, param, code}}`; HTTP status code mapping
- [OpenAI API Reference](https://developers.openai.com/api/reference/) - Full endpoint listing including responses, chat/completions, embeddings, images, audio, files, etc.
- [OpenAI Assistants deprecation](https://learn.microsoft.com/en-gb/answers/questions/5571874/openai-assistants-api-will-be-deprecated-in-august) - Assistants API deprecated August 2026
- [swaggest/swgui](https://github.com/swaggest/swgui) - Embedded Swagger UI for Go with native embed support

### Tertiary (LOW confidence)

- [OpenAI gpt-oss verification cookbook](https://developers.openai.com/cookbook/articles/gpt-oss/verifying-implementations/) - Referenced in project research for API shape verification patterns; could not fetch content directly; verify methodology during implementation

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - All versions confirmed via GitHub releases API within 24 hours
- Architecture: HIGH - Project structure follows prior research (ARCHITECTURE.md, STACK.md) validated against current tooling
- Pitfalls: HIGH - Primary pitfall (compatibility by approximation) directly addressed by contract-first approach; codegen risk is the main unknown
- Validation: MEDIUM - Test framework choices are standard but none exist yet; Wave 0 setup is significant

**Research date:** 2026-03-28
**Valid until:** 2026-04-28 (stable domain; spec and SDK versions move frequently but patterns are stable)
