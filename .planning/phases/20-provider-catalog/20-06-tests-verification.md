---
phase: 20-provider-catalog
plan: 06
type: execute
wave: 4
depends_on: [20-01, 20-02, 20-03, 20-04, 20-05]
size: M
branch: b/phase-20-provider-catalog
milestone: v1.1
track: A
files_modified:
  - apps/control-plane/internal/providers/integration_test.go
  - apps/edge-api/internal/inference/chat_completions_integration_test.go
  - packages/sdk-tests/tool_call_test.js
  - .planning/phases/20-provider-catalog/20-VERIFICATION.md
autonomous: true
---

# Plan 20-06 — Integration Tests + VERIFICATION.md

## Objective

Write integration tests that exercise the full Phase 20 feature stack against a real DB and live containers, then produce `20-VERIFICATION.md` recording pass/fail evidence for every must-have truth.

---

## Tasks

### Task 1: Provider CRUD integration test

**File:** `apps/control-plane/internal/providers/integration_test.go`

Build tag: `//go:build integration`

Prerequisites: real Postgres DB with Phase 20 migration applied. Uses the same test-DB wiring as existing integration tests in the project (check `apps/control-plane/internal/` for the pattern).

Test flow:

1. `POST /internal/providers` creates a provider with slug `"test-provider-XXXXXX"` (random suffix).
2. `GET /internal/providers/{id}` returns the created row.
3. `PUT /internal/providers/{id}` updates `display_name`; verify updated value in response and DB.
4. `DELETE /internal/providers/{id}` sets `enabled=false`; verify `GET` returns `enabled: false`.
5. Attempt `POST /internal/providers` with the same slug; assert 409.
6. `INSERT INTO provider_routes (provider=<slug>)` succeeds (no CHECK constraint violation).
7. Cleanup: delete test rows.

---

### Task 2: Tenant visibility integration test

**File:** `apps/control-plane/internal/catalog/catalog_integration_test.go`

Build tag: `//go:build integration`

Test flow:

1. Seed two aliases: one `visibility=public`, one `visibility=restricted`.
2. `GET /api/v1/catalog/models` as tenant A (no visibility rows): assert public alias present, restricted absent.
3. Insert `tenant_model_visibility` row `(tenantA, restrictedAlias, visible=true)`.
4. Repeat GET: assert restricted alias now present for tenant A.
5. Insert `tenant_model_visibility` row `(tenantA, publicAlias, visible=false)`.
6. Repeat GET: assert public alias now absent for tenant A.
7. `GET /api/v1/catalog/models` as tenant B (no rows): still sees original public set.
8. Cleanup test rows.

---

### Task 3: LiteLLM sync integration test

**File:** `apps/control-plane/internal/litellmconfig/sync_integration_test.go`

Build tag: `//go:build integration`

Requires Docker socket accessible from test host (CI must bind-mount it). Use a `MockRestarter` that records the call rather than calling real `docker restart` in this test.

Test flow:

1. Seed a `custom_providers` row and two `provider_routes` rows referencing it.
2. Call `SyncService.Sync(ctx)`.
3. Read the written config file; parse YAML; assert `model_list` length matches seeded routes.
4. Assert `MockRestarter.Restart` was called exactly once.
5. Assert YAML `api_key` field is `os.environ/PROVIDER_KEY_ENV` format (not a literal key).

---

### Task 4: Tool-call passthrough integration test

**File:** `apps/edge-api/internal/inference/chat_completions_integration_test.go`

Build tag: `//go:build integration`

Uses the in-process handler test helper (or `httptest.NewServer` wrapping the edge-api handler).

Test flow:

1. Configure a route with `tools_supported=true` in `provider_capabilities`.
2. Send `POST /v1/chat/completions` with `tools: [...]`. Assert 200 and upstream request (captured via a mock LiteLLM) contains the `tools` field.
3. Configure a route with `tools_supported=false`.
4. Same request to that route. Assert 400, `code: "unsupported_parameter"`, `param: "tools"`.
5. Request with no `tools` field to the incapable route. Assert 200 (existing flow unbroken).

---

### Task 5: VERIFICATION.md

**File:** `.planning/phases/20-provider-catalog/20-VERIFICATION.md`

Shape: same as `phases/11-verification-cleanup/11-VERIFICATION.md` (Must-Have Truth Verification table + Requirement Coverage + Blockers + Ship-Gate Mapping).

Must-Have Truth Verification table rows:

| # | Truth | Verify Command | Status |
|---|-------|---------------|--------|
| 1 | `provider_routes` CHECK constraint no longer enumerates `openrouter`/`groq` | `psql -c "\d provider_routes" | grep -v "IN ('openrouter"` | PENDING |
| 2 | `custom_providers` table with slug/litellm_prefix/enabled columns exists | `psql -c "\d custom_providers"` | PENDING |
| 3 | `tenant_model_visibility` with `UNIQUE(tenant_id, alias_id)` exists | `psql -c "\d tenant_model_visibility"` | PENDING |
| 4 | Provider CRUD endpoints return 201/200/409/401 as specified | `go test -tags integration ./apps/control-plane/internal/providers/...` | PENDING |
| 5 | Catalog tenant filtering hides restricted aliases without visibility rows | `go test -tags integration ./apps/control-plane/internal/catalog/...` | PENDING |
| 6 | OWUI `access_control` set to tenant group on visibility grant | unit test `owui/client_test.go` | PENDING |
| 7 | LiteLLM config YAML generated from DB rows; restart triggered | `go test -tags integration ./apps/control-plane/internal/litellmconfig/...` | PENDING |
| 8 | Tool-capable alias accepts `tools` (200); incapable alias rejects (400) | `go test -tags integration ./apps/edge-api/internal/inference/...` | PENDING |
| 9 | SDK tool-call test passes or is skipped with `SKIP_TOOL_TESTS=1` | `cd packages/sdk-tests && node tool_call_test.js` | PENDING |
| 10 | `go vet ./apps/...` clean across both services | `go vet ./apps/control-plane/... && go vet ./apps/edge-api/...` | PENDING |

The executor fills in Status (PASS/FAIL) and captures command output snippets.

Blockers section: must address any of the following if they arise:

- LiteLLM DB-backed `/model/*` API shape not re-confirmed via Context7 at build time.
- Docker socket not available in CI environment (sync integration test deferred).
- Tool-capable provider key absent in test environment (`SKIP_TOOL_TESTS=1` deferral).

Ship-Gate Mapping:

- Phase 20 closes v1.1 ship-gate items: "Provider catalog admin API", "Tenant model access control", "Tool-call capability gating (issue #118 medium-term fix)".
- Does NOT close: Phase 21 tier limits, Phase 22 RAG, Phase 23 Bengali translations.

---

## Acceptance Criteria

- [ ] All 5 integration test files compile with `-tags integration`.
- [ ] Provider CRUD integration (Task 1): 7 steps pass against real DB.
- [ ] Catalog visibility integration (Task 2): 8-step flow passes.
- [ ] LiteLLM sync integration (Task 3): YAML output + restart call verified.
- [ ] Tool-call integration (Task 4): positive (200) + negative (400) + no-regression (200) cases pass.
- [ ] `20-VERIFICATION.md` exists with Must-Have table, Requirement Coverage, Blockers (explicit empty list acceptable), and Ship-Gate Mapping sections.
- [ ] Executor has updated Status column from PENDING to PASS/FAIL with captured evidence.
