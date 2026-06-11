---
phase: 20-provider-catalog
type: plan
milestone: v1.1
branch: b/phase-20-provider-catalog
track: A
depends_on:
  - 10-routing-storage-critical-fixes   # provider_routes table, routing.SelectionInput baseline
  - 16-capability-columns-fix           # provider_capabilities table with tool/vision flags
autonomous: true
---

# Phase 20 — Provider Catalog

**Goal:** Give platform administrators the ability to register arbitrary LLM providers and models at runtime, control which models each tenant may use, and propagate that visibility to LiteLLM (inference layer) and Open WebUI (chat surface) without manual file edits or container rebuilds.

**Scope:**

1. Schema: `custom_providers` + `tenant_model_visibility` tables; relax `provider_routes` CHECK constraint.
2. Provider CRUD: internal Go package + internal HTTP endpoints (shared-secret + platform admin auth).
3. LiteLLM config generation + controlled proxy restart (file-based primary path; DB-backed noted as zero-downtime alternative).
4. Tenant model visibility: catalog filtering by `ModelAlias.Visibility` + `tenant_model_visibility`; Open WebUI per-model `access_control` sync.
5. Capability-based tool-call passthrough: edge-api forwards `tools`/`tool_choice`/`response_format` when the alias has at least one tool-capable route; 400 only when no capable route exists.
6. Tests + VERIFICATION.md.

---

## Wave Structure

```
Wave 1 (sequential first):
  20-01   Schema changes

Wave 2 (parallel):
  20-02   Provider CRUD package + endpoints
  20-03   LiteLLM config generation + restart

Wave 3 (parallel):
  20-04   Tenant model visibility + OWUI sync
  20-05   Capability-based tool-call passthrough

Wave 4 (sequential last):
  20-06   Tests + VERIFICATION.md
```

---

## Dependencies Between Plans

```
20-01
  └── 20-02 (needs custom_providers table)
  └── 20-03 (needs relaxed provider_routes, config gen reads from tables)
       └── 20-04 (needs tenant_model_visibility table from 20-01, OWUI client from 20-03 wiring)
       └── 20-05 (needs provider_capabilities columns from Phase 16, SelectionInput from Phase 10)
            └── 20-06 (tests all of the above)
```

---

## Files Expected to Change (Phase-Level Summary)

| Area | Key Paths |
|------|-----------|
| Schema | `supabase/migrations/YYYYMMDD_NN_phase20_provider_catalog.sql` |
| Go: providers | `apps/control-plane/internal/providers/` (new package) |
| Go: catalog | `apps/control-plane/internal/catalog/service.go` |
| Go: routing | `apps/edge-api/internal/routing/selector.go`, `apps/edge-api/internal/inference/chat_completions.go`, `apps/edge-api/internal/inference/errors.go` |
| Go: OWUI | `apps/control-plane/internal/owui/client.go` |
| LiteLLM | `deploy/litellm/config.yaml`, `deploy/docker/docker-compose.yml` |
| Contract | `packages/openai-contract/support-matrix.md` |
| SDK tests | `packages/sdk-tests/` |
| Env | `.env.example` |
| Planning | `.planning/phases/20-provider-catalog/20-VERIFICATION.md` |

---

## Acceptance Criteria (Phase-Level)

- [ ] A new provider row inserted into `custom_providers` flows through to a fresh `config.yaml` and restarts LiteLLM within the documented window.
- [ ] A tenant with `tenant_model_visibility` rows sees only permitted aliases from `GET /api/v1/catalog/models`.
- [ ] OWUI per-model `access_control` reflects the same tenant group constraint as `tenant_model_visibility`.
- [ ] An alias backed by a tool-capable route accepts `tools` + `tool_choice` without a 400; an alias with no capable route returns 400 as before.
- [ ] All Go unit tests pass; SDK integration tests green on tool-capable alias.
- [ ] 20-VERIFICATION.md records pass/fail for every must-have truth.

---

## Related Plans

- `.planning/phases/10-routing-storage-critical-fixes/PLAN.md` — `provider_routes`, `SelectionInput`, `ModelAlias`
- `.planning/phases/16-capability-columns-fix/PLAN.md` — `provider_capabilities` tool/vision columns
- `.planning/phases/19-chat-app-fork-strip/PLAN.md` — OWUI client foundation (`EnsureGroup`, `AddUserToGroup`)
