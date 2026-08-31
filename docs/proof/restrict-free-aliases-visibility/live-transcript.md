# Live proof: hive-free and hive-free-tools locked out of the tenant picker

Captured 2026-08-31 against a control-plane built from this branch, talking to a
scratch Postgres with the real migration chain (`.github/ci/test-db-bootstrap.sql`
plus every file in `supabase/migrations/`, including
`20260831_01_restrict_free_pool_aliases_visibility.sql`, applied in order) and
the fixture in `seed.sql` from this directory. Reproduce with `run-proof.sh`.
Same shape as the precedent proof for the original per-tenant entitlement
feature, `docs/proof/tenant-model-entitlement/`.

Why this transcript rather than a browser screenshot of Open WebUI's model
picker: OWUI on this deployment is SSO-only by design (`ENABLE_LOGIN_FORM` and
`ENABLE_SIGNUP` are both hardcoded `"false"` in
`deploy/docker/docker-compose.yml`'s `open-webui` service; every account is
provisioned through the Hive OIDC provider). Producing a literal picker
screenshot means standing up the full self-hosted Supabase/GoTrue/PostgREST/
Caddy OIDC chain, the same topology the demo box runs, not something a quick
local capture can do safely or representatively. This repository's own
precedent for the identical class of change (`docs/proof/tenant-model-
entitlement/`, the PR that first wired tenant model entitlement into the
inference path) used exactly this live-transcript shape, with no image, for
the same reason: the change is backend-only (zero UI/frontend code touched by
this PR), and the endpoint exercised below,
`/internal/catalog/snapshot/tenant/{tenantID}`, is the literal tenant-scoped
catalog snapshot that `GET /v1/models` (what Open WebUI's own model dropdown
calls, per `scripts/seed-owui-e2e-user.py`'s OWUI_SHIM_KEY documentation)
resolves through.

Tenants in the fixture:

| tenant | id | posture |
|---|---|---|
| customer | `c1111111-…` | zero `tenant_model_visibility` rows: any ordinary Hive tenant today |
| automation | `a1111111-…` | `visible=true` grants on both aliases, mirroring exactly what `scripts/ci-seed-api-key.sh` now inserts for its own throwaway tenant |

Every response below comes from the running service over HTTP, not from a test
double.

## 1. An ordinary customer tenant asks for both aliases

```
--- ordinary customer, hive-free (no visibility grant)
POST /internal/routing/select  {"alias_id":"hive-free","tenant_id":"c1111111-1111-1111-1111-111111111111","need_chat_completions":true,"need_streaming":true}
{"error":"routing: model not entitled for tenant: alias hive-free"}

HTTP 403

--- ordinary customer, hive-free-tools (no visibility grant)
POST /internal/routing/select  {"alias_id":"hive-free-tools","tenant_id":"c1111111-1111-1111-1111-111111111111","need_chat_completions":true}
{"error":"routing: model not entitled for tenant: alias hive-free-tools"}

HTTP 403
```

This is the fix working end to end: the same call that a chat completion makes
before dispatch now refuses both aliases for a tenant with no grant, on both
the rate-limited free pool and the anonymous-upstream alias.

## 2. The automation tenant (the CI-lane grant) still gets through

```
--- automation tenant, hive-free (has the grant)
POST /internal/routing/select  {"alias_id":"hive-free","tenant_id":"a1111111-1111-1111-1111-111111111111","need_chat_completions":true,"need_streaming":true}
{"alias_id":"hive-free","route_id":"route-free-pool-groq","litellm_model_name":"route-free-pool","provider":"groq","fallback_route_ids":["route-free-pool-groq-2"],"pricing":{"input_price_credits":1000000,"output_price_credits":4000000,"cache_read_price_credits":0,"cache_write_price_credits":0,"pricing_mode":"fixed"},"price_unit":"tokens","reasoning_reserve_tokens":4096}

HTTP 200

--- automation tenant, hive-free-tools (has the grant)
POST /internal/routing/select  {"alias_id":"hive-free-tools","tenant_id":"a1111111-1111-1111-1111-111111111111","need_chat_completions":true}
{"alias_id":"hive-free-tools","route_id":"route-free-opencode-zen","litellm_model_name":"route-free-opencode-zen","provider":"opencode-zen","fallback_route_ids":[],"pricing":{"input_price_credits":1000000,"output_price_credits":4000000,"cache_read_price_credits":0,"cache_write_price_credits":0,"pricing_mode":"fixed"},"price_unit":"tokens","reasoning_reserve_tokens":0}

HTTP 200
```

Proof that `scripts/ci-seed-api-key.sh`'s new grant is the correct shape:
a tenant carrying exactly the rows that script now inserts keeps invoking
both aliases with no other change, which is what keeps CI's live-integration
lane (default `HIVE_TEST_MODEL`/`HIVE_TOOLS_MODEL: hive-free`) green.

## 3. Picker source: the tenant-scoped catalog snapshot

`/internal/catalog/snapshot/tenant/{tenantID}` is the tenant-scoped listing
`GET /v1/models` resolves through (same `ListModelsForTenant` /
`AliasVisibleToTenant` path). Ten models for the customer tenant, twelve for
the automation tenant, the difference being exactly the two restricted
aliases:

```
--- ordinary customer snapshot: hive-free and hive-free-tools both absent
GET /internal/catalog/snapshot/tenant/c1111111-1111-1111-1111-111111111111
{"id":"deepseek-v4-flash"}
{"id":"deepseek-v4-pro"}
{"id":"hive-auto"}
{"id":"hive-default"}
{"id":"hive-embedding-default"}
{"id":"hive-fast"}
{"id":"hive-medium"}
{"id":"hive-small"}
{"id":"hive-stt"}
{"id":"hive-tts"}
HTTP 200                                                    (10 models)

--- automation tenant snapshot: both present
GET /internal/catalog/snapshot/tenant/a1111111-1111-1111-1111-111111111111
{"id":"deepseek-v4-flash"}
{"id":"deepseek-v4-pro"}
{"id":"hive-auto"}
{"id":"hive-default"}
{"id":"hive-embedding-default"}
{"id":"hive-fast"}
{"id":"hive-free"}
{"id":"hive-free-tools"}
{"id":"hive-medium"}
{"id":"hive-small"}
{"id":"hive-stt"}
{"id":"hive-tts"}
HTTP 200                                                    (12 models)
```

## 4. A public alias, same ordinary tenant, is unaffected

```
--- ordinary customer, hive-default (public, no restriction here)
POST /internal/routing/select  {"alias_id":"hive-default","tenant_id":"c1111111-1111-1111-1111-111111111111","need_chat_completions":true,"need_streaming":true}
{"alias_id":"hive-default","route_id":"route-deepseek-v4-flash-default","litellm_model_name":"route-deepseek-v4-flash-default","provider":"openrouter","fallback_route_ids":[],"pricing":{"input_price_credits":89460000,"output_price_credits":178920000,"cache_read_price_credits":2982000,"cache_write_price_credits":0,"pricing_mode":"fixed"},"price_unit":"tokens","reasoning_reserve_tokens":0}

HTTP 200
```

Confirms the restriction is scoped to the two named aliases: an ordinary
tenant with no grants at all still gets a normal, priced route back for the
public default alias, on the same call shape that just 403'd on hive-free.
