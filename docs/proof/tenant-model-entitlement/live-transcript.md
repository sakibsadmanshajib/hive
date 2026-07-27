# Live proof: per-tenant model entitlement on the inference path

Captured 2026-07-27 against a control-plane built from this branch, talking to a
scratch Postgres with the real migration chain (`.github/ci/test-db-bootstrap.sql`
plus every file in `supabase/migrations/`, applied in order) and the fixture in
`seed.sql` from this directory. Reproduce with `run-proof.sh`.

Tenants in the fixture:

| tenant | id | posture |
|---|---|---|
| entitled | `11111111-…` | no visibility row for `hive-fast`, `visible=true` grant on the restricted alias |
| blocked | `22222222-…` | `hive-fast` row with `visible=false` |
| greenfield | `33333333-…` | zero rows in `tenant_model_visibility` |

Every response below comes from the running service over HTTP, not from a test double.

## 1. The same alias, two tenants

```
--- entitled tenant asks for hive-fast (no visibility row: public stays allowed)
POST /internal/routing/select  {"alias_id":"hive-fast","tenant_id":"11111111-1111-1111-1111-111111111111","need_chat_completions":true,"need_streaming":true}
{"alias_id":"hive-fast","route_id":"route-groq-fast","litellm_model_name":"route-groq-fast","provider":"groq","fallback_route_ids":["route-openrouter-fast-fallback"]}

HTTP 200

--- blocked tenant asks for the SAME alias (visible=false row)
POST /internal/routing/select  {"alias_id":"hive-fast","tenant_id":"22222222-2222-2222-2222-222222222222","need_chat_completions":true,"need_streaming":true}
{"error":"routing: model not entitled for tenant: alias hive-fast"}

HTTP 403
```

Before this change both requests returned the route: the visibility rules were
applied to the catalog listing only, never to route selection.

## 2. A tenant with zero visibility rows still works

This is the production-safety case. `tenant_model_visibility` is empty on every
current deployment, so "no row means allowed" for public and preview aliases had
to be preserved exactly.

```
--- greenfield tenant, public alias hive-fast
POST /internal/routing/select  {"alias_id":"hive-fast","tenant_id":"33333333-3333-3333-3333-333333333333","need_chat_completions":true,"need_streaming":true}
{"alias_id":"hive-fast","route_id":"route-groq-fast","litellm_model_name":"route-groq-fast","provider":"groq","fallback_route_ids":["route-openrouter-fast-fallback"]}

HTTP 200

--- greenfield tenant, preview alias hive-auto
POST /internal/routing/select  {"alias_id":"hive-auto","tenant_id":"33333333-3333-3333-3333-333333333333","need_chat_completions":true}
{"alias_id":"hive-auto","route_id":"route-openrouter-auto","litellm_model_name":"route-openrouter-auto","provider":"openrouter","fallback_route_ids":[]}

HTTP 200
```

## 3. A restricted alias needs an explicit grant

```
--- greenfield tenant, restricted alias, no grant
POST /internal/routing/select  {"alias_id":"hive-restricted-proof","tenant_id":"33333333-3333-3333-3333-333333333333","need_chat_completions":true}
{"error":"routing: model not entitled for tenant: alias hive-restricted-proof"}

HTTP 403

--- entitled tenant, restricted alias, visible=true grant
POST /internal/routing/select  {"alias_id":"hive-restricted-proof","tenant_id":"11111111-1111-1111-1111-111111111111","need_chat_completions":true}
{"alias_id":"hive-restricted-proof","route_id":"route-proof-restricted","litellm_model_name":"route-proof-restricted","provider":"groq","fallback_route_ids":[]}

HTTP 200
```

## 4. The untenanted (API-key) principal is unchanged

`api_keys` hang off `accounts`, which carry no `tenant_id`, so those requests
send no tenant and are governed by the key policy allowlist as before.

```
--- no tenant_id at all, hive-fast
POST /internal/routing/select  {"alias_id":"hive-fast","need_chat_completions":true,"need_streaming":true}
{"alias_id":"hive-fast","route_id":"route-groq-fast","litellm_model_name":"route-groq-fast","provider":"groq","fallback_route_ids":["route-openrouter-fast-fallback"]}

HTTP 200
```

## 5. The admin visibility toggle now changes an inference verdict

Same tenant, same alias, three calls in a row. The block and unblock calls are
exactly the endpoints the admin control writes to
(`/internal/catalog/visibility/{tenantID}/{aliasID}`), which this branch also
mounts for the first time: the handler existed but was never constructed in
`cmd/server/main.go`, so it answered 404 before.

```
--- console block action: DELETE /internal/catalog/visibility/11111111-1111-1111-1111-111111111111/hive-fast
{"status":"ok"}

HTTP 200

--- same entitled tenant, same alias, immediately after the toggle
POST /internal/routing/select  {"alias_id":"hive-fast","tenant_id":"11111111-1111-1111-1111-111111111111","need_chat_completions":true,"need_streaming":true}
{"error":"routing: model not entitled for tenant: alias hive-fast"}

HTTP 403

--- console unblock action: PUT visible=true
{"status":"ok"}

HTTP 200

--- same entitled tenant, same alias, after unblocking
POST /internal/routing/select  {"alias_id":"hive-fast","tenant_id":"11111111-1111-1111-1111-111111111111","need_chat_completions":true,"need_streaming":true}
{"alias_id":"hive-fast","route_id":"route-groq-fast","litellm_model_name":"route-groq-fast","provider":"groq","fallback_route_ids":["route-openrouter-fast-fallback"]}

HTTP 200
```

## 6. What `/v1/models` now reads: the tenant-scoped snapshot

`hive-fast` is absent for the blocked tenant and present for the tenant with zero
rows, from the same predicate that decided sections 1 to 3.

```
--- blocked tenant snapshot (hive-fast must be absent)
{"models":[{"id":"hive-auto"
{"id":"hive-default"
{"id":"hive-embedding-default"
{"id":"hive-stt"
{"id":"hive-tts"
HTTP 200

--- greenfield tenant snapshot (zero rows: hive-fast must be present)
{"models":[{"id":"hive-auto"
{"id":"hive-default"
{"id":"hive-embedding-default"
{"id":"hive-fast"
{"id":"hive-stt"
{"id":"hive-tts"
HTTP 200
```

## What this transcript does not cover

- The edge-api leg (`POST /v1/chat/completions` returning 403 rather than the old
  503) is covered by `TestDispatchUnentitledModelReturns403` and
  `TestSelectRouteMapsForbiddenToErrModelNotEntitled`, not by a live call. A live
  JWT-session chat call needs a Supabase JWT issuer, and the only local GoTrue
  stack on this box belongs to another agent's running compose project.
- There is no browser screenshot of a console model-visibility toggle because no
  such UI exists yet: nothing under `apps/web-console/` references model
  visibility. Section 5 exercises the write endpoint the UI will call.
