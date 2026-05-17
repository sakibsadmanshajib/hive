---
title: "Phase 19 — Foundation Slice (Tenant Settings + Identity Bridge + Open WebUI Deploy)"
status: draft
phase: 19
authors: [sakib]
date: 2026-05-16
supersedes: []
related:
  - .planning/phases/18-rbac-matrix/
  - .planning/ROADMAP.md
  - .wolf/cerebrum.md
trust_service_criteria:
  - CC6.1
  - CC6.2
  - CC6.3
  - CC6.7
  - CC7.1
  - CC7.2
  - CC8.1
naming_conventions:
  enums_and_codes: ALL_CAPS_WITH_UNDERSCORES
  go_types: PascalCase
  go_functions: camelCase
  sql_identifiers: snake_case
---

# Phase 19 — Foundation Slice

## 1. Overview

Phase 19 establishes the foundation that every subsequent phase of the v1 office-AI pivot depends on. It ships:

1. The tenant-settings backbone (table, enum, middleware, RLS, JWT custom claim) — mirrors the FundMore.ai `tenantSettings.enum.ts` pattern.
2. The identity bridge — Supabase Auth (with Google OAuth) flowing into both Open WebUI and `edge-api` via JWT pass-through.
3. The Open WebUI compose service, deployed with its native admin stripped, configured against our Supabase Auth, our pgvector instance, and our LiteLLM gateway.
4. One end-to-end chat happy path (user signs in via Google → opens Open WebUI → sends a prompt → receives a streamed response → trace + audit rows written).
5. The SOC 2 Type II compliance logging primitive (hash-chained `audit_log` table, async sink fanout, two-tier write policy, integrity verification).

The audit-log primitive must ship in Phase 19 so every later phase (provider catalog, credits, RAG, audit UI, self-host packaging, payments gating) wires through a single helper from day one.

### Brand and tenancy terminology

| Term | Meaning |
| --- | --- |
| **Hive Cloud** | The hosted SaaS product. We operate it. Always multi-tenant. |
| **EnterpriseEdge** | The self-hosted product the customer deploys. Defaults to a single tenant. The customer can enable `ENABLE_MULTI_TENANT` to host multiple departments. We never host EnterpriseEdge for the customer. |
| **EnterpriseEdge users** | End users inside a customer-deployed EnterpriseEdge instance. We do not refer to them as "office users". |
| **Tenant** | The unit of isolation. In Hive Cloud, a tenant is a customer organisation. In EnterpriseEdge, a tenant is typically a department. |

ONE codebase, ONE compose stack. The same image runs both products. Behaviour diverges only through `tenant_settings` keys.

### Naming conventions

* Enum values (Postgres enum types, Go constants representing enums, audit `action` strings, severity, error codes, tenant-setting keys, status, role, deployment, actor_type) use `ALL_CAPS_WITH_UNDERSCORES`.
* SQL identifiers (column names, table names, type names) remain `snake_case`.
* Go types are `PascalCase`; functions and methods are `camelCase` (idiomatic Go also accepts initialisms like `JWT`).
* Stable customer-visible error codes are `ALL_CAPS_WITH_UNDERSCORES`.

## 2. Scope

### In scope (Phase 19)

* `tenants`, `tenant_settings`, `tenant_users` tables with row-level security keyed off `auth.jwt() ->> 'tenant_id'`.
* `tenant_setting_key` Postgres enum (initial set, append-only via migration).
* Supabase custom-access-token hook (`public.custom_access_token_hook`) that injects `tenant_id`, `tenants[]`, and `role` claims.
* Open WebUI compose service with native admin disabled, native OIDC pointed at Supabase, native RAG (personal documents only) backed by Supabase pgvector.
* `edge-api` middleware that validates Supabase JWTs via JWKS, populates request context, and reuses the Phase 18 `Allow*` RBAC helpers.
* `audit_log` table (hash-chained, partitioned monthly), `audit_outbox`, `audit_outbox_dlq`, and `llm_traces` tables.
* `internal/audit/log.go` helper with two-tier write policy (security-tier sync-block, llm-tier WAL-fallback).
* Async sink-fanout worker in `control-plane` with retry, backoff, dead-letter, and a `/healthz/sinks` endpoint.
* Optional sink integrations gated by environment variables: ELK / OpenSearch, Loki, Datadog, Splunk HEC, Sentry, Langfuse. None of them required for the core stack to start.
* Lint rules `lint-no-direct-tenant-id` and `lint-no-direct-audit-write` (extend the Phase 18 lint framework).
* Playwright user-flow E2E suite covering the chat happy path, tenant isolation, JWT expiry, tenant switching, signup-without-tenant rejection, and cross-tenant attack rejection.
* Open WebUI direct E2E suite (dev-time + nightly schedule, not per-commit) covering chat send/stream, multi-turn, model switch, personal-RAG PDF upload + citation, image upload, signout.
* Audit-evidence Go test suite verifying hash-chain integrity, audit-coverage against documented TSC controls, sink failure resilience, and cold-archive retention.

### Out of scope (deferred to later phases)

| Phase | Deliverable |
| --- | --- |
| Phase 20 | Provider catalog (hybrid: stock providers seeded, custom providers DB-managed, LiteLLM YAML hot-reload). |
| Phase 21 | Credit / quota engine in the Anthropic Teams shape (tenant pool + per-user soft cap + monthly grant + extra-usage top-up + bucket rate limits). |
| Phase 22 | Shared tenant knowledge-base RAG pipeline (admin upload in web-console, embeddings via LiteLLM, retrieval injection by edge-api). |
| Phase 23 | Admin console pages (audit viewer, tenant settings UI, user / role management, credit grants, provider mgmt). |
| Phase 24 | Self-host packaging for EnterpriseEdge (bootstrap script, docs, single-tenant defaults). |
| Phase 25 | Payments tenant-gating (existing Stripe / bKash / SSLCommerz gated behind `ENABLE_PUBLIC_BILLING`). |
| Phase 26 | Web search tool (self-host SearXNG, Edge `/v1/tools/web_search`, OWUI native web-search wiring). |

The deferred credit-shape decision direction is the Anthropic Teams model (tenant pool with per-user soft cap, monthly auto-grant, optional extra-usage top-up). Phase 19 ships only a placeholder ledger-attribution row in `llm_traces`; the real engine lands in Phase 21.

## 3. Architecture

```
+----------------------+     +----------------------+
| Browser (user)       |     | Browser (admin)      |
+----------+-----------+     +----------+-----------+
           | login + chat               | login + admin
           v                             v
+----------------+               +-----------------------+
| Open WebUI     |               | web-console           |
| chat + per-    |               | (Next.js admin shell  |
| user RAG       |               |  in Phase 19)         |
+--+----------+--+               +----+------------------+
   | OIDC     | chat (JWT)            | Supabase JS + REST
   |          |                       |
   v          v                       v
+----------------+   +-------------+  +----------------------+
| Supabase Auth  |   | edge-api    |  | control-plane        |
| (GoTrue) +     |   | (Go)        |  | (Go)                 |
| custom_access_ |   | JWT validate|  | tenant settings,     |
| token_hook     |   | + tenant    |  | user signup webhook, |
| (tenant_id     |   | guards      |  | OWUI group sync,     |
| in claims)     |   | + dispatch  |  | audit sink fanout    |
+------+---------+   +------+------+  +----+-----------------+
       |                    | HTTP        |
       |                    v             |
       |             +-------------+      |
       |             | LiteLLM     |      |
       |             | (provider   |      |
       |             |  router)    |      |
       |             +------+------+      |
       |                    |             |
       |                    v             |
       |             +-------------+      |
       |             | Providers   |      |
       |             | OpenAI /    |      |
       |             | Anthropic / |      |
       |             | OpenRouter /|      |
       |             | Groq / ...  |      |
       |             +-------------+      |
       v                                  v
+------------------------------------------------------+
| Supabase Postgres (single multi-tenant DB)          |
| + pgvector (Open WebUI native RAG store)            |
| + audit_log (hash-chained, partitioned monthly)     |
| + llm_traces (partitioned)                          |
| RLS by tenant_id on every tenant-scoped table       |
+------------------------------------------------------+
```

### Component responsibilities (Phase 19 scope)

* **Open WebUI** — new compose service. Native OIDC against Supabase. Native admin disabled by environment and reinforced at the reverse proxy. Native RAG configured against Supabase pgvector for personal documents only. Forwards the user's Supabase JWT to `edge-api` via a pipeline filter.
* **web-console** — existing Next.js app. In Phase 19 it ships only a login screen and a placeholder landing page; full admin pages land in Phase 23.
* **Supabase Auth** — central IdP. Google OAuth provider enabled. Custom access-token hook injects `tenant_id`, `tenants[]`, and `role` claims.
* **edge-api** — existing Go service. A new middleware path validates Supabase JWTs alongside the existing API-key path. JWT users and API-key users converge on the same `ctx.User` / `ctx.TenantID` so all downstream RBAC and audit code stays auth-mode-agnostic.
* **control-plane** — existing Go service. Adds tenant-settings CRUD, a signup webhook handler, an Open WebUI group-sync client, the audit sink-fanout worker, and the cold-archive job.
* **LiteLLM** — existing. In Phase 19 its config is still YAML-driven (the hybrid catalog ships in Phase 20).
* **Postgres** — existing Supabase project. New tables introduced this phase: `tenants`, `tenant_settings`, `tenant_users`, `audit_log`, `audit_outbox`, `audit_outbox_dlq`, `llm_traces`.

## 4. Tenant-settings data model

```sql
-- Tenants — root of every tenant-scoped relation.
CREATE TABLE tenants (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug        text UNIQUE NOT NULL,
  name        text NOT NULL,
  deployment  text NOT NULL CHECK (deployment IN ('HIVE_CLOUD','ENTERPRISE_EDGE')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz
);

-- Central enum of every gateable feature. Append-only.
-- Each new key requires its own migration (FundMore.ai convention).
CREATE TYPE tenant_setting_key AS ENUM (
  'ENABLE_PUBLIC_BILLING',
  'ENABLE_BKASH',
  'ENABLE_SSLCOMMERZ',
  'ENABLE_STRIPE',
  'ENABLE_CREDIT_POOL',
  'ENABLE_PER_USER_CAP',
  'ENABLE_EXTRA_USAGE',
  'ENABLE_RAG_PERSONAL',
  'ENABLE_RAG_SHARED_KB',
  'ENABLE_MULTI_TENANT',
  'ENABLE_SSO_GOOGLE',
  'ENABLE_SSO_MICROSOFT',
  'ENABLE_SSO_SAML',
  'ENABLE_AUDIT_SINK_ELK',
  'ENABLE_AUDIT_SINK_LOKI',
  'ENABLE_AUDIT_SINK_DATADOG',
  'ENABLE_AUDIT_SINK_SPLUNK',
  'ENABLE_AUDIT_SINK_LANGFUSE',
  'ENABLE_ADMIN_CONSOLE',
  'ENABLE_PROVIDER_CUSTOM'
);

CREATE TABLE tenant_settings (
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  key         tenant_setting_key NOT NULL,
  enabled     boolean NOT NULL,
  value_json  jsonb,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid REFERENCES auth.users(id),
  PRIMARY KEY (tenant_id, key)
);

CREATE TABLE tenant_users (
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('OWNER','ADMIN','MEMBER','VIEWER')),
  status      text NOT NULL CHECK (status IN ('ACTIVE','SUSPENDED','INVITED')),
  invited_by  uuid REFERENCES auth.users(id),
  joined_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX tenant_users_user_idx ON tenant_users(user_id);

ALTER TABLE tenant_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_users    ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_settings_isolation ON tenant_settings
  FOR ALL TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

CREATE POLICY tenant_users_isolation ON tenant_users
  FOR ALL TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);
```

### Go helper

A new package `internal/tenant/settings` (shared between `control-plane` and `edge-api`) exposes the only sanctioned API:

```go
package settings

type Key string

const (
    EnablePublicBilling     Key = "ENABLE_PUBLIC_BILLING"
    EnableCreditPool        Key = "ENABLE_CREDIT_POOL"
    EnableRAGPersonal       Key = "ENABLE_RAG_PERSONAL"
    EnableRAGSharedKB       Key = "ENABLE_RAG_SHARED_KB"
    EnableMultiTenant       Key = "ENABLE_MULTI_TENANT"
    EnableSSOGoogle         Key = "ENABLE_SSO_GOOGLE"
    EnableAdminConsole      Key = "ENABLE_ADMIN_CONSOLE"
    EnableProviderCustom    Key = "ENABLE_PROVIDER_CUSTOM"
    // append-only — keep in lockstep with the Postgres enum.
)

type Resolver interface {
    IsEnabled(ctx context.Context, tenantID uuid.UUID, key Key) bool
    Value(ctx context.Context, tenantID uuid.UUID, key Key) (json.RawMessage, bool)
}
```

The default implementation is a per-process cache with a 30-second TTL plus invalidation via Postgres `LISTEN/NOTIFY` on a `tenant_settings_changed` channel. The cache returns a typed `MissingSetting` sentinel rather than `false` when a key has never been written for a tenant, so callers can distinguish "explicitly off" from "unset" if they need to.

### Lint rule: `lint-no-direct-tenant-setting`

Code that needs to gate behaviour on a tenant setting must call `settings.Resolver`. Direct queries against `tenant_settings` are rejected by CI lint. This guarantees there is a single code path for setting reads and matches the Phase 18 `lint-no-bare-role-check` pattern.

## 5. Audit log and SOC 2 Type II coverage

### Storage strategy

* The Postgres `audit_log` table is the single authoritative store. It is required for the stack to operate; if it is unreachable, security-tier writes fail closed.
* Hot tier: 90 days in Postgres, partitioned by month.
* Cold tier: a daily batch job copies completed partitions to parquet in Supabase Storage and retains them for 7 years. The job records an integrity hash (`prev_hash` of the last row + sha256 of the parquet file) into a `cold_archive_manifest` table.
* No new infrastructure is required to ship Phase 19. ELK / Loki / Langfuse and similar sinks are optional and shipped as opt-in compose profiles.

### Required write coverage (mapped to Trust Service Criteria)

| Control | Audit `action` values emitted in Phase 19 |
| --- | --- |
| CC6.1, CC6.2 (logical access) | `AUTH_SIGNIN_SUCCESS`, `AUTH_SIGNIN_FAILURE`, `AUTH_SIGNUP_SUCCESS`, `AUTH_JWT_INVALID`, `AUTH_JWT_EXPIRED`, `AUTH_SESSION_REVOKED`, `AUTH_SIGNIN_FAILURE_NO_TENANT` |
| CC6.3 (authorization) | `RBAC_GRANT`, `RBAC_REVOKE`, `RBAC_DENY`, `AUTH_ROLE_CHANGE`, `TENANT_SETTING_UPDATE`, `CROSS_TENANT_ATTEMPT` |
| CC6.7 (data access) | `KB_PERSONAL_DOC_UPLOAD`, `KB_PERSONAL_DOC_DELETE`, `RAG_PERSONAL_RETRIEVAL`, `LLM_TRACE_CONTENT_ACCESS` |
| CC7.1, CC7.2, CC7.3 (security ops) | `RATE_LIMIT_BREACH`, `WEBHOOK_SIGNATURE_FAIL`, `MIGRATION_APPLY`, `SERVER_PANIC`, `AUDIT_CHAIN_VERIFY_FAIL` |
| CC7.5, CC8.1 (change mgmt) | `DEPLOY_PUSH`, `TENANT_SWITCH`, `TENANT_USER_ADD`, `TENANT_USER_REMOVE`, `OWUI_GROUP_CREATE_SUCCESS`, `OWUI_GROUP_CREATE_FAILURE`, `OWUI_GROUP_ADD_SUCCESS`, `OWUI_GROUP_ADD_FAILURE` |
| A1.1 (availability) | `BACKUP_COMPLETE`, `BACKUP_INTEGRITY_FAIL`, `HEALTHCHECK_FAIL_SUSTAINED` |
| C1.1, C1.2 (confidentiality) | `CRYPTO_KEY_ROTATE`, `TLS_CERT_ROTATE`, `JWKS_FETCH_FAILURE` |

Phase 22 (RAG shared KBs), Phase 21 (credits), and Phase 23 (admin pages) will add their own action codes against the same primitive. Each phase must update `docs/compliance/SOC2-LOG-COVERAGE.md` with the controls it newly satisfies.

### Schema

```sql
CREATE TABLE audit_log (
  id                bigserial,
  tenant_id         uuid,
  actor_id          uuid,
  actor_type        text NOT NULL CHECK (actor_type IN ('USER','SERVICE','SYSTEM','EXTERNAL')),
  action            text NOT NULL,
  resource_type     text,
  resource_id       text,
  severity          text NOT NULL CHECK (severity IN ('DEBUG','INFO','NOTICE','WARNING','ERROR','CRITICAL')),
  before_json       jsonb,
  after_json        jsonb,
  request_id        uuid,
  source_ip         inet,
  user_agent        text,
  jwt_claims_digest text,
  deploy_sha        text NOT NULL,
  env               text NOT NULL,
  ts                timestamptz NOT NULL DEFAULT clock_timestamp(),
  seq               bigint NOT NULL,
  prev_hash         bytea NOT NULL,
  row_hash          bytea NOT NULL,
  PRIMARY KEY (ts, id)
) PARTITION BY RANGE (ts);

CREATE TABLE audit_log_2026_05 PARTITION OF audit_log
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
-- Monthly partitions are created by a control-plane scheduled job; the
-- migration provisions the current and next month at install time.

REVOKE UPDATE, DELETE ON audit_log FROM PUBLIC;
GRANT INSERT, SELECT ON audit_log TO app_role;

CREATE ROLE auditor_ro;
GRANT SELECT ON audit_log TO auditor_ro;
GRANT USAGE ON SCHEMA public TO auditor_ro;

CREATE TABLE audit_outbox (
  id           bigserial PRIMARY KEY,
  audit_id     bigint NOT NULL,
  audit_ts     timestamptz NOT NULL,
  sink         text NOT NULL,
  attempts     int NOT NULL DEFAULT 0,
  last_error   text,
  delivered_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_outbox_undelivered
  ON audit_outbox(sink, created_at)
  WHERE delivered_at IS NULL;

CREATE TABLE audit_outbox_dlq (LIKE audit_outbox INCLUDING ALL);

CREATE TABLE llm_traces (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL,
  user_id           uuid,
  request_id        uuid NOT NULL,
  model             text NOT NULL,
  provider          text NOT NULL,
  in_tokens         int  NOT NULL,
  out_tokens        int  NOT NULL,
  latency_ms        int  NOT NULL,
  cost_credits      bigint NOT NULL,
  finish_reason     text,
  prompt_hash       text NOT NULL,
  completion_hash   text NOT NULL,
  retrieval_doc_ids text[],
  langfuse_trace_id text,
  ts                timestamptz NOT NULL DEFAULT now()
) PARTITION BY RANGE (ts);
```

### Go helper

```go
package audit

type Severity string

const (
    SeverityDebug    Severity = "DEBUG"
    SeverityInfo     Severity = "INFO"
    SeverityNotice   Severity = "NOTICE"
    SeverityWarning  Severity = "WARNING"
    SeverityError    Severity = "ERROR"
    SeverityCritical Severity = "CRITICAL"
)

type Event struct {
    TenantID     uuid.UUID
    Actor        Actor
    Action       string
    ResourceType string
    ResourceID   string
    Severity     Severity
    Before, After any
    RequestID    uuid.UUID
}

func Log(ctx context.Context, e Event) error {
    if e.Severity == SeverityCritical ||
        e.Severity == SeverityError   ||
        isSecurityAction(e.Action) {
        return insertSyncWithHashChain(ctx, e) // fail closed on error
    }
    return insertAsyncOrWAL(ctx, e)            // fail open with local WAL
}
```

`isSecurityAction` is a generated set populated from the audit action registry. The registry lives at `apps/control-plane/internal/audit/actions.go` and is the source of truth for which actions are security-tier vs llm-tier.

### Hash chain

For each partition, `seq` is monotonically incremented starting at 1. The `prev_hash` of the first row in a partition is a 32-byte zero hash; for every subsequent row it is the `row_hash` of the preceding row. `row_hash` is `sha256(prev_hash || canonical_json_of_all_other_columns)`.

A scheduled daily job (`audit_chain_verifier`) verifies every partition from oldest to newest. A mismatch emits `AUDIT_CHAIN_VERIFY_FAIL` with severity `CRITICAL`, fires an Alertmanager page, and is forwarded to Sentry if configured.

### Async sink fanout

* On every `INSERT` into `audit_log`, a trigger calls `pg_notify('audit_event', row_id)` and inserts a row into `audit_outbox` for each enabled sink.
* A control-plane worker (`audit_sink_worker`) listens on the channel and drains the outbox. Retry uses exponential backoff (1s → 2s → 4s ... cap 5 min). After an hour of repeated failures, the row moves to `audit_outbox_dlq` and an operator can replay it via a control-plane endpoint.
* `/healthz/sinks` exposes the per-sink state used by k8s readiness probes and the admin UI in Phase 23.

### Optional sinks (all off by default)

| Sink | Activation | Events forwarded |
| --- | --- | --- |
| ELK / OpenSearch | `AUDIT_SINK_ELK_URL`, `AUDIT_SINK_ELK_API_KEY` | All audit events |
| Loki | `AUDIT_SINK_LOKI_URL` | All audit events |
| Datadog | `AUDIT_SINK_DATADOG_API_KEY`, `AUDIT_SINK_DATADOG_SITE` | All audit events |
| Splunk HEC | `AUDIT_SINK_SPLUNK_HEC_URL`, `AUDIT_SINK_SPLUNK_HEC_TOKEN` | All audit events |
| Sentry | `SENTRY_DSN` | Severity `ERROR` and `CRITICAL` only |
| Langfuse | `LANGFUSE_HOST`, `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY` | LLM-call events emitted as Langfuse `trace + span + generation` |
| Prometheus | always on (existing) | Counter + histogram of audit-event volume by severity / action |

Startup behaviour for every sink: a connectivity probe runs once. Missing env vars emit a single `INFO` log line. A failing probe emits a single `WARNING`, marks the sink degraded, sets `audit_sink_up{sink="..."} 0` in Prometheus, and fires Alertmanager after `AUDIT_SINK_FAILURE_GRACE_MINUTES` (default 5).

### Two-tier write policy

* **Security tier (sync-block):** authentication events, RBAC decisions, tenant-setting changes, admin mutations, cross-tenant attempts, key issue / revoke, panics, integrity failures. If the Postgres insert fails, the originating request fails. This is the SOC 2 hard guarantee.
* **LLM tier (WAL-fallback):** `CHAT_REQUEST`, `RAG_PERSONAL_RETRIEVAL`, `LLM_TRACE_CONTENT_ACCESS`, and other high-volume runtime events. Postgres insert is attempted with a short deadline; on failure, the event is appended to an on-disk WAL (`/var/lib/hive/audit-wal/`) and a background drainer replays it when Postgres recovers. A `WARNING` alert is raised whenever the WAL is non-empty for longer than 60 seconds.

### Langfuse fanout for LLM observability

* `llm_traces` is the primary store; it is always populated.
* If Langfuse env vars are set, the worker forwards each new `llm_traces` row as a trace (with one generation span) and writes the returned `trace_id` back into the row.
* Prompts and completions are forwarded only when `LANGFUSE_INCLUDE_CONTENT=true` (default `false`). Hashes are always forwarded.
* Langfuse downtime never blocks chat.

## 6. Identity bridge

### Components

1. Supabase Auth (GoTrue) as IdP. Google OAuth provider configured. SAML and Microsoft providers are listed in tenant-settings keys but ship inert in Phase 19.
2. The Postgres function `public.custom_access_token_hook` injects `tenant_id`, `tenants[]`, and `role` claims into every issued access token.
3. Open WebUI is registered as an OIDC client of Supabase.
4. A signup webhook in `control-plane` reacts to `auth.users INSERT` to provision the membership row and Open WebUI group.
5. The `edge-api` JWT middleware validates Supabase JWTs via JWKS, populates request context, and reuses the Phase 18 `Allow*` helpers.

### JWT claims after the hook

```json
{
  "sub": "<auth.users.id>",
  "email": "user@office.example",
  "aud": "authenticated",
  "iss": "https://<project>.supabase.co/auth/v1",
  "exp": 1747000000,
  "iat": 1746996400,
  "tenant_id": "<active-tenant-uuid>",
  "tenants": [
    { "id": "<tenant-uuid>", "role": "ADMIN" },
    { "id": "<other-tenant-uuid>", "role": "MEMBER" }
  ],
  "role": "ADMIN"
}
```

### Postgres function

```sql
CREATE OR REPLACE FUNCTION public.custom_access_token_hook(event jsonb)
RETURNS jsonb
LANGUAGE plpgsql STABLE
AS $$
DECLARE
  claims        jsonb;
  user_id       uuid;
  tenant_list   jsonb;
  selected      uuid;
  user_role     text;
BEGIN
  user_id := (event->>'user_id')::uuid;
  claims  := event->'claims';

  SELECT jsonb_agg(jsonb_build_object('id', t.id, 'role', tu.role))
    INTO tenant_list
    FROM public.tenant_users tu
    JOIN public.tenants t ON t.id = tu.tenant_id
   WHERE tu.user_id = user_id
     AND tu.status  = 'ACTIVE'
     AND t.archived_at IS NULL;

  SELECT (raw_user_meta_data->>'selected_tenant_id')::uuid
    INTO selected
    FROM auth.users
   WHERE id = user_id;

  IF selected IS NULL AND tenant_list IS NOT NULL
     AND jsonb_array_length(tenant_list) > 0 THEN
    selected := (tenant_list->0->>'id')::uuid;
  END IF;

  SELECT role INTO user_role
    FROM public.tenant_users
   WHERE user_id = user_id AND tenant_id = selected;

  claims := claims
    || jsonb_build_object('tenant_id', selected)
    || jsonb_build_object('tenants',   COALESCE(tenant_list, '[]'::jsonb))
    || jsonb_build_object('role',      user_role);

  RETURN jsonb_build_object('claims', claims);
END;
$$;

GRANT EXECUTE ON FUNCTION public.custom_access_token_hook TO supabase_auth_admin;
```

The hook is registered in the Supabase dashboard under **Auth → Hooks → Custom Access Token**. The migration that installs the function emits an audit row when applied (`MIGRATION_APPLY`).

### Tenant switching

Admin or self-service tenant-switch flow:

1. The UI calls `POST /v1/tenants/{tenant_id}/switch` on `control-plane`.
2. The handler verifies the user belongs to the target tenant; if not, audits `CROSS_TENANT_ATTEMPT` and returns `403 CROSS_TENANT`.
3. On success, the handler updates `auth.users.raw_user_meta_data.selected_tenant_id` via the Supabase admin client.
4. The UI calls `supabase.auth.refreshSession()`; the new access token reflects the switch.
5. The handler emits `TENANT_SWITCH` with severity `INFO`.

### Open WebUI OIDC configuration

The compose service environment block (full block in Section 7) sets:

```
ENABLE_SIGNUP=false
ENABLE_OAUTH_SIGNUP=true
OAUTH_PROVIDER_NAME=Hive
OPENID_PROVIDER_URL=${SUPABASE_URL}/auth/v1/.well-known/openid-configuration
OAUTH_CLIENT_ID=${SUPABASE_OAUTH_CLIENT_ID}
OAUTH_CLIENT_SECRET=${SUPABASE_OAUTH_CLIENT_SECRET}
OAUTH_SCOPES=openid email profile
ENABLE_OAUTH_ROLE_MANAGEMENT=true
OAUTH_ROLES_CLAIM=role
OAUTH_ALLOWED_ROLES=ADMIN,MEMBER,VIEWER
OAUTH_ADMIN_ROLES=ADMIN
ENABLE_OAUTH_GROUP_MANAGEMENT=true
OAUTH_GROUPS_CLAIM=tenants
DEFAULT_USER_ROLE=MEMBER
```

### Signup webhook flow

```
[OAuth callback to Supabase]
         |
         v
[auth.users INSERT]
         |  Supabase Database Webhook
         v
[POST /internal/auth/user-created on control-plane]
         |
         |  1. Determine target tenant:
         |     a) invite token in OAuth state -> that tenant
         |     b) email domain match -> EnterpriseEdge single tenant
         |     c) otherwise -> new-tenant signup flow (Hive Cloud) or reject
         |  2. INSERT tenant_users (role=MEMBER, status=ACTIVE)
         |  3. Audit AUTH_SIGNUP_SUCCESS + TENANT_USER_ADD
         |  4. Async job: call OWUI admin API to add user to group
         |     tenant_<tenant_uuid> (polls OWUI by email up to 30s
         |     because OWUI creates its user row on its own OIDC
         |     callback, which may race the webhook)
         v
[done]
```

The Open WebUI admin API call:

```
POST {OWUI_BASE_URL}/api/v1/groups/{group_id}/add-user
Authorization: Bearer {OWUI_ADMIN_TOKEN}
Body: { "user_email": "user@office.example" }
```

Audit outcomes: `OWUI_GROUP_ADD_SUCCESS` or `OWUI_GROUP_ADD_FAILURE` (severity `ERROR` after the retry budget is exhausted).

### JWT forwarding from Open WebUI to edge-api

Open WebUI pipelines filter (`deploy/docker/pipelines/hive_jwt_forward.py`) injects the user's Supabase JWT as the `Authorization` header on every upstream request:

```python
class Filter:
    """Replaces upstream Authorization header with the user's Supabase JWT."""

    def inlet(self, body, user):
        token = user.get("oauth_sub_token") or user.get("token")
        if token:
            body.setdefault("__metadata", {})["upstream_auth"] = f"Bearer {token}"
        return body
```

Phase 19 Plan-01 (the implementation plan that follows this spec) includes a research task to confirm the exact OWUI hook for injecting `upstream_auth` into the outgoing OpenAI-compatible request. The fallback, if the pipeline filter does not have direct access to the request headers, is a tiny Go reverse-proxy sidecar (≤200 LOC) that sits between OWUI and `edge-api`, looks up the active user from an OWUI session cookie, and rewrites `Authorization` from a static shim key to the user's JWT. The fallback adds one container to the compose; we adopt it only if the pipeline path is blocked.

### edge-api JWT middleware

```
apps/edge-api/internal/auth/
  jwt_supabase.go          # JWKS-backed validator + middleware
  jwt_supabase_test.go
  user_context.go          # ctx getters: User, TenantID, Role
  api_key.go               # existing — unchanged
  selector.go              # picks JWT vs API-key path on Authorization prefix
```

```go
type SupabaseJWTValidator struct {
    issuer string
    jwks   *jwk.Cache // 24h TTL, refresh-on-miss
}

func (v *SupabaseJWTValidator) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
        if strings.HasPrefix(tok, "hk_") {
            // Existing API-key path — handled by api_key.go upstream of this
            // middleware in the chain; we should never see it here, but the
            // guard avoids accidental double-handling.
            next.ServeHTTP(w, r)
            return
        }
        claims, err := v.parse(tok)
        if err != nil {
            audit.Log(r.Context(), audit.Event{
                Action:   "AUTH_JWT_INVALID",
                Severity: audit.SeverityWarning,
                Before:   map[string]string{"reason": err.Error()},
            })
            http.Error(w, "invalid token", http.StatusUnauthorized)
            return
        }
        ctx := auth.WithUser(r.Context(), &auth.User{
            ID:       claims.Sub,
            TenantID: claims.TenantID,
            Role:     claims.Role,
            Email:    claims.Email,
        })
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

The `selector.go` middleware routes requests to either the API-key validator (existing) or the JWT validator based on the `Authorization` prefix. JWT users and API-key users both populate the same `auth.User` context, so all downstream RBAC and audit code stays auth-mode-agnostic.

## 7. Open WebUI deploy and stripped admin

### Compose service (added to `deploy/docker/docker-compose.yml`)

```yaml
services:
  open-webui:
    image: ghcr.io/open-webui/open-webui:main
    ports: ["3002:8080"]
    depends_on:
      edge-api:      { condition: service_healthy }
      control-plane: { condition: service_healthy }
    environment:
      ENABLE_SIGNUP: "false"
      ENABLE_OAUTH_SIGNUP: "true"
      OAUTH_PROVIDER_NAME: "Hive"
      OPENID_PROVIDER_URL: "${SUPABASE_URL}/auth/v1/.well-known/openid-configuration"
      OAUTH_CLIENT_ID: "${SUPABASE_OAUTH_CLIENT_ID}"
      OAUTH_CLIENT_SECRET: "${SUPABASE_OAUTH_CLIENT_SECRET}"
      OAUTH_SCOPES: "openid email profile"
      ENABLE_OAUTH_ROLE_MANAGEMENT: "true"
      OAUTH_ROLES_CLAIM: "role"
      OAUTH_ALLOWED_ROLES: "ADMIN,MEMBER,VIEWER"
      OAUTH_ADMIN_ROLES: "ADMIN"
      ENABLE_OAUTH_GROUP_MANAGEMENT: "true"
      OAUTH_GROUPS_CLAIM: "tenants"
      DEFAULT_USER_ROLE: "MEMBER"

      ENABLE_ADMIN_PANEL: "false"
      ENABLE_ADMIN_EXPORT: "false"
      ENABLE_COMMUNITY_SHARING: "false"
      ENABLE_MODEL_FILTER: "true"
      ENABLE_EVALUATION_ARENA_MODELS: "false"

      # Preserve Open WebUI branding until `.planning/v1.1-chatapp/LICENSE-DECISION.md`
      # chooses a compliant branding path.
      WEBUI_NAME: "Open WebUI for Hive"
      WEBUI_URL: "${HIVE_CHAT_URL}"
      DEFAULT_LOCALE: "en"

      OPENAI_API_BASE_URL: "http://edge-api:8080/v1"
      OPENAI_API_KEY: "owui-shim-key"
      ENABLE_OPENAI_API: "true"
      ENABLE_OLLAMA_API: "false"

      RAG_EMBEDDING_ENGINE: "openai"
      RAG_OPENAI_API_BASE_URL: "http://edge-api:8080/v1"
      RAG_OPENAI_API_KEY: "owui-shim-key"
      RAG_EMBEDDING_MODEL: "text-embedding-3-small"
      VECTOR_DB: "pgvector"
      PGVECTOR_DB_URL: "${SUPABASE_DB_URL}"
      RAG_TOP_K: "5"
      ENABLE_RAG_HYBRID_SEARCH: "true"

      ENABLE_PIPELINES: "true"
      PIPELINES_URLS: "/app/pipelines/hive_jwt_forward.py"

      DATA_DIR: "/data"
      STATIC_DIR: "/data/static"
    volumes:
      - owui-data:/data
      - ./pipelines:/app/pipelines:ro
    healthcheck:
      test: ["CMD", "curl", "-fsSL", "http://localhost:8080/health"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 30s

volumes:
  owui-data: {}
```

### Image pinning

The `main` tag above is the development default. Production deploys (`deploy/docker/.image-locks.yml`) pin Open WebUI by sha256 digest. Bumping the digest is an explicit PR. A nightly canary job (Section 11) runs the OWUI E2E suite against `:main` and posts a warning if upstream changes break our integration; it never blocks builds.

### Tenant-to-group mapping

* Each tenant has exactly one OWUI group named `tenant_<tenant_uuid>`.
* The group is created by `control-plane` on tenant creation via the OWUI admin API.
* Audit outcomes: `OWUI_GROUP_CREATE_SUCCESS`, `OWUI_GROUP_CREATE_FAILURE`.
* Model visibility (Phase 20) will assign models to groups so users see only the models granted to their tenant.

### Defence-in-depth admin stripping

Environment flags disable Open WebUI's native admin surfaces, but the compose stack also runs a Caddy reverse proxy in front of Open WebUI. The Caddy block rejects any path matching `^/admin/.*` with a 404 and blocks the native signup endpoint, so a future regression in upstream defaults cannot accidentally re-expose either surface.

## 8. Chat happy-path data flow

```
+---------+   (1) GET /                          +-----------------------+
| Browser +------------------------------------->| Open WebUI            |
+----+----+                                       | (OAuth gate)         |
     |    (2) redirect to Supabase Auth          +-----------------------+
     v
+------------------+    (3) Google OAuth login     +-------------+
| Supabase GoTrue  +-------------------------------> Google IdP  |
| + custom claim   <-------------------------------+             |
|   hook           |    (4) ID token              +-------------+
+----+-------------+
     | (5) JWT carrying tenant_id, tenants[], role
     |     Custom-access-token hook fires.
     |     auth.users INSERT triggers Supabase Database Webhook
     |       -> control-plane /internal/auth/user-created
     |       -> tenant_users INSERT
     |       -> OWUI group add (async, retries)
     |       -> audit AUTH_SIGNUP_SUCCESS + TENANT_USER_ADD + OWUI_GROUP_ADD_SUCCESS
     v
+------------------+   (6) callback with JWT
| Open WebUI       |<-----------------------------
| session created  |
+----+-------------+
     | (7) user types a prompt -> OWUI internal POST /openai/v1/chat/completions
     v
+--------------------+   (8) pipeline filter hive_jwt_forward
| OWUI pipelines     |   replaces upstream Authorization header with user's
+----+---------------+   Supabase JWT
     | (9) POST http://edge-api:8080/v1/chat/completions
     |     Authorization: Bearer <supabase-jwt>
     v
+--------------------------------------------------------------------+
| edge-api                                                           |
|   middleware 1: selector (JWT vs API-key path)                     |
|   middleware 2: JWT validator (JWKS, sets ctx.User)                |
|     |- audit AUTH_JWT_INVALID on failure -> 401 UNAUTHENTICATED    |
|   middleware 3: tenant settings load (cached 30s)                  |
|   middleware 4: RBAC -- AllowAction(PermChatInvoke)                |
|     |- on deny: audit RBAC_DENY -> 403 FORBIDDEN                   |
|   middleware 5: rate-limit (Phase 19 uses existing limiter;        |
|                  Anthropic Teams shape lands in Phase 21)          |
|   middleware 6: credit pre-check (Phase 19: count + audit only;    |
|                  real engine lands in Phase 21)                    |
|   handler:                                                         |
|     |- audit CHAT_REQUEST severity=INFO (LLM tier)                 |
|     |- dispatch -> http://litellm:4000/v1/chat/completions         |
|     |- stream SSE back to OWUI                                     |
|     |- on completion:                                              |
|         |- INSERT llm_traces row (cost_credits computed via the    |
|         |     existing math/big FX path)                           |
|         |- async fanout llm_traces -> Langfuse if configured       |
|         |- existing provider-blind error sanitiser (Phase 16/17)   |
|         |     reused on every response path                        |
+----+---------------------------------------------------------------+
     | (10) LiteLLM -> provider (OpenAI / Anthropic / OpenRouter / Groq / ...)
     v
+-------------+
| Provider    |
+-------------+
```

### Invariants

* `tenant_id` flows only from the JWT. It is never read from a query string, request body, or header. Every handler that touches tenant-scoped data goes through `ctx.TenantID()`. The lint rule `lint-no-direct-tenant-id` blocks reintroductions.
* Stream errors keep the SSE channel open; each error frame contains only the `request_id`. Upstream messages and provider names never leak. The Phase 16 / Phase 17 sanitiser is the single boundary.
* `cost_credits` is a `bigint` of micro-credits (`1 credit = 1_000_000 micro-credits`). USD never appears on a customer-visible surface. Phase 17 zero-leak enforcement remains in force.
* LLM-tier audit (`CHAT_REQUEST`, `RAG_PERSONAL_RETRIEVAL`) uses the fail-open WAL policy. Security-tier events (JWT invalid, RBAC deny, cross-tenant attempt, panic) use sync-block.

### Failure modes Phase 19 explicitly handles

| Failure | Behaviour |
| --- | --- |
| Supabase JWKS unreachable | Cached keys serve until 24h TTL; alert after grace; reject only if cache empty. |
| Open WebUI pipeline filter not loaded | Chat fails closed (no silent unauthenticated bypass). |
| Signup webhook fails | User signs in but has no tenant. Middleware rejects with `403 NO_TENANT` and audits `AUTH_SIGNIN_FAILURE_NO_TENANT`. A retry job re-runs the signup side effects every 60 seconds for 30 minutes. |
| LiteLLM down | `edge-api` returns `503 SERVICE_UNAVAILABLE` with `request_id`. Chat call is audited with severity `ERROR`. The existing circuit breaker opens. Open WebUI shows a generic message; the provider name never surfaces. |
| Postgres audit insert fails (security tier) | Request fails with `503 SERVICE_UNAVAILABLE`. Local WAL drains LLM-tier events. |
| Audit sink (Langfuse, ELK, ...) unreachable | Outbox accumulates. Chat is unaffected. Alertmanager fires after grace. |

## 9. edge-api JWT middleware and Phase 18 RBAC reuse

### Permission set

```go
const (
    // Phase 18 (already shipped):
    PermAdminAccess    Permission = "ADMIN_ACCESS"
    PermAccountManage  Permission = "ACCOUNT_MANAGE"
    PermBillingView    Permission = "BILLING_VIEW"
    // ... eight more Phase 18 permissions

    // Phase 19 additions:
    PermChatInvoke         Permission = "CHAT_INVOKE"
    PermTenantSettingRead  Permission = "TENANT_SETTING_READ"
    PermTenantSettingWrite Permission = "TENANT_SETTING_WRITE"
    PermTenantSwitch       Permission = "TENANT_SWITCH"
    PermAuditRead          Permission = "AUDIT_READ"
)
```

### Role-to-permission map

```go
var rolePermissions = map[Role][]Permission{
    RoleOwner: {
        PermAdminAccess, PermAccountManage, PermBillingView, PermChatInvoke,
        PermTenantSettingRead, PermTenantSettingWrite, PermTenantSwitch,
        PermAuditRead /* + all Phase 18 perms */,
    },
    RoleAdmin: {
        PermAdminAccess, PermChatInvoke, PermTenantSettingRead,
        PermTenantSettingWrite, PermTenantSwitch, PermAuditRead,
    },
    RoleMember: {
        PermChatInvoke, PermTenantSwitch,
    },
    RoleViewer: {
        PermChatInvoke,
    },
}
```

### Cross-tenant guard

```go
func RequireOwnTenant(ctx context.Context, requested uuid.UUID) error {
    if ctx.TenantID() != requested {
        audit.Log(ctx, audit.Event{
            Action:       "CROSS_TENANT_ATTEMPT",
            Severity:     audit.SeverityCritical,
            ResourceType: "tenant",
            ResourceID:   requested.String(),
        })
        return ErrForbidden
    }
    return nil
}
```

### Permissions codegen drift guard

The role-to-permission map is exported to JSON at `apps/web-console/lib/permissions.gen.json` so the admin UI can compute "can I" decisions without an extra API hop. CI step `codegen-permissions-check` regenerates this file from the Go source and diffs it; drift fails the build. This extends the Phase 18 permissions-codegen check that already runs on every PR.

## 10. Error handling

### Customer-visible response shape

```json
{
  "error": {
    "code": "<STABLE_CODE>",
    "message": "<short customer-safe message>",
    "request_id": "<uuid>",
    "type": "<RATE_LIMIT | UNAUTHORIZED | FORBIDDEN | INVALID_REQUEST | SERVICE_UNAVAILABLE | INTERNAL>"
  }
}
```

### Stable error codes introduced by Phase 19

| Code | HTTP | Meaning |
| --- | --- | --- |
| `UNAUTHENTICATED` | 401 | No or invalid JWT / API key. |
| `JWT_EXPIRED` | 401 | JWT past `exp`. Hint: refresh. |
| `NO_TENANT` | 403 | Authenticated user has no tenant membership. |
| `FORBIDDEN` | 403 | RBAC deny. |
| `CROSS_TENANT` | 403 | Tenant-scope violation. Audited at `CRITICAL`. |
| `INVALID_TENANT_SETTING` | 400 | Unknown setting key or invalid value. |
| `SERVICE_UNAVAILABLE` | 503 | Upstream or dispatch failure. |
| `INTERNAL` | 500 | Last-resort generic. |

### Provider-blind sanitisation

The Phase 16 / 17 sanitiser remains the single boundary between upstream provider responses and customer-visible payloads. Phase 19 introduces no bypass. Sanitisation runs at:

* the `edge-api` response writer;
* `audit_log` `before_json` / `after_json` columns (raw upstream text is replaced by its sha256);
* `llm_traces.prompt_hash` and `completion_hash` (content is included only when `LANGFUSE_INCLUDE_CONTENT=true`).

### Retry, backoff, and circuit breakers

| Caller | Target | Policy |
| --- | --- | --- |
| JWT validator | Supabase JWKS | 3 attempts at 200ms / 500ms / 2s; cached results serve until 24h TTL. |
| Signup webhook | Open WebUI admin API | 5 attempts over 30s, then 30 min slow retry. |
| Audit sink worker | Each configured sink | Exponential backoff 1s → 5 min cap, dead-letter after 1h of failures. |
| edge-api dispatcher | LiteLLM | Existing edge-api retry policy (unchanged). |

Circuit breakers reused from previous phases protect both LiteLLM and Supabase JWKS. The Supabase JWKS breaker, when open with an empty cache, returns `503 SERVICE_UNAVAILABLE` (not `401`) because the failure is in the auth infrastructure, not in the credential.

### Panic recovery

The existing top-level recover middleware writes `SERVER_PANIC` at severity `CRITICAL`, returns 500 with a `request_id`, and never leaks the stack to customers. Stacks are written only to server logs.

## 11. Testing strategy

Every test surface uses real environments. No mocks for Supabase, Open WebUI, LiteLLM, providers, or pgvector.

### Surface 1 — Unit and integration (Go), runs in CI per push

* `testcontainers-go` boots Postgres, Redis, LiteLLM, and Open WebUI.
* LiteLLM is configured with real Anthropic and OpenAI keys from CI secrets, using the cheapest test models.
* A CI-scoped Supabase test project provides Auth + JWKS.
* Coverage targets:
  * `internal/auth/jwt_supabase`, `internal/audit`, `internal/tenant/settings`: ≥ 80% line, ≥ 70% branch.
  * Identity-bridge handlers: ≥ 90% line.
  * Audit helpers: ≥ 95% line (compliance-critical).
* Required tests:
  * `jwt_supabase_integration_test.go` — JWKS rotation, expired token rejection, custom-claim hook injects `tenant_id`, malformed token, missing `tenant_id` claim, RBAC deny audit row written, cross-tenant attempt blocked and audited.
  * `audit_chain_integrity_test.go` — inserts ≥100 events across two partitions and verifies the full hash chain end-to-end.

### Surface 2 — Playwright user flows, runs in CI per push

Located under `apps/web-console/e2e/phase-19/`. Configured via the existing `playwright.config.ts`. Runs against the full Docker compose stack via the existing `--profile test` (extended with the Open WebUI service).

| File | Journey |
| --- | --- |
| `01-signin-google.spec.ts` | OWUI root → Supabase → Google login (test acct) → OWUI lands signed in. Assertions: audit rows for `AUTH_SIGNUP_SUCCESS`, `TENANT_USER_ADD`, `OWUI_GROUP_ADD_SUCCESS`. |
| `02-first-chat.spec.ts` | Signed-in user sends "hi" and receives a streamed response. Assertions: `llm_traces` row with correct `tenant_id`, `user_id`, `model`, tokens, `cost_credits`; audit `CHAT_REQUEST` with severity `INFO`. |
| `03-tenant-isolation.spec.ts` | Sign in as user A in tenant T1 and user B in tenant T2. Each user sees only their tenant's models. A cannot read T2 settings (`403 CROSS_TENANT`, severity `CRITICAL` audit row). |
| `04-tenant-switch.spec.ts` | User in T1 + T2; calls switch endpoint; JWT refreshes; subsequent chat is bound to T2. Audit `TENANT_SWITCH`. |
| `05-jwt-expiry.spec.ts` | Force JWT expiry mid-session. Next chat returns `401 JWT_EXPIRED`; refresh succeeds; retry succeeds. |
| `06-no-tenant-block.spec.ts` | New Google user signs up without invite or matching domain. Assertion: `403 NO_TENANT`, audit `AUTH_SIGNIN_FAILURE_NO_TENANT`. After admin invite, retry succeeds. |
| `07-cross-tenant-attack.spec.ts` | Signed-in T1 user crafts an API call with T2 `tenant_id` in the body. Assertion: `403 CROSS_TENANT` and audit `CROSS_TENANT_ATTEMPT` severity `CRITICAL`. |

### Surface 3 — Audit-evidence Go tests, runs in CI per push

* `tests/compliance/audit_chain_integrity_test.go` — runs after the E2E suite; verifies the chain over every partition produced during the run.
* `tests/compliance/audit_coverage_test.go` — table-driven against `docs/compliance/SOC2-LOG-COVERAGE.md`. For every documented TSC control, asserts at least one matching audit `action` was emitted during the E2E run. CI fails if any control has zero matches.
* `tests/compliance/audit_sink_failure_test.go` — kills Langfuse mid-run; asserts chat still works, outbox accumulates, recovery drains, `audit_outbox_dlq` stays empty under the retry cap.
* `tests/compliance/audit_retention_test.go` — runs the cold-archive job; verifies parquet integrity hash matches Postgres rows.

### Surface 4 — Open WebUI direct E2E (dev-time + scheduled)

Located under `apps/web-console/e2e/phase-19/owui/` with its own `playwright.owui.config.ts` (baseURL `http://localhost:3002`).

Run modes:

* Local dev: `pnpm --filter web-console e2e:owui`.
* Targeted: `pnpm --filter web-console e2e:owui -- -g "rag"`.
* Nightly CI: `.github/workflows/phase-19-owui-nightly.yml`, cron `0 6 * * *` UTC, posts results to Slack / Sentry on red.
* Manual dispatch: `workflow_dispatch` on PRs labelled `ci:e2e-owui`.
* Per-commit CI: skipped (would balloon CI cost and Google OAuth quota).

Test journeys:

| File | Journey |
| --- | --- |
| `01-chat-send-stream.spec.ts` | Send a message, assert stream starts within budget, completes, history persists. |
| `02-chat-multi-turn.spec.ts` | Multi-turn conversation continuity (second message includes first context). |
| `03-chat-model-switch.spec.ts` | Switch model mid-session; UI reflects new model; subsequent calls go to it. |
| `04-rag-personal-pdf-upload.spec.ts` | Upload `fixtures/policy.pdf`; assert OWUI ingests, embeddings land in pgvector. |
| `05-rag-personal-citation.spec.ts` | Ask a known question grounded in the uploaded PDF; assert citation references the doc and answer contains the anchor phrase. |
| `06-tenant-model-visibility.spec.ts` | Assert only models granted to the user's tenant group are visible. |
| `07-image-upload-vision.spec.ts` | Upload an image, run a vision model, assert the model response references image content. |
| `08-signout.spec.ts` | Sign out; subsequent navigation prompts for login. |
| `performance/ttfb.spec.ts` | First-token latency budget. |
| `performance/embed-latency.spec.ts` | Personal-doc upload → ready budget. |

Budget rules:

* Per test ≤ 30s. Per file ≤ 2 min. Full suite ≤ 15 min.
* LLM calls use the cheapest model on the configured providers.
* Spend cap: `OWUI_E2E_MAX_USD_PER_RUN` (default `$1`). Suite aborts if exceeded.
* Cost summary surfaced in the GitHub Action output.

Fixtures live under `apps/web-console/e2e/phase-19/owui/fixtures/` (`policy.pdf` plus `expected-citations.json`).

A test-mode toggle on the JWT-forward pipeline (`OWUI_E2E_MODE=true`) pins `temperature=0` and `top_p=1` for deterministic assertions. The toggle is dev / CI only and is never enabled in Hive Cloud or EnterpriseEdge production.

Compose addition:

```yaml
profiles: [test-owui]   # opt-in, default off
```

Boot for the OWUI E2E suite:

```
docker compose --profile test --profile test-owui --profile observability-langfuse up -d
```

Including `observability-langfuse` validates the Langfuse fanout for every chat call (it lets the audit-coverage test assert `LANGFUSE_TRACE_PRESENT` for every `CHAT_REQUEST`).

### CI wiring

* New workflow `.github/workflows/phase-19.yml` runs Surfaces 1–3 on every push and PR.
* `.github/workflows/phase-19-owui-nightly.yml` runs Surface 4 nightly at 06:00 UTC and on `ci:e2e-owui`-labelled PRs.
* Required checks before merge: unit, integration, Playwright happy path (Surface 2), `audit_chain_integrity_test`, `audit_coverage_test`, `lint-no-bare-role-check`, `lint-no-direct-tenant-id`, `lint-no-direct-tenant-setting`, `lint-no-direct-audit-write`, `codegen-permissions-check`.

### Artefact collection

* Playwright screenshots, videos, and traces on failure under `apps/web-console/test-results/` (uploaded as workflow artefacts).
* Go race detector and `-coverprofile` for every Go test invocation; coverage uploaded.
* Compose logs streamed to file on any E2E failure and attached to the CI run.
* `docs/compliance/SOC2-LOG-COVERAGE.md` is regenerated on every CI run with an HTML table mapping TSC control → audit `action` → count for the run.

## 12. Open questions and deferred decisions

These are tracked here rather than answered now. The implementation plan that follows this spec includes a research task for each.

* **OWUI pipeline path for JWT forwarding** — confirm that the Open WebUI `Filter.inlet` hook can rewrite the upstream `Authorization` header in the OpenAI-compatible request emitted to `edge-api`. Fallback is the small Go reverse-proxy sidecar described in Section 6.
* **Credit-engine shape (Phase 21 prerequisite)** — the direction is the Anthropic Teams model. Phase 21 will design the tables, scheduler, and admin UI in detail.
* **Hive Cloud invite flow** — for Phase 19 we only require email-domain auto-assignment and invite-token resolution. The full self-service multi-tenant signup ships in Phase 23.
* **Audit retention duration** — default 7 years cold archive is fine for Hive Cloud (BD market). EnterpriseEdge customers may need configurable retention; we will expose `AUDIT_COLD_ARCHIVE_RETENTION_YEARS` as an env knob in Phase 24 (self-host packaging).
* **Reverse-proxy choice** — Caddy is the default for the defence-in-depth admin block. We will revisit in Phase 24 if NGINX is more familiar to EnterpriseEdge ops teams.

## 13. Phase sequence (decided)

| Phase | Title | Scope summary |
| --- | --- | --- |
| 19 | Foundation slice | This spec. |
| 20 | Provider catalog | Hybrid catalog (stock seeded + custom DB-managed) with LiteLLM YAML hot-reload. Admin UI for custom providers. |
| 21 | Credit and quota engine | Anthropic Teams shape: tenant pool, per-user soft cap, monthly grant, extra-usage top-up, bucket rate limits. |
| 22 | Shared knowledge-base RAG | Admin upload in web-console, embeddings via LiteLLM, retrieval injection by `edge-api`. |
| 23 | Admin console pages | Audit viewer, tenant settings UI, users / roles, credit grants, provider mgmt. |
| 24 | Self-host packaging | EnterpriseEdge bootstrap script, single-tenant defaults, docs. |
| 25 | Payments tenant-gating | Existing Stripe / bKash / SSLCommerz gated behind `ENABLE_PUBLIC_BILLING`. Hive Cloud cutover. |
| 26 | Web search tool | Append-numbered scope addition. Execute after Phase 21 and before Phase 24/25 if included in v1.1 launch scope. |

Each phase ships an independently testable slice. Each phase wires its events through the Phase 19 audit helper and exposes any new tenant-toggleable behaviour through the Phase 19 settings resolver. GSD ceremony is skipped per project decision; specs live under `docs/superpowers/specs/` and the implementation plans under `docs/superpowers/plans/`.

## 14. Acceptance criteria for Phase 19

Phase 19 is complete when all of the following hold:

1. The tenant-settings table, enum, RLS policies, and Go resolver are merged, with a migration that backfills a default `HIVE_CLOUD` tenant for the dev environment and an `ENTERPRISE_EDGE` tenant for the test environment.
2. The Supabase custom-access-token hook is registered and verified by an integration test that signs a real user in and inspects the resulting JWT.
3. Open WebUI is reachable at `http://localhost:3002`, native admin paths return 404 at the reverse proxy, OIDC against Supabase works end-to-end, and the JWT-forward pipeline filter is loaded.
4. `edge-api` accepts both Supabase JWTs and the existing API keys; the same `ctx.User` is populated by both paths.
5. `audit_log`, `audit_outbox`, `audit_outbox_dlq`, and `llm_traces` are created. The hash-chain verifier runs daily and emits `AUDIT_CHAIN_VERIFY_FAIL` if any partition fails.
6. Every audit `action` listed in Section 5 is emitted at least once during the Playwright suite.
7. All CI checks listed in Section 11 pass on the merge.
8. `docs/compliance/SOC2-LOG-COVERAGE.md` exists, lists every Phase 19 TSC control with its matching audit action, and is regenerated by CI.
9. Open WebUI nightly E2E (Surface 4) succeeds against the pinned image at least once before merge.
10. The lint rules `lint-no-direct-tenant-id`, `lint-no-direct-tenant-setting`, and `lint-no-direct-audit-write` are enforced by CI.
