---
phase: 20-provider-catalog
plan: 04
type: execute
wave: 3
depends_on: [20-01, 20-02, 20-03]
size: M
branch: b/phase-20-provider-catalog
milestone: v1.1
track: A
files_modified:
  - apps/control-plane/internal/catalog/service.go
  - apps/control-plane/internal/catalog/service_test.go
  - apps/control-plane/internal/catalog/http.go
  - apps/control-plane/internal/owui/client.go
  - apps/control-plane/internal/owui/client_test.go
autonomous: true
---

# Plan 20-04 — Tenant Model Visibility + Open WebUI Access Control Sync

## Objective

Filter the public catalog endpoint so each tenant sees only the model aliases they are permitted to use, and synchronize those visibility rules to Open WebUI's per-model `access_control` objects so the chat surface enforces the same constraint.

---

## Context (Verified Facts)

- `GET /api/v1/catalog/models` currently has no tenant filter; `ModelAlias.Visibility` field exists but is unused in filtering.
- Open WebUI: `ENABLE_MODEL_FILTER` + `MODEL_FILTER_LIST` are deprecated/removed. Current mechanism: per-model `access_control` objects via admin API `POST/GET /api/v1/models/` with `{read: {group_ids: [...], user_ids: [...]}, write: {...}}`. `null` means public.
- `apps/control-plane/internal/owui/client.go` already has `EnsureGroup` and `AddUserToGroup`. This plan adds model upsert with `access_control` keyed by tenant group ID.

> **Schema prerequisite:** The `model_aliases.visibility` CHECK currently allows only `public`, `preview`, and `internal` (see `supabase/migrations/20260331_01_model_catalog.sql`). The `"restricted"` value used in the filtering rules below does not yet exist. The phase-20 migration (Plan 20-01 or a dedicated migration step) **must** add `'restricted'` to that CHECK before this service code is deployed:
>
> ```sql
> ALTER TABLE public.model_aliases
>   DROP CONSTRAINT IF EXISTS model_aliases_visibility_check;
> ALTER TABLE public.model_aliases
>   ADD CONSTRAINT model_aliases_visibility_check
>     CHECK (visibility IN ('public', 'preview', 'internal', 'restricted'));
> ```
>
> Add this DDL to `YYYYMMDD_01_phase20_provider_catalog_schema.sql` (Plan 20-01 Task 1.5) and add the acceptance criterion: `model_aliases.visibility` CHECK accepts `'restricted'`.

---

## Tasks

### Task 1: Catalog service tenant filtering

**File:** `apps/control-plane/internal/catalog/service.go`

Read the existing file before editing. Current `ListModels` (or equivalent) returns all aliases with `visibility = 'public'` or all aliases with no filter. Update the signature to accept a `tenantID uuid.UUID` parameter:

```go
// ListModelsForTenant returns aliases the tenant is permitted to use.
// Rules (in order):
//   1. If ModelAlias.Visibility == "public" and no tenant_model_visibility row exists for
//      (tenantID, aliasID), the alias is included (public default).
//   2. If ModelAlias.Visibility == "restricted" and a tenant_model_visibility row with
//      visible=true exists for (tenantID, aliasID), the alias is included.
//   3. If ModelAlias.Visibility == "restricted" and no such row exists, the alias is excluded.
//   4. If a tenant_model_visibility row with visible=false exists for any alias, that alias
//      is always excluded regardless of Visibility field.
func (s *Service) ListModelsForTenant(ctx context.Context, tenantID uuid.UUID) ([]ModelAlias, error)
```

The query joins `model_aliases` with `tenant_model_visibility` using a LEFT JOIN and applies the rules above in SQL (not Go-side filtering) to avoid loading all rows.

---

### Task 2: Catalog HTTP handler update

**File:** `apps/control-plane/internal/catalog/http.go`

Read the existing file before editing. Update the `GET /api/v1/catalog/models` handler to:

1. Extract `tenantID` from the authenticated JWT claims (the field already exists in the claims struct per Phase 10 conventions; verify the exact field name before editing).
2. Call `ListModelsForTenant(ctx, tenantID)` instead of the current unfiltered call.
3. Return the filtered list; response shape unchanged.

Unauthenticated requests (public API key context with no tenant) fall back to returning only `visibility = 'public'` aliases with no tenant-specific overrides.

> **Auth middleware prerequisite:** `GET /api/v1/catalog/models` is currently mounted without authentication middleware in the router. Before this handler change can safely read tenant claims, the route must be wrapped with the JWT auth middleware (the same middleware used on `/v1/...` inference routes). Read `apps/control-plane/platform/http/router.go` to find the exact mount point and apply the middleware there. If the route is intentionally unauthenticated for public catalog browsing, add an explicit claim-presence check: when no tenant claim is present, use the unauthenticated fallback path; when a claim is present but invalid, return 401.

---

### Task 3: Tenant visibility admin endpoint

Add to the providers/admin area (or a new `catalog/admin` sub-router):

```
PUT  /internal/catalog/visibility/{tenantID}/{aliasID}   upsert tenant_model_visibility row
DELETE /internal/catalog/visibility/{tenantID}/{aliasID} set visible=false
GET  /internal/catalog/visibility/{tenantID}             list visibility rows for tenant
```

Protected by shared-secret middleware. Calls into a new `VisibilityRepository` (same pattern as Plan 20-02's `Repository`).

After any PUT/DELETE, trigger an OWUI sync for the affected alias (Task 4).

---

### Task 4: Open WebUI access_control sync

**File:** `apps/control-plane/internal/owui/client.go`

Read the existing file before editing. Extend the client with:

```go
// SyncModelAccessControl fetches the current access_control for modelID from OWUI,
// merges the provided allowedGroupIDs into the read.group_ids list, and writes it back.
// Passing an empty allowedGroupIDs slice sets access_control to null (public).
// This function replaces the original single-groupID design to support multi-tenant grants:
// granting alias X to tenant B must not revoke tenant A's existing grant.
func (c *Client) SyncModelAccessControl(ctx context.Context, modelID string, allowedGroupIDs []string) error
```

Implementation:

1. `GET /api/v1/models/<modelID>` to fetch current `access_control` (if the model does not exist in OWUI, treat current `read.group_ids` as empty).
2. Build the new `read.group_ids` as the caller-supplied `allowedGroupIDs` (the caller is responsible for computing the full desired set from `tenant_model_visibility`; this function does not query the DB).
3. `POST /api/v1/models/` with body:
   ```json
   {
     "id": "<modelID>",
     "access_control": {
       "read":  {"group_ids": <allowedGroupIDs>, "user_ids": []},
       "write": {"group_ids": [],                "user_ids": []}
     }
   }
   ```
   If `allowedGroupIDs` is empty or nil, send `"access_control": null`.
4. Auth header: `Authorization: Bearer <OWUI_ADMIN_TOKEN>` (existing pattern in the client).

> **Caller responsibility:** the visibility admin endpoint (Task 3) must compute the full current allowlist before calling `SyncModelAccessControl`. After any PUT/DELETE on `tenant_model_visibility`, query all `visible=true` rows for the affected alias, resolve each tenant's OWUI group ID (`"tenant_"+tenantID.String()`), and pass the complete resulting `[]string` to `SyncModelAccessControl`. This ensures grants are additive and revocations are precise.

Wire `UpsertModelAccessControl` into the visibility admin endpoint: after writing to `tenant_model_visibility`, call `UpsertModelAccessControl(ctx, alias.ExternalModelID, tenantGroupID)`. The tenant's OWUI group ID is resolved via `EnsureGroup(ctx, "tenant_"+tenantID.String())` — note the `tenant_` prefix, which matches the naming convention used by the signup provisioning code when creating OWUI groups. Using bare `tenantID.String()` targets a different (empty) group and would silently grant access to nobody.

---

### Task 5: Unit tests

**File:** `apps/control-plane/internal/catalog/service_test.go`

TDD cases:

1. Tenant with no `tenant_model_visibility` rows sees all `visibility=public` aliases.
2. Tenant with `visible=false` row for a public alias does NOT see that alias.
3. Tenant with `visible=true` row for a `restricted` alias DOES see it.
4. Tenant with no row for a `restricted` alias does NOT see it.
5. Unauthenticated (no tenantID) returns only `public` aliases.

**File:** `apps/control-plane/internal/owui/client_test.go`

1. `SyncModelAccessControl` with a non-empty `allowedGroupIDs` slice sends correct `access_control` JSON.
2. `SyncModelAccessControl` with empty/nil `allowedGroupIDs` sends `access_control: null`.
3. `SyncModelAccessControl` performs a GET before the POST to confirm merge semantics (mock returns existing groups; verify new call includes all groups).
4. HTTP 401 from OWUI returns a typed error.

Use `httptest.NewServer` to mock the OWUI API in tests.

---

## TDD Notes

Write service tests against a mock repository (interface). Write OWUI client tests against `httptest.Server`. Do not hit a real OWUI instance in unit tests.

---

## Acceptance Criteria

- [ ] `model_aliases.visibility` CHECK constraint extended to include `'restricted'` (prerequisite migration).
- [ ] `GET /api/v1/catalog/models` returns only visibility-permitted aliases for the authenticated tenant.
- [ ] `visibility=public` aliases visible by default unless explicitly blocked by `visible=false` row.
- [ ] `visibility=restricted` aliases hidden unless `visible=true` row exists for tenant.
- [ ] Visibility admin endpoints (`/internal/catalog/visibility/...`) write to `tenant_model_visibility` and trigger OWUI sync.
- [ ] `SyncModelAccessControl` sets `access_control.read.group_ids` to the full computed allowlist; nil/empty sends null (public).
- [ ] Multi-tenant grant does not revoke prior tenant's access (second PUT extends the allowlist).
- [ ] OWUI group resolved as `"tenant_"+tenantID.String()` matching signup provisioning convention.
- [ ] `GET /api/v1/catalog/models` route wrapped in JWT auth middleware before handler extracts claims.
- [ ] All 5 catalog service tests + 4 OWUI client tests pass.
- [ ] `go vet ./apps/control-plane/...` clean.
