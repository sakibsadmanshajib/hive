---
phase: 20-provider-catalog
plan: 02
type: execute
wave: 2
depends_on: [20-01]
size: M
branch: b/phase-20-provider-catalog
milestone: v1.1
track: A
files_modified:
  - apps/control-plane/internal/providers/repository.go
  - apps/control-plane/internal/providers/service.go
  - apps/control-plane/internal/providers/http.go
  - apps/control-plane/internal/providers/http_test.go
  - apps/control-plane/cmd/server/main.go
autonomous: true
---

# Plan 20-02 — Provider CRUD Package + Internal Endpoints

## Objective

Implement a new `internal/providers` Go package in `apps/control-plane` that provides full CRUD for `custom_providers` rows, exposed via internal HTTP endpoints protected by shared-secret auth. Platform admins can also reach these endpoints using `platform.RequirePlatformAdmin`.

---

## Architecture

```
POST   /internal/providers          create provider
GET    /internal/providers          list providers
GET    /internal/providers/{id}     get provider
PUT    /internal/providers/{id}     update provider
DELETE /internal/providers/{id}     delete provider (soft: sets enabled=false)
```

Auth: shared-secret header `X-Internal-Secret: <INTERNAL_SECRET>` (same mechanism as `platform/http/internalauth.go`). Platform admins (`platform.RequirePlatformAdmin`) may also access these routes via the standard JWT path.

---

## Tasks

### Task 1: Repository layer

**File:** `apps/control-plane/internal/providers/repository.go`

```go
package providers

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jmoiern/sqlx"  // match existing DB driver in control-plane
)

type Provider struct {
    ID            uuid.UUID `db:"id"`
    Slug          string    `db:"slug"`
    DisplayName   string    `db:"display_name"`
    BaseURL       string    `db:"base_url"`
    APIKeyEnv     string    `db:"api_key_env"`
    LiteLLMPrefix string    `db:"litellm_prefix"`
    Enabled       bool      `db:"enabled"`
    CreatedAt     time.Time `db:"created_at"`
    UpdatedAt     time.Time `db:"updated_at"`
}

type Repository interface {
    Create(ctx context.Context, p Provider) (Provider, error)
    List(ctx context.Context) ([]Provider, error)
    Get(ctx context.Context, id uuid.UUID) (Provider, error)
    Update(ctx context.Context, id uuid.UUID, p Provider) (Provider, error)
    Delete(ctx context.Context, id uuid.UUID) error   // soft: enabled=false
}
```

Implement `pgRepository` backed by the existing `*sqlx.DB` connection (match how `apps/control-plane/internal/routing/repository.go` receives its DB handle via constructor injection). Use parameterized queries only (no `fmt.Sprintf` with user data).

---

### Task 2: Service layer

**File:** `apps/control-plane/internal/providers/service.go`

Thin service wrapping the repository. Responsibilities:

- Validate input (slug non-empty, base_url valid URL scheme, api_key_env non-empty).
- Return typed errors (`ErrNotFound`, `ErrSlugConflict`) that the HTTP handler maps to 404/409.
- No business logic beyond validation and delegation to Repository.

---

### Task 3: HTTP handler

**File:** `apps/control-plane/internal/providers/http.go`

- Use the same `chi` router pattern as existing control-plane handlers.
- Request/response structs use `encoding/json`; no external serialization library unless already present.
- All handler methods return structured JSON errors: `{"error": "...", "code": "..."}`.
- `DELETE` is a soft delete (sets `enabled = false`); return 200 with the updated record.

Request body for create/update:

```json
{
  "slug":           "together",
  "display_name":   "Together AI",
  "base_url":       "https://api.together.xyz/v1",
  "api_key_env":    "TOGETHER_API_KEY",
  "litellm_prefix": "together_ai/",
  "enabled":        true
}
```

---

### Task 4: Auth middleware wiring

Reuse `platform/http/internalauth.go` `InternalAuthMiddleware` (shared-secret). Mount the providers router under `/internal/providers` inside the internal-only sub-router already guarded by that middleware. Do NOT expose these endpoints on the public-facing router.

Also accept `platform.RequirePlatformAdmin` as a second auth path: mount a parallel route group under `/api/v1/admin/providers` protected by `platform.RequirePlatformAdmin`. Both groups call the same handler functions.

**File:** `apps/control-plane/cmd/server/main.go` (or wherever routes are registered).

---

### Task 5: Unit tests

**File:** `apps/control-plane/internal/providers/http_test.go`

TDD: write tests before handler implementation.

Required cases:

1. `POST /internal/providers` with valid body returns 201 + provider JSON.
2. `POST /internal/providers` with duplicate slug returns 409.
3. `POST /internal/providers` with empty slug returns 400.
4. `GET /internal/providers` returns array including seeded openrouter + groq rows.
5. `PUT /internal/providers/{id}` updates `display_name`; returns 200 with updated record.
6. `DELETE /internal/providers/{id}` sets `enabled=false`; subsequent GET shows `enabled: false`.
7. Request without shared-secret header returns 401.

Use a test DB or mock repository that satisfies the `Repository` interface (prefer interface mock over real DB in unit tests; integration test in 20-06 hits real DB).

---

## TDD Notes

RED first: write `http_test.go` stubs that fail compilation. GREEN: implement handler. IMPROVE: ensure no `fmt.Sprintf` with user input, no unhandled errors, all paths return structured JSON.

---

## Acceptance Criteria

- [ ] `apps/control-plane/internal/providers/` package compiles with `go build ./apps/control-plane/...`.
- [ ] `Repository` interface satisfied by `pgRepository` with parameterized queries.
- [ ] Internal endpoints mounted under `/internal/providers` behind shared-secret middleware.
- [ ] Admin endpoints mounted under `/api/v1/admin/providers` behind `platform.RequirePlatformAdmin`.
- [ ] All 7 unit test cases pass.
- [ ] `go vet ./apps/control-plane/...` clean.
- [ ] No hardcoded provider slugs anywhere in the new package.
