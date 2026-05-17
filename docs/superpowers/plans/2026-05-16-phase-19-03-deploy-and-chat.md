# Phase 19 / Plan 03 — Open WebUI Deploy + Chat Happy-Path + Sink Fanout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up Open WebUI in the compose stack with native admin stripped, forward the user's Supabase JWT to `edge-api` via a pipeline filter, complete the chat happy-path so a streamed response writes a `llm_traces` row, and drain audit events to optional sinks (ELK / Loki / Datadog / Splunk / Langfuse) with retry + dead-letter. The Phase 19 chat round-trip works end-to-end after this plan merges.

**Architecture:** Plan 01 built the data layer. Plan 02 built the auth layer. Plan 03 builds the running surfaces — OWUI in front, chat dispatch in `edge-api`, audit sink fanout in `control-plane`. The chat dispatch handler validates JWT, checks `PermChatInvoke`, dispatches to LiteLLM, streams the response back to OWUI, and writes one `llm_traces` row + one `CHAT_REQUEST` audit event on completion. The sink worker reads `audit_outbox` rows, fans out to each configured sink with exponential backoff, and moves exhausted rows to `audit_outbox_dlq`. The LLM-tier WAL drainer runs on a ticker, draining any disk-buffered events when Postgres is healthy.

**Tech stack:** Open WebUI (`ghcr.io/open-webui/open-webui:main`, pinned by sha in `deploy/docker/.image-locks.yml`), Caddy (`caddy:2-alpine`, existing in compose), LiteLLM (existing), Langfuse Python SDK shape (HTTP only — no SDK dep), Go 1.24, `pgx`, OWUI Pipelines (Python) for the JWT forwarder. Compose profile additions: `local` already exists; we add `test-owui` (opt-in, default off) for the OWUI E2E suite that lands in Plan 04.

---

## File Structure (Plan 03)

**New files (created):**
- `supabase/migrations/20260516_08_phase19_tenant_invites.sql` — `tenant_invites` table (consumed by signup webhook in Plan 02).
- `supabase/migrations/20260516_09_phase19_tenant_email_domains.sql` — `tenant_email_domains` table.
- `deploy/docker/pipelines/hive_jwt_forward.py` — OWUI Pipelines filter file.
- `deploy/docker/Caddyfile.owui` — Caddy block fronting OWUI; 404s on `/admin/*` and signup paths.
- `deploy/docker/.image-locks.yml` — image sha256 pins (created if not already present).
- `apps/edge-api/internal/chat/dispatch.go` — chat happy-path handler.
- `apps/edge-api/internal/chat/dispatch_test.go` — integration test (real Postgres, fake LiteLLM).
- `apps/edge-api/internal/chat/trace.go` — `llm_traces` writer.
- `apps/edge-api/internal/errors/codes.go` — stable error code constants + response writer.
- `apps/edge-api/internal/errors/codes_test.go` — sanitiser regression test.
- `apps/control-plane/internal/auditworker/worker.go` — outbox drainer + sink dispatcher.
- `apps/control-plane/internal/auditworker/worker_test.go` — integration test with two fake sinks.
- `apps/control-plane/internal/auditworker/sinks/elk.go` — ELK sink adapter.
- `apps/control-plane/internal/auditworker/sinks/loki.go` — Loki sink adapter.
- `apps/control-plane/internal/auditworker/sinks/datadog.go` — Datadog sink adapter.
- `apps/control-plane/internal/auditworker/sinks/splunk.go` — Splunk HEC sink adapter.
- `apps/control-plane/internal/auditworker/sinks/langfuse.go` — Langfuse sink adapter (LLM-trace events only).
- `apps/control-plane/internal/auditworker/sinks/sentry.go` — Sentry sink adapter (severity ≥ ERROR only).
- `apps/control-plane/internal/auditworker/sinks/sinks_test.go` — per-sink shape tests.
- `apps/control-plane/internal/auditverifier/verifier.go` — daily hash-chain integrity job.
- `apps/control-plane/internal/auditverifier/verifier_test.go`
- `apps/control-plane/internal/auditarchive/archive.go` — daily cold archive job (Postgres → parquet → Supabase Storage).
- `apps/control-plane/internal/auditarchive/archive_test.go`
- `apps/control-plane/internal/waldrainer/drainer.go` — LLM-tier WAL drainer ticker.

**Existing files (modified):**
- `deploy/docker/docker-compose.yml` — new `open-webui` and (if not present) `caddy` services; new `test-owui` profile; new compose volumes; new env wiring.
- `.env.example` — add OWUI / Caddy / Langfuse / sink env vars (placeholders only).
- `apps/edge-api/cmd/edge-api/main.go` — mount `/v1/chat/completions` to new dispatch handler.
- `apps/control-plane/cmd/control-plane/main.go` — start sink worker, verifier, archive job, WAL drainer goroutines.

---

## Task 1: `tenant_invites` + `tenant_email_domains` migrations

**Files:**
- Create: `supabase/migrations/20260516_08_phase19_tenant_invites.sql`
- Create: `supabase/migrations/20260516_09_phase19_tenant_email_domains.sql`

- [ ] **Step 1: Write the invites migration**

```sql
-- supabase/migrations/20260516_08_phase19_tenant_invites.sql
-- Phase 19 — invite tokens consumed during OAuth state callback.

BEGIN;

CREATE TABLE public.tenant_invites (
  token       text PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('OWNER','ADMIN','MEMBER','VIEWER')) DEFAULT 'MEMBER',
  email_hint  text,
  created_by  uuid REFERENCES auth.users(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  consumed_at timestamptz,
  consumed_by uuid REFERENCES auth.users(id)
);

CREATE INDEX tenant_invites_tenant_idx   ON public.tenant_invites(tenant_id);
CREATE INDEX tenant_invites_active_idx   ON public.tenant_invites(tenant_id) WHERE consumed_at IS NULL;

ALTER TABLE public.tenant_invites ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_invites_isolation ON public.tenant_invites
  FOR ALL TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON public.tenant_invites TO authenticated;
GRANT SELECT ON public.tenant_invites TO hive_app;

COMMIT;
```

- [ ] **Step 2: Write the email-domains migration**

```sql
-- supabase/migrations/20260516_09_phase19_tenant_email_domains.sql
-- Phase 19 — EnterpriseEdge default tenant-domain auto-assignment.

BEGIN;

CREATE TABLE public.tenant_email_domains (
  domain     text PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  added_by   uuid REFERENCES auth.users(id),
  added_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tenant_email_domains_tenant_idx ON public.tenant_email_domains(tenant_id);

ALTER TABLE public.tenant_email_domains ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_email_domains_isolation ON public.tenant_email_domains
  FOR ALL TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT, INSERT, DELETE ON public.tenant_email_domains TO authenticated;
GRANT SELECT ON public.tenant_email_domains TO hive_app;

COMMIT;
```

- [ ] **Step 3: Apply both migrations**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && psql \"$SUPABASE_DB_URL\" -f supabase/migrations/20260516_08_phase19_tenant_invites.sql -f supabase/migrations/20260516_09_phase19_tenant_email_domains.sql"
```

Expected output: `CREATE TABLE`, `CREATE INDEX` x3, `ALTER TABLE` x2, `CREATE POLICY` x2, `GRANT` x4, `COMMIT` x2.

- [ ] **Step 4: Commit**

```
git add supabase/migrations/20260516_08_phase19_tenant_invites.sql \
        supabase/migrations/20260516_09_phase19_tenant_email_domains.sql
git commit -m "feat(phase-19): add tenant_invites and tenant_email_domains tables"
```

---

## Task 2: Open WebUI compose service + Caddy admin block

**Files:**
- Modify: `deploy/docker/docker-compose.yml`
- Create: `deploy/docker/Caddyfile.owui`
- Create: `deploy/docker/.image-locks.yml`
- Modify: `.env.example`

- [ ] **Step 1: Add the OWUI service to `docker-compose.yml`**

Inside the `services:` block, alongside `edge-api`, `control-plane`, `litellm`, etc., add:

```yaml
  open-webui:
    image: ghcr.io/open-webui/open-webui@sha256:REPLACE_WITH_PINNED_DIGEST
    profiles: ["local"]
    ports:
      - "3002:8080"
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
      # Hive product branding. LICENSE-DECISION.md: latest Open WebUI image with
      # WEBUI_NAME and visible surfaces overridden to "Hive"; upstream Open WebUI
      # branding-preservation license risk is accepted by the project owner.
      WEBUI_NAME: "Hive"
      WEBUI_URL: "${HIVE_CHAT_URL:-http://localhost:3002}"
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

  caddy-owui:
    image: caddy:2-alpine
    profiles: ["local"]
    ports:
      - "3003:80"
    depends_on:
      open-webui: { condition: service_healthy }
    volumes:
      - ./Caddyfile.owui:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
      - caddy-config:/config

volumes:
  owui-data: {}
  caddy-data: {}
  caddy-config: {}
```

If `caddy-data` / `caddy-config` volumes already exist in the file, do not duplicate; reuse.

- [ ] **Step 2: Write the Caddyfile**

```
# deploy/docker/Caddyfile.owui
:80 {
  # Block native OWUI admin and signup surfaces at the proxy layer.
  @blocked {
    path /admin/*
    path /api/v1/admin/*
    path /signup
    path /auths/signup
  }
  respond @blocked 404

  reverse_proxy open-webui:8080 {
    transport http {
      response_header_timeout 60s
    }
  }
}
```

- [ ] **Step 3: Pin OWUI image digest in `.image-locks.yml`**

```yaml
# deploy/docker/.image-locks.yml
# Production deploys MUST consume the digests here, not floating tags.
# Bumping a digest is an explicit PR. The nightly canary job runs
# against :main and posts a warning if upstream changes break
# integration; it never blocks builds.

images:
  open-webui:
    repository: ghcr.io/open-webui/open-webui
    tag: main
    digest: sha256:REPLACE_WITH_LATEST_DIGEST_FROM_GHCR

  caddy:
    repository: caddy
    tag: 2-alpine
    digest: sha256:REPLACE_WITH_LATEST_DIGEST
```

Resolve the actual digest before merge:

```
docker buildx imagetools inspect ghcr.io/open-webui/open-webui:main \
  --format '{{json .Manifest}}' | jq -r '.digest'
docker buildx imagetools inspect caddy:2-alpine \
  --format '{{json .Manifest}}' | jq -r '.digest'
```

Replace the two `REPLACE_WITH_*` strings with the resolved values in both `.image-locks.yml` and the `image:` field of the `open-webui` service in `docker-compose.yml`.

- [ ] **Step 4: Extend `.env.example`**

Append a Phase 19 block:

```
# --- Phase 19: Open WebUI + Supabase OAuth ---
HIVE_CHAT_URL=http://localhost:3002
SUPABASE_OAUTH_CLIENT_ID=
SUPABASE_OAUTH_CLIENT_SECRET=

# --- Phase 19: signup webhook shared secret ---
HIVE_SIGNUP_WEBHOOK_SECRET=

# --- Phase 19: audit sinks (all optional) ---
AUDIT_SINK_FAILURE_GRACE_MINUTES=5
AUDIT_SINK_ELK_URL=
AUDIT_SINK_ELK_API_KEY=
AUDIT_SINK_LOKI_URL=
AUDIT_SINK_DATADOG_API_KEY=
AUDIT_SINK_DATADOG_SITE=datadoghq.com
AUDIT_SINK_SPLUNK_HEC_URL=
AUDIT_SINK_SPLUNK_HEC_TOKEN=
SENTRY_DSN=

# Langfuse
LANGFUSE_HOST=
LANGFUSE_PUBLIC_KEY=
LANGFUSE_SECRET_KEY=
LANGFUSE_INCLUDE_CONTENT=false

# Audit retention + WAL
AUDIT_WAL_DIR=/var/lib/hive/audit-wal
AUDIT_COLD_ARCHIVE_RETENTION_YEARS=7
AUDIT_PARTITION_MONTHS_HOT=3

# OWUI admin token used by control-plane signup webhook
OWUI_BASE_URL=http://open-webui:8080
OWUI_ADMIN_TOKEN=
```

- [ ] **Step 5: Boot the stack and smoke-test OWUI reachability**

```
cd deploy/docker && docker compose --env-file ../../.env --profile local up -d --build
```

Wait for healthy status, then:

```
curl -fsSL http://localhost:3002/health   # expect 200 OK
curl -fsSL http://localhost:3003/health   # expect 200 via Caddy
curl -o /dev/null -w '%{http_code}\n' http://localhost:3003/admin/users  # expect 404
```

Expected last line: `404`. (Direct hit on port 3002 may still surface admin UI HTML in the response, depending on OWUI version; the Caddy block at 3003 is the user-facing surface.)

- [ ] **Step 6: Commit**

```
git add deploy/docker/docker-compose.yml deploy/docker/Caddyfile.owui \
        deploy/docker/.image-locks.yml .env.example
git commit -m "feat(phase-19): add Open WebUI service with OIDC + Caddy admin block"
```

---

## Task 3: OWUI pipeline filter — JWT forward to upstream

**Files:**
- Create: `deploy/docker/pipelines/hive_jwt_forward.py`

- [ ] **Step 1: Write the pipeline filter**

```python
# deploy/docker/pipelines/hive_jwt_forward.py
"""
hive_jwt_forward — OWUI Pipelines filter

Replaces the static OWUI -> edge-api shim API key with the signed-in
user's Supabase access token on every outgoing OpenAI-compatible request.
The user dict OWUI passes into the filter carries the OAuth-issued tokens
in user["oauth_sub"]["access_token"] when ENABLE_OAUTH_SIGNUP=true and
the upstream client (edge-api) is configured against the user's IdP.

Failure modes:
  - No token on the request: leave Authorization unchanged. edge-api's
    middleware will return 401 UNAUTHENTICATED + audit AUTH_JWT_INVALID.
  - OWUI_E2E_MODE env set: also pin temperature=0 / top_p=1 for
    deterministic Playwright assertions.
"""

from __future__ import annotations
import os
from typing import Any


class Pipeline:
    class Valves:
        priority: int = 0

    def __init__(self) -> None:
        self.name = "hive_jwt_forward"
        self.valves = self.Valves()
        self.e2e_mode = os.environ.get("OWUI_E2E_MODE", "").lower() in ("1", "true")

    async def on_startup(self) -> None:
        return None

    async def on_shutdown(self) -> None:
        return None

    async def inlet(self, body: dict, user: dict | None = None) -> dict:
        if not isinstance(body, dict):
            return body

        token = None
        if user is not None:
            oauth = user.get("oauth_sub") or {}
            if isinstance(oauth, dict):
                token = oauth.get("access_token") or oauth.get("id_token")
            token = token or user.get("token")

        meta = body.setdefault("__metadata", {})
        if token:
            meta["upstream_auth"] = f"Bearer {token}"

        if self.e2e_mode:
            body.setdefault("temperature", 0)
            body.setdefault("top_p", 1)

        return body

    async def outlet(self, body: Any, user: dict | None = None) -> Any:
        return body
```

A note for the implementing engineer: Open WebUI Pipelines exposes `__metadata.upstream_auth` to the upstream HTTP client only on supported builds. The Plan 03 OWUI version is pinned in `.image-locks.yml`; verify by running the Step 2 smoke test below. If the pinned build does not honour `__metadata.upstream_auth`, fall back to the small Go reverse-proxy sidecar described in Spec §6 — defer that decision to the integration test outcome, do not pre-build the sidecar.

- [ ] **Step 2: Pipeline smoke test (manual but scripted)**

In one shell, tail `edge-api` logs:

```
cd deploy/docker && docker compose logs -f edge-api
```

In another, sign in to `http://localhost:3003` via Google OAuth (test account) and send any chat message. Look for one line in the edge-api log matching `AUTH_JWT_INVALID` (no — we expect success) or, on success, the dispatch trace line emitted by Task 4. Expected: success path, no `AUTH_JWT_INVALID`. If `AUTH_JWT_INVALID` appears, the pipeline filter is not forwarding the JWT — investigate per the note above and adopt the fallback sidecar.

- [ ] **Step 3: Commit**

```
git add deploy/docker/pipelines/hive_jwt_forward.py
git commit -m "feat(phase-19): add OWUI pipeline filter forwarding user JWT to edge-api"
```

---

## Task 4: edge-api chat dispatch handler

**Files:**
- Create: `apps/edge-api/internal/chat/dispatch.go`
- Create: `apps/edge-api/internal/chat/trace.go`
- Test: `apps/edge-api/internal/chat/dispatch_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/edge-api/internal/chat/dispatch_test.go
package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hive/edge-api/internal/auth"
	"hive/edge-api/internal/chat"
)

func TestDispatch_HappyPath_WritesLLMTraceAndAuditsCHAT_REQUEST(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	// Fake LiteLLM
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		f.Flush()
		_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\n"))
		f.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer upstream.Close()

	tenantID := uuid.New()
	userID := uuid.New()

	h := chat.NewDispatch(chat.Deps{
		Pool:         pool,
		LiteLLMURL:   upstream.URL,
		DeploySHA:    "test",
		Env:          "test",
	})

	body := strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: userID, TenantID: tenantID, Role: "MEMBER", Email: "x@y.example",
	}))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "data: ")
	require.Contains(t, rec.Body.String(), "[DONE]")

	// llm_traces row written
	var rows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.llm_traces
		  WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID).Scan(&rows))
	require.Equal(t, 1, rows)

	// CHAT_REQUEST audit row written (LLM tier)
	var actions []string
	q, err := pool.Query(ctx,
		`SELECT action FROM public.audit_log WHERE tenant_id=$1`, tenantID)
	require.NoError(t, err)
	defer q.Close()
	for q.Next() {
		var a string
		_ = q.Scan(&a)
		actions = append(actions, a)
	}
	require.Contains(t, actions, "CHAT_REQUEST")
}

func TestDispatch_NoTenant_403NoTenant(t *testing.T) {
	pool := newPool(t, context.Background())
	t.Cleanup(func() { pool.Close() })

	h := chat.NewDispatch(chat.Deps{Pool: pool, LiteLLMURL: "http://unused", DeploySHA: "s", Env: "test"})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New() /* TenantID nil */}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.NopCloser(bytes.NewReader(rec.Body.Bytes()))).Decode(&errBody)
	require.Equal(t, "NO_TENANT", errBody.Error.Code)
}

func newPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
```

- [ ] **Step 2: Write `trace.go`**

```go
// apps/edge-api/internal/chat/trace.go
package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TraceRow struct {
	TenantID        uuid.UUID
	UserID          uuid.UUID
	RequestID       uuid.UUID
	Model           string
	Provider        string
	InTokens        int
	OutTokens       int
	LatencyMs       int
	CostCredits     int64
	FinishReason    string
	PromptHash      string
	CompletionHash  string
}

func InsertTrace(ctx context.Context, pool *pgxpool.Pool, t TraceRow) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO public.llm_traces (
			tenant_id, user_id, request_id, model, provider,
			in_tokens, out_tokens, latency_ms, cost_credits,
			finish_reason, prompt_hash, completion_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		t.TenantID, nullableUUID(t.UserID), t.RequestID, t.Model, t.Provider,
		t.InTokens, t.OutTokens, t.LatencyMs, t.CostCredits,
		nullableString(t.FinishReason), t.PromptHash, t.CompletionHash,
	)
	return err
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func nullableUUID(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

- [ ] **Step 3: Write `dispatch.go`**

```go
// apps/edge-api/internal/chat/dispatch.go
package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hive/edge-api/internal/auth"
	apierr "hive/edge-api/internal/errors"
)

type Deps struct {
	Pool       *pgxpool.Pool
	LiteLLMURL string
	DeploySHA  string
	Env        string
	HTTP       *http.Client
}

type Handler struct{ deps Deps }

func NewDispatch(deps Deps) *Handler {
	if deps.HTTP == nil {
		deps.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Handler{deps: deps}
}

type chatRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Stream   bool             `json:"stream,omitempty"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type sseEnvelope struct {
	Usage   *usage `json:"usage,omitempty"`
	Choices []struct {
		FinishReason string `json:"finish_reason,omitempty"`
		Delta        struct {
			Content string `json:"content,omitempty"`
		} `json:"delta,omitempty"`
	} `json:"choices,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthenticated, "missing user")
		return
	}
	if user.TenantID == uuid.Nil {
		apierr.Write(w, http.StatusForbidden, apierr.CodeNoTenant, "no tenant for user")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "body read")
		return
	}
	var parsed chatRequest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "bad json")
		return
	}
	requestID := uuid.New()
	parsed.Stream = true
	body, _ := json.Marshal(parsed)

	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		h.deps.LiteLLMURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "build upstream")
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Request-Id", requestID.String())

	resp, err := h.deps.HTTP.Do(upstream)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeServiceUnavailable, "upstream unreachable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	started := time.Now()
	var totalTokens, inTokens, outTokens int
	var finishReason string
	var completionBuilder strings.Builder

	scanner := bufioScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			_, _ = w.Write([]byte("\n"))
			if flusher != nil { flusher.Flush() }
			continue
		}
		_, _ = w.Write(line)
		_, _ = w.Write([]byte("\n"))
		if flusher != nil { flusher.Flush() }

		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}
		var env sseEnvelope
		if err := json.Unmarshal(payload, &env); err == nil {
			for _, c := range env.Choices {
				if c.Delta.Content != "" {
					completionBuilder.WriteString(c.Delta.Content)
				}
				if c.FinishReason != "" {
					finishReason = c.FinishReason
				}
			}
			if env.Usage != nil {
				inTokens = env.Usage.PromptTokens
				outTokens = env.Usage.CompletionTokens
				totalTokens = env.Usage.TotalTokens
			}
		}
	}

	latency := int(time.Since(started).Milliseconds())
	promptHash := hashString(string(raw))
	completionHash := hashString(completionBuilder.String())
	provider := guessProvider(parsed.Model)
	costCredits := int64(totalTokens) // Phase 19 placeholder; Phase 21 wires real pricing

	_ = InsertTrace(r.Context(), h.deps.Pool, TraceRow{
		TenantID: user.TenantID, UserID: user.ID, RequestID: requestID,
		Model: parsed.Model, Provider: provider,
		InTokens: inTokens, OutTokens: outTokens, LatencyMs: latency,
		CostCredits: costCredits, FinishReason: finishReason,
		PromptHash: promptHash, CompletionHash: completionHash,
	})

	// CHAT_REQUEST is LLM-tier — let the audit Logger pick the WAL path.
	// The Logger is constructed at main.go and propagated via Deps in a
	// follow-up; for Plan 03 we keep the dispatch path self-contained
	// by writing through the *control-plane* audit primitive via a thin
	// Go client. To avoid an inter-service synchronous hop on every
	// chat, we emit through the local audit package compiled into
	// edge-api (same internal/audit code, same DB pool).
	// See Spec §11 — the two services share the package, not an HTTP API.
}
```

`bufioScanner` and `guessProvider` are helpers in `helpers.go`:

```go
// apps/edge-api/internal/chat/helpers.go
package chat

import (
	"bufio"
	"io"
	"strings"
)

func bufioScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return s
}

func guessProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "gpt-"):
		return "openai"
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "openrouter/"):
		return "openrouter"
	default:
		return "unknown"
	}
}
```

The audit `CHAT_REQUEST` emission relies on the `internal/audit` package being imported here. Add to `dispatch.go` near the trace insert:

```go
import (
	// ... existing
	"hive/control-plane/internal/audit"
)

// near end of ServeHTTP, after InsertTrace:
if h.deps.Audit != nil {
	_ = h.deps.Audit.Log(r.Context(), audit.Event{
		TenantID: user.TenantID,
		Actor:    audit.Actor{ID: user.ID, Type: audit.ActorUser},
		Action:   "CHAT_REQUEST",
		Severity: audit.SeverityInfo,
		RequestID: requestID,
		After: map[string]any{
			"model": parsed.Model, "provider": provider,
			"in_tokens": inTokens, "out_tokens": outTokens,
			"latency_ms": latency, "cost_credits": costCredits,
			"finish_reason": finishReason,
		},
	})
}
```

Add `Audit *audit.Logger` to `Deps`. Update the test to construct + pass a real logger built off the test pool.

- [ ] **Step 4: Run the test**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/edge-api/internal/chat/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```
git add apps/edge-api/internal/chat/
git commit -m "feat(phase-19): add chat dispatch with llm_traces row and CHAT_REQUEST audit"
```

---

## Task 5: Stable error code surface

**Files:**
- Create: `apps/edge-api/internal/errors/codes.go`
- Test: `apps/edge-api/internal/errors/codes_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/edge-api/internal/errors/codes_test.go
package errors_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	apierr "hive/edge-api/internal/errors"
)

func TestWrite_ShapeAndType(t *testing.T) {
	rec := httptest.NewRecorder()
	apierr.Write(rec, http.StatusForbidden, apierr.CodeCrossTenant, "no")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var got struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "CROSS_TENANT", got.Error.Code)
	require.Equal(t, "FORBIDDEN", got.Error.Type)
}

func TestWrite_SanitisesProviderLeakInMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	apierr.Write(rec, http.StatusServiceUnavailable, apierr.CodeServiceUnavailable,
		"upstream openai/v1/chat/completions returned rate-limit at $0.0024 per 1k tokens")
	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	require.NotContains(t, got.Error.Message, "openai")
	require.NotContains(t, got.Error.Message, "$0.0024")
}
```

- [ ] **Step 2: Write `codes.go`**

```go
// apps/edge-api/internal/errors/codes.go
package errors

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

type Code string

const (
	CodeUnauthenticated    Code = "UNAUTHENTICATED"
	CodeJWTExpired         Code = "JWT_EXPIRED"
	CodeNoTenant           Code = "NO_TENANT"
	CodeForbidden          Code = "FORBIDDEN"
	CodeCrossTenant        Code = "CROSS_TENANT"
	CodeInvalidTenantSetting Code = "INVALID_TENANT_SETTING"
	CodeInvalidRequest     Code = "INVALID_REQUEST"
	CodeServiceUnavailable Code = "SERVICE_UNAVAILABLE"
	CodeInternal           Code = "INTERNAL"
)

// providerLeakPatterns is the customer-facing sanitiser. Reuses Phase 16/17
// pattern: redact provider names, USD figures, raw upstream URLs.
var providerLeakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(openai|anthropic|openrouter|groq|ollama|vllm|sglang|nim|aura)\b`),
	regexp.MustCompile(`\$\d+(\.\d+)?`),
	regexp.MustCompile(`https?://[^\s]+`),
	regexp.MustCompile(`(?i)\b(upstream|provider|backend|litellm)\b`),
}

func sanitise(msg string) string {
	for _, re := range providerLeakPatterns {
		msg = re.ReplaceAllString(msg, "[redacted]")
	}
	return msg
}

func typeOf(status int, code Code) string {
	switch {
	case status == http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case status == http.StatusForbidden:
		return "FORBIDDEN"
	case status == http.StatusBadRequest:
		return "INVALID_REQUEST"
	case status == http.StatusServiceUnavailable:
		return "SERVICE_UNAVAILABLE"
	case status >= 500:
		return "INTERNAL"
	default:
		return "INTERNAL"
	}
}

func Write(w http.ResponseWriter, status int, code Code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":       string(code),
			"message":    sanitise(msg),
			"request_id": uuid.NewString(),
			"type":       typeOf(status, code),
		},
	})
}
```

- [ ] **Step 3: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/errors/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 4: Commit**

```
git add apps/edge-api/internal/errors/
git commit -m "feat(phase-19): add stable error codes with provider-blind sanitiser"
```

---

## Task 6: Audit sink worker — outbox drainer

**Files:**
- Create: `apps/control-plane/internal/auditworker/worker.go`
- Create: `apps/control-plane/internal/auditworker/sinks/sink.go` (interface)
- Test: `apps/control-plane/internal/auditworker/worker_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/auditworker/worker_test.go
package auditworker_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/audit"
	"hive/control-plane/internal/auditworker"
)

func TestWorker_DrainsOutboxToSink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool := newWorkerPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	sync := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"})
	require.NoError(t, sync.Write(ctx, audit.Event{
		Action: "AUTH_SIGNIN_SUCCESS", Severity: audit.SeverityInfo,
		Actor: audit.Actor{Type: audit.ActorUser},
	}))

	_, err := pool.Exec(ctx,
		`INSERT INTO public.audit_outbox(audit_id, audit_ts, sink)
		   SELECT id, ts, 'fake-sink' FROM public.audit_log WHERE action='AUTH_SIGNIN_SUCCESS' ORDER BY ts DESC LIMIT 1`)
	require.NoError(t, err)

	delivered := make(chan int, 8)
	var mu sync.Mutex
	count := 0
	fake := &fakeSink{
		name: "fake-sink",
		send: func(ctx context.Context, row map[string]any) error {
			mu.Lock(); defer mu.Unlock()
			count++
			delivered <- count
			return nil
		},
	}
	w := auditworker.New(auditworker.Config{
		Pool: pool, Sinks: []auditworker.Sink{fake},
		MaxAttempts: 3, BackoffStart: 50 * time.Millisecond,
	})
	go w.Run(ctx)

	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("sink never received row")
	}
}

func TestWorker_RetriesThenDLQs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool := newWorkerPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	sync := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"})
	require.NoError(t, sync.Write(ctx, audit.Event{
		Action: "AUTH_SIGNIN_SUCCESS", Severity: audit.SeverityInfo,
		Actor: audit.Actor{Type: audit.ActorUser},
	}))
	_, err := pool.Exec(ctx,
		`INSERT INTO public.audit_outbox(audit_id, audit_ts, sink)
		   SELECT id, ts, 'always-fail' FROM public.audit_log WHERE action='AUTH_SIGNIN_SUCCESS' ORDER BY ts DESC LIMIT 1`)
	require.NoError(t, err)

	failer := &fakeSink{
		name: "always-fail",
		send: func(ctx context.Context, row map[string]any) error { return errors.New("nope") },
	}
	w := auditworker.New(auditworker.Config{
		Pool: pool, Sinks: []auditworker.Sink{failer},
		MaxAttempts: 2, BackoffStart: 10 * time.Millisecond,
	})
	go w.Run(ctx)

	require.Eventually(t, func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_outbox_dlq WHERE sink='always-fail'`).Scan(&n)
		return n > 0
	}, 5*time.Second, 100*time.Millisecond)
}

type fakeSink struct {
	name string
	send func(ctx context.Context, row map[string]any) error
}

func (f *fakeSink) Name() string { return f.name }
func (f *fakeSink) Send(ctx context.Context, row map[string]any) error { return f.send(ctx, row) }

func newWorkerPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
```

- [ ] **Step 2: Write `sink.go` interface**

```go
// apps/control-plane/internal/auditworker/sinks/sink.go
package sinks

import "context"

// Sink is the per-destination contract. Name() must match the value
// inserted into audit_outbox.sink.
type Sink interface {
	Name() string
	Send(ctx context.Context, row map[string]any) error
}
```

- [ ] **Step 3: Write `worker.go`**

```go
// apps/control-plane/internal/auditworker/worker.go
package auditworker

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hive/control-plane/internal/auditworker/sinks"
)

type Sink = sinks.Sink

type Config struct {
	Pool         *pgxpool.Pool
	Sinks        []Sink
	MaxAttempts  int
	BackoffStart time.Duration
	BackoffMax   time.Duration
	PollInterval time.Duration
}

type Worker struct {
	cfg     Config
	bySink  map[string]Sink
}

func New(cfg Config) *Worker {
	if cfg.PollInterval == 0 { cfg.PollInterval = 250 * time.Millisecond }
	if cfg.MaxAttempts == 0  { cfg.MaxAttempts  = 8 }
	if cfg.BackoffStart == 0 { cfg.BackoffStart = time.Second }
	if cfg.BackoffMax == 0   { cfg.BackoffMax   = 5 * time.Minute }
	m := make(map[string]Sink, len(cfg.Sinks))
	for _, s := range cfg.Sinks {
		m[s.Name()] = s
	}
	return &Worker{cfg: cfg, bySink: m}
}

func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(w.cfg.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := w.drainOnce(ctx); err != nil {
				slog.Warn("auditworker drain error", "err", err)
			}
		}
	}
}

func (w *Worker) drainOnce(ctx context.Context) error {
	rows, err := w.cfg.Pool.Query(ctx, `
		SELECT o.id, o.audit_id, o.audit_ts, o.sink, o.attempts
		  FROM public.audit_outbox o
		 WHERE o.delivered_at IS NULL
		 ORDER BY o.created_at
		 LIMIT 50
	`)
	if err != nil { return err }
	defer rows.Close()

	type job struct {
		id, auditID int64
		auditTS time.Time
		sink string
		attempts int
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.auditID, &j.auditTS, &j.sink, &j.attempts); err != nil {
			return err
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	for _, j := range jobs {
		sink, ok := w.bySink[j.sink]
		if !ok {
			continue
		}
		payload, err := w.loadPayload(ctx, j.auditID, j.auditTS)
		if err != nil {
			w.markFailed(ctx, j.id, j.attempts+1, err.Error())
			continue
		}
		if err := sink.Send(ctx, payload); err != nil {
			if j.attempts+1 >= w.cfg.MaxAttempts {
				w.toDLQ(ctx, j.id, j.attempts+1, err.Error())
			} else {
				w.markFailed(ctx, j.id, j.attempts+1, err.Error())
			}
			continue
		}
		_, _ = w.cfg.Pool.Exec(ctx,
			`UPDATE public.audit_outbox SET delivered_at=now() WHERE id=$1`, j.id)
	}
	return nil
}

func (w *Worker) loadPayload(ctx context.Context, auditID int64, ts time.Time) (map[string]any, error) {
	var raw []byte
	err := w.cfg.Pool.QueryRow(ctx,
		`SELECT row_to_json(a)::jsonb FROM public.audit_log a WHERE id=$1 AND ts=$2`,
		auditID, ts).Scan(&raw)
	if err != nil { return nil, err }
	var out map[string]any
	return out, json.Unmarshal(raw, &out)
}

func (w *Worker) markFailed(ctx context.Context, id int64, attempts int, msg string) {
	_, _ = w.cfg.Pool.Exec(ctx,
		`UPDATE public.audit_outbox SET attempts=$1, last_error=$2 WHERE id=$3`,
		attempts, msg, id)
}

func (w *Worker) toDLQ(ctx context.Context, id int64, attempts int, msg string) {
	_, _ = w.cfg.Pool.Exec(ctx, `
		WITH del AS (DELETE FROM public.audit_outbox WHERE id=$1 RETURNING *)
		INSERT INTO public.audit_outbox_dlq SELECT * FROM del`, id)
	slog.Warn("auditworker DLQ", "id", id, "attempts", attempts, "err", msg)
}
```

- [ ] **Step 4: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/auditworker/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS` on both worker tests.

- [ ] **Step 5: Commit**

```
git add apps/control-plane/internal/auditworker/
git commit -m "feat(phase-19): add audit outbox drainer with retry, backoff, and DLQ"
```

---

## Task 7: Sink adapters — ELK, Loki, Datadog, Splunk, Sentry, Langfuse

**Files:**
- Create: `apps/control-plane/internal/auditworker/sinks/elk.go`
- Create: `apps/control-plane/internal/auditworker/sinks/loki.go`
- Create: `apps/control-plane/internal/auditworker/sinks/datadog.go`
- Create: `apps/control-plane/internal/auditworker/sinks/splunk.go`
- Create: `apps/control-plane/internal/auditworker/sinks/sentry.go`
- Create: `apps/control-plane/internal/auditworker/sinks/langfuse.go`
- Test: `apps/control-plane/internal/auditworker/sinks/sinks_test.go`

- [ ] **Step 1: Write per-sink unit tests**

```go
// apps/control-plane/internal/auditworker/sinks/sinks_test.go
package sinks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/auditworker/sinks"
)

func TestELK_PostsExpectedShape(t *testing.T) {
	var captured struct {
		Auth string
		Path string
		Body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Auth = r.Header.Get("Authorization")
		captured.Path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&captured.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := sinks.NewELK(sinks.ELKConfig{URL: srv.URL + "/hive-audit/_doc", APIKey: "k"})
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "AUTH_SIGNIN_SUCCESS"}))
	require.Equal(t, "ApiKey k", captured.Auth)
	require.Equal(t, "/hive-audit/_doc", captured.Path)
	require.Equal(t, "AUTH_SIGNIN_SUCCESS", captured.Body["action"])
}

func TestLoki_PostsExpectedShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	s := sinks.NewLoki(sinks.LokiConfig{URL: srv.URL + "/loki/api/v1/push"})
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "RBAC_DENY", "severity": "WARNING"}))
	require.NotNil(t, got["streams"])
}

func TestSentry_OnlyForwardsErrorOrCritical(t *testing.T) {
	called := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++; w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := sinks.NewSentry(sinks.SentryConfig{DSN: srv.URL + "/api/1/store/", Key: "k"})
	require.NoError(t, s.Send(context.Background(), map[string]any{"severity": "INFO"}))
	require.Equal(t, 0, called, "INFO must be skipped")
	require.NoError(t, s.Send(context.Background(), map[string]any{"severity": "CRITICAL"}))
	require.Equal(t, 1, called)
}

func TestLangfuse_SkipsNonLLMActions(t *testing.T) {
	called := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++; w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := sinks.NewLangfuse(sinks.LangfuseConfig{Host: srv.URL, PublicKey: "p", SecretKey: "s"})
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "AUTH_SIGNIN_SUCCESS"}))
	require.Equal(t, 0, called)
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "CHAT_REQUEST", "after_json": map[string]any{"model":"gpt-4o-mini"}}))
	require.Equal(t, 1, called)
}
```

- [ ] **Step 2: Write each adapter**

Each sink follows the same shape. Below is `elk.go`; the rest follow the same pattern (omitted for brevity in this plan — each file is ~40 LOC).

```go
// apps/control-plane/internal/auditworker/sinks/elk.go
package sinks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ELKConfig struct {
	URL    string
	APIKey string
	HTTP   *http.Client
}

type ELK struct{ cfg ELKConfig }

func NewELK(cfg ELKConfig) *ELK {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 5 * time.Second}
	}
	return &ELK{cfg: cfg}
}

func (s *ELK) Name() string { return "elk" }

func (s *ELK) Send(ctx context.Context, row map[string]any) error {
	if s.cfg.URL == "" {
		return errors.New("elk: not configured")
	}
	body, _ := json.Marshal(row)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", "ApiKey "+s.cfg.APIKey)
	}
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("elk: %d %s", resp.StatusCode, string(b))
	}
	return nil
}
```

The implementing engineer writes the other five sink files following the same template, with these per-sink differences:

* **`loki.go`** — POSTs `{"streams":[{"stream":{"action":..,"severity":..},"values":[["<ns>","<json>"]]}]}` to `LokiConfig.URL`; ns = `time.Now().UnixNano()`.
* **`datadog.go`** — POSTs to `https://http-intake.logs.${SITE}/api/v2/logs` with header `DD-API-KEY`; payload is a JSON array of records.
* **`splunk.go`** — POSTs `{"event": row, "sourcetype":"hive:audit"}` to `SplunkConfig.URL` with header `Authorization: Splunk ${TOKEN}`.
* **`sentry.go`** — early-returns when `row["severity"]` is neither `"ERROR"` nor `"CRITICAL"`; otherwise POSTs Sentry envelope.
* **`langfuse.go`** — early-returns when `row["action"]` is not `"CHAT_REQUEST"`; otherwise POSTs `/api/public/ingestion` with three records (trace + span + generation) built from `row["after_json"]`. If `LANGFUSE_INCLUDE_CONTENT=true` is set, also include `prompt`/`completion` (placeholder for Phase 19 — until Plan 03 wires content forwarding, send hashes only).

- [ ] **Step 3: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/auditworker/sinks/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 4: Commit**

```
git add apps/control-plane/internal/auditworker/sinks/
git commit -m "feat(phase-19): add ELK / Loki / Datadog / Splunk / Sentry / Langfuse sink adapters"
```

---

## Task 8: Daily audit-chain integrity verifier

**Files:**
- Create: `apps/control-plane/internal/auditverifier/verifier.go`
- Test: `apps/control-plane/internal/auditverifier/verifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/auditverifier/verifier_test.go
package auditverifier_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/audit"
	"hive/control-plane/internal/auditverifier"
)

func TestVerifier_ChainOK_ReturnsNoMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	sync := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"})
	for i := 0; i < 3; i++ {
		require.NoError(t, sync.Write(ctx, audit.Event{
			Action: "AUTH_SIGNIN_SUCCESS", Severity: audit.SeverityInfo,
			Actor: audit.Actor{Type: audit.ActorUser},
		}))
	}

	v := auditverifier.New(pool)
	mismatches, err := v.VerifyPartition(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, 0, mismatches)
}

func TestVerifier_TamperedRowDetected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	sync := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"})
	require.NoError(t, sync.Write(ctx, audit.Event{
		Action: "AUTH_SIGNIN_SUCCESS", Severity: audit.SeverityInfo,
		Actor: audit.Actor{Type: audit.ActorUser},
	}))
	require.NoError(t, sync.Write(ctx, audit.Event{
		Action: "RBAC_DENY", Severity: audit.SeverityWarning,
		Actor: audit.Actor{Type: audit.ActorUser},
	}))

	// Tamper: flip a byte of row_hash on the first row.
	// (UPDATE on audit_log is REVOKEd from hive_app, so we use the
	// owner role for this test — psql session level, not application.)
	_, err := pool.Exec(ctx, `
		WITH first AS (SELECT id, ts FROM public.audit_log ORDER BY seq LIMIT 1)
		UPDATE public.audit_log SET row_hash = decode('00','hex') || substring(row_hash from 2)
		WHERE id = (SELECT id FROM first) AND ts = (SELECT ts FROM first)`)
	if err != nil {
		t.Skip("tamper requires owner privilege on test DB — skipping in CI containers")
	}

	v := auditverifier.New(pool)
	mismatches, err := v.VerifyPartition(ctx, time.Now())
	require.NoError(t, err)
	require.GreaterOrEqual(t, mismatches, 1)
}

func newPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
```

- [ ] **Step 2: Write `verifier.go`**

```go
// apps/control-plane/internal/auditverifier/verifier.go
package auditverifier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Verifier struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Verifier { return &Verifier{pool: pool} }

// VerifyPartition walks the audit_log partition covering t and recomputes
// row_hash for every row. Returns the count of mismatches (0 = clean).
func (v *Verifier) VerifyPartition(ctx context.Context, t time.Time) (int, error) {
	rows, err := v.pool.Query(ctx, `
		SELECT id, ts, tenant_id, actor_id, actor_type, action,
		       resource_type, resource_id, severity, before_json, after_json,
		       request_id, source_ip::text, user_agent, jwt_claims_digest,
		       deploy_sha, env, seq, prev_hash, row_hash
		  FROM public.audit_log
		 WHERE ts >= date_trunc('month', $1::timestamptz)
		   AND ts <  date_trunc('month', $1::timestamptz) + interval '1 month'
		 ORDER BY seq`,
		t)
	if err != nil { return 0, err }
	defer rows.Close()

	mismatches := 0
	for rows.Next() {
		var (
			id, seq               int64
			ts                    time.Time
			tenantID, actorID,
			requestID             *string
			actorType, action,
			resourceType, resourceID,
			severity, sourceIP,
			userAgent, jwtDigest,
			deploySHA, env         string
			beforeJSON, afterJSON  []byte
			prevHash, rowHash      []byte
		)
		if err := rows.Scan(
			&id, &ts, &tenantID, &actorID, &actorType, &action,
			&resourceType, &resourceID, &severity, &beforeJSON, &afterJSON,
			&requestID, &sourceIP, &userAgent, &jwtDigest,
			&deploySHA, &env, &seq, &prevHash, &rowHash,
		); err != nil { return mismatches, err }

		canon := map[string]any{
			"actor_type": actorType, "action": action,
			"severity": severity, "deploy_sha": deploySHA,
			"env": env, "ts": ts.UTC().Format(time.RFC3339Nano),
		}
		if tenantID != nil   { canon["tenant_id"] = *tenantID }
		if actorID != nil    { canon["actor_id"]  = *actorID  }
		if requestID != nil  { canon["request_id"] = *requestID }
		if resourceType != "" { canon["resource_type"] = resourceType }
		if resourceID != ""   { canon["resource_id"]   = resourceID   }
		if sourceIP != ""     { canon["source_ip"]     = sourceIP     }
		if userAgent != ""    { canon["user_agent"]    = userAgent    }
		if jwtDigest != ""    { canon["jwt_claims_digest"] = jwtDigest }
		if len(beforeJSON) > 0 && string(beforeJSON) != "null" {
			canon["before_json"] = json.RawMessage(beforeJSON)
		}
		if len(afterJSON) > 0 && string(afterJSON) != "null" {
			canon["after_json"] = json.RawMessage(afterJSON)
		}

		raw, _ := json.Marshal(canon)
		sum := sha256.New()
		sum.Write(prevHash)
		sum.Write(raw)
		expect := sum.Sum(nil)
		if !bytes.Equal(expect, rowHash) {
			mismatches++
			fmt.Printf("audit_chain_mismatch id=%d seq=%d\n", id, seq)
		}
	}
	return mismatches, nil
}
```

- [ ] **Step 3: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/auditverifier/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`. The tamper-detection sub-test may skip if the test role lacks UPDATE — that is acceptable in CI; the happy path must pass.

- [ ] **Step 4: Commit**

```
git add apps/control-plane/internal/auditverifier/
git commit -m "feat(phase-19): add daily audit_log chain integrity verifier"
```

---

## Task 9: WAL drainer ticker

**Files:**
- Create: `apps/control-plane/internal/waldrainer/drainer.go`

- [ ] **Step 1: Write `drainer.go`**

```go
// apps/control-plane/internal/waldrainer/drainer.go
package waldrainer

import (
	"context"
	"log/slog"
	"time"

	"hive/control-plane/internal/audit"
)

// Run repeatedly drains the audit-WAL. It runs on a ticker so that LLM-tier
// events buffered to disk during a Postgres outage flush back to Postgres
// as soon as it is reachable.
func Run(ctx context.Context, wal *audit.FileWALWriter, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := wal.Drain(ctx)
			if err != nil {
				slog.Warn("waldrainer error", "err", err)
				continue
			}
			if n > 0 {
				slog.Info("waldrainer drained", "count", n)
			}
		}
	}
}
```

- [ ] **Step 2: Commit (no separate test — covered by `audit/wal_test.go` from Plan 01)**

```
git add apps/control-plane/internal/waldrainer/
git commit -m "feat(phase-19): add LLM-tier WAL drainer ticker"
```

---

## Task 10: Wire chat dispatch, sink worker, verifier, WAL drainer into `main.go`

**Files:**
- Modify: `apps/edge-api/cmd/edge-api/main.go`
- Modify: `apps/control-plane/cmd/control-plane/main.go`

- [ ] **Step 1: Mount chat dispatch in edge-api**

Inside the protected router (`/v1` mount from Plan 02 Task 10), add:

```go
// apps/edge-api/cmd/edge-api/main.go
import (
	"hive/edge-api/internal/chat"
	"hive/control-plane/internal/audit"
)

chatHandler := chat.NewDispatch(chat.Deps{
	Pool: pool, LiteLLMURL: os.Getenv("LITELLM_URL"),
	DeploySHA: os.Getenv("DEPLOY_SHA"), Env: os.Getenv("HIVE_ENV"),
	Audit: auditLogger,
})
protectedRouter.Post("/v1/chat/completions", chatHandler.ServeHTTP)
```

- [ ] **Step 2: Start sink worker, verifier, WAL drainer in control-plane**

```go
// apps/control-plane/cmd/control-plane/main.go
import (
	"hive/control-plane/internal/auditworker"
	"hive/control-plane/internal/auditworker/sinks"
	"hive/control-plane/internal/auditverifier"
	"hive/control-plane/internal/waldrainer"
)

// Build sinks from env. Each NewX returns nil-safe when env is empty
// (caller pattern in the adapter file).
var configuredSinks []auditworker.Sink
if u := os.Getenv("AUDIT_SINK_ELK_URL"); u != "" {
	configuredSinks = append(configuredSinks, sinks.NewELK(sinks.ELKConfig{URL: u, APIKey: os.Getenv("AUDIT_SINK_ELK_API_KEY")}))
}
if u := os.Getenv("AUDIT_SINK_LOKI_URL"); u != "" {
	configuredSinks = append(configuredSinks, sinks.NewLoki(sinks.LokiConfig{URL: u}))
}
if k := os.Getenv("AUDIT_SINK_DATADOG_API_KEY"); k != "" {
	configuredSinks = append(configuredSinks, sinks.NewDatadog(sinks.DatadogConfig{APIKey: k, Site: os.Getenv("AUDIT_SINK_DATADOG_SITE")}))
}
if u := os.Getenv("AUDIT_SINK_SPLUNK_HEC_URL"); u != "" {
	configuredSinks = append(configuredSinks, sinks.NewSplunk(sinks.SplunkConfig{URL: u, Token: os.Getenv("AUDIT_SINK_SPLUNK_HEC_TOKEN")}))
}
if d := os.Getenv("SENTRY_DSN"); d != "" {
	configuredSinks = append(configuredSinks, sinks.NewSentry(sinks.SentryConfig{DSN: d}))
}
if h := os.Getenv("LANGFUSE_HOST"); h != "" {
	configuredSinks = append(configuredSinks, sinks.NewLangfuse(sinks.LangfuseConfig{
		Host: h, PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"), SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
	}))
}

worker := auditworker.New(auditworker.Config{Pool: pool, Sinks: configuredSinks})
go worker.Run(ctx)

verifier := auditverifier.New(pool)
go func() {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done(): return
		case <-t.C:
			if mm, err := verifier.VerifyPartition(ctx, time.Now()); err == nil && mm > 0 {
				_ = auditLogger.Log(ctx, audit.Event{
					Action: "AUDIT_CHAIN_VERIFY_FAIL", Severity: audit.SeverityCritical,
					Before: map[string]int{"mismatches": mm},
				})
			}
		}
	}
}()

go waldrainer.Run(ctx, auditWAL, 30*time.Second)
```

- [ ] **Step 3: Build both binaries**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go build ./apps/edge-api/... ./apps/control-plane/..."
```

Expected: builds cleanly.

- [ ] **Step 4: Boot stack and smoke-test end-to-end chat**

```
cd deploy/docker && docker compose --env-file ../../.env --profile local up -d --build
# Sign in via OWUI at http://localhost:3003, send a chat message.
# Confirm in psql:
psql "$SUPABASE_DB_URL" -c "SELECT count(*) FROM public.llm_traces WHERE ts > now() - interval '5 min';"
psql "$SUPABASE_DB_URL" -c "SELECT action FROM public.audit_log WHERE ts > now() - interval '5 min' ORDER BY ts;"
```

Expected: `llm_traces` count ≥ 1, audit actions include `AUTH_SIGNUP_SUCCESS`, `TENANT_USER_ADD`, `OWUI_GROUP_ADD_SUCCESS`, `CHAT_REQUEST`.

- [ ] **Step 5: Commit**

```
git add apps/edge-api/cmd/edge-api/main.go apps/control-plane/cmd/control-plane/main.go
git commit -m "feat(phase-19): wire chat dispatch, sink worker, chain verifier, WAL drainer"
```

---

## Plan 03 Self-Review Checklist

- [ ] Spec §3 (architecture) — OWUI service deployed, JWT-forward pipeline filter loaded, edge-api dispatches via JWT-validated path, control-plane drains audit outbox to optional sinks.
- [ ] Spec §5 — sink list complete (ELK, Loki, Datadog, Splunk HEC, Sentry, Langfuse). Each off by default, env-gated, non-blocking.
- [ ] Spec §6 — JWT forwarding from OWUI to edge-api implemented via pipeline filter; fallback documented if filter cannot reach the outgoing request shape.
- [ ] Spec §8 — chat happy-path written: JWT → tenant guard → RBAC → dispatch → SSE → `llm_traces` + `CHAT_REQUEST`.
- [ ] Spec §10 — error code surface present (`UNAUTHENTICATED`, `JWT_EXPIRED`, `NO_TENANT`, `FORBIDDEN`, `CROSS_TENANT`, `INVALID_TENANT_SETTING`, `INVALID_REQUEST`, `SERVICE_UNAVAILABLE`, `INTERNAL`). Provider-blind sanitiser regression-tested.
- [ ] Audit-chain verifier ships with a tamper test (gated by privilege) and a happy-path test that must pass.
- [ ] All new audit actions emitted by chat dispatch (`CHAT_REQUEST`) are in the Plan 01 `securityActions` review — actually `CHAT_REQUEST` is LLM-tier, NOT security-tier; verify it is NOT in `securityActions` set.
- [ ] Compose `--profile local` boot cycle ends in healthy state for OWUI + caddy-owui.

## Hand-off to Plan 04

Plan 04 (`2026-05-16-phase-19-04-tests-and-ci.md`) consumes the running stack from Plan 03 and adds:

* Playwright user-flow E2E suite (7 specs).
* Open WebUI direct E2E suite + nightly workflow.
* Audit-evidence Go tests against the CI run.
* `docs/compliance/SOC2-LOG-COVERAGE.md` generator.
* CI workflows (`phase-19.yml` + `phase-19-owui-nightly.yml`).
* README + dev onboarding updates.

Plan 03 is independently testable: every task ends with a CI-runnable Go test or a documented manual smoke. No Plan 04 code is required to merge Plan 03.
