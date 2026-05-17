# Phase 19 / Plan 02 — Identity Bridge + Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire Supabase Auth through the stack: a control-plane signup webhook provisions tenant membership and the Open WebUI group on every new user, control-plane exposes tenant CRUD + switch endpoints, and `edge-api` gains a JWT validator alongside the existing API-key path. Phase 18 RBAC helpers are reused; the permission set is extended with the five Phase 19 permissions.

**Architecture:** Plan 01 built the data layer. Plan 02 builds the auth layer on top. control-plane owns provisioning and tenant CRUD; edge-api owns request-time identity. A selector middleware in edge-api routes `Authorization: Bearer hk_*` to the existing API-key validator and everything else to the new Supabase-JWT validator. Both paths produce the same `auth.User` context, so all downstream RBAC and audit code stays auth-mode-agnostic. Cross-tenant guards and the lint rule that enforces them are wired into the request lifecycle.

**Tech stack:** Go 1.24, `github.com/lestrrat-go/jwx/v2/jwk`, `github.com/lestrrat-go/jwx/v2/jwt` (JWKS cache + JWT parsing), `github.com/go-chi/chi/v5` (router — existing), `pgx` (DB), `testcontainers-go` (integration tests), Supabase Auth REST API.

---

## File Structure (Plan 02)

**New files (created):**
- `apps/control-plane/internal/owui/client.go` — typed Go client for Open WebUI admin API.
- `apps/control-plane/internal/owui/client_test.go` — unit tests against a fake OWUI server.
- `apps/control-plane/internal/signup/resolver.go` — tenant resolution strategies (invite token / email domain / new-tenant).
- `apps/control-plane/internal/signup/resolver_test.go` — table-driven resolver tests.
- `apps/control-plane/internal/signup/webhook.go` — `POST /internal/auth/user-created` handler.
- `apps/control-plane/internal/signup/webhook_test.go` — handler integration test.
- `apps/control-plane/internal/tenants/http.go` — tenant CRUD + switch endpoints.
- `apps/control-plane/internal/tenants/http_test.go` — handler tests.
- `apps/edge-api/internal/auth/selector.go` — JWT vs API-key router middleware.
- `apps/edge-api/internal/auth/jwt_supabase.go` — Supabase JWT validator (JWKS-backed).
- `apps/edge-api/internal/auth/jwt_supabase_test.go` — validator integration test using a self-signed RS256 keypair.
- `apps/edge-api/internal/auth/user_context.go` — ctx getters for User/TenantID/Role.
- `apps/edge-api/internal/auth/user_context_test.go` — ctx round-trip test.
- `apps/edge-api/internal/authz/permissions_phase19.go` — Phase 19 permission additions.
- `apps/edge-api/internal/authz/role_policy_phase19.go` — role-permission map extension.
- `apps/edge-api/internal/authz/cross_tenant.go` — `RequireOwnTenant` helper.
- `apps/edge-api/internal/authz/cross_tenant_test.go` — guard tests.
- `apps/edge-api/internal/authz/policy_test_phase19.go` — RBAC table tests for new permissions.
- `tools/lint-no-direct-tenant-id.mjs` — CI lint script.

**Existing files (modified):**
- `apps/control-plane/cmd/control-plane/main.go` — wire signup webhook + tenants router + audit Logger.
- `apps/edge-api/cmd/edge-api/main.go` — wire selector + JWT validator alongside existing API-key path.
- `apps/edge-api/internal/authz/permissions.go` — re-export Phase 18 set plus new constants (import only; constants live in `permissions_phase19.go`).
- `package.json` — add `lint:tenant-id` script.

---

## Task 1: OWUI admin client

**Files:**
- Create: `apps/control-plane/internal/owui/client.go`
- Test: `apps/control-plane/internal/owui/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/owui/client_test.go
package owui_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/owui"
)

func TestClient_AddUserToGroup_PostsExpectedShape(t *testing.T) {
	var captured struct {
		Path        string
		Method      string
		Auth        string
		Body        map[string]string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		captured.Method = r.Method
		captured.Auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&captured.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c := owui.New(owui.Config{
		BaseURL:    srv.URL,
		AdminToken: "owui-admin-token",
	})

	err := c.AddUserToGroup(context.Background(), "grp-123", "user@office.example")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, captured.Method)
	require.Equal(t, "/api/v1/groups/grp-123/add-user", captured.Path)
	require.Equal(t, "Bearer owui-admin-token", captured.Auth)
	require.Equal(t, "user@office.example", captured.Body["user_email"])
}

func TestClient_AddUserToGroup_4xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"group not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c := owui.New(owui.Config{BaseURL: srv.URL, AdminToken: "t"})
	err := c.AddUserToGroup(context.Background(), "grp-404", "user@office.example")
	require.Error(t, err)
}

func TestClient_CreateGroup_Idempotent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"grp-new","name":"tenant_a"}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"already exists"}`))
	}))
	defer srv.Close()

	c := owui.New(owui.Config{BaseURL: srv.URL, AdminToken: "t"})
	id, err := c.EnsureGroup(context.Background(), "tenant_a")
	require.NoError(t, err)
	require.Equal(t, "grp-new", id)
	id2, err := c.EnsureGroup(context.Background(), "tenant_a")
	require.NoError(t, err)
	require.NotEmpty(t, id2, "EnsureGroup must look up the existing group on 409")
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/owui/... -count=1 -short -buildvcs=false"
```

Expected: compile error — `package owui` not found.

- [ ] **Step 3: Write `client.go`**

```go
// apps/control-plane/internal/owui/client.go
package owui

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

type Config struct {
	BaseURL    string
	AdminToken string
	HTTPClient *http.Client
}

type Client struct {
	base   string
	token  string
	client *http.Client
}

func New(cfg Config) *Client {
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{base: cfg.BaseURL, token: cfg.AdminToken, client: c}
}

// AddUserToGroup adds a user (looked up by email) to the named OWUI group.
func (c *Client) AddUserToGroup(ctx context.Context, groupID, email string) error {
	body := map[string]string{"user_email": email}
	return c.post(ctx, fmt.Sprintf("/api/v1/groups/%s/add-user", groupID), body, nil)
}

// EnsureGroup creates a group by name. If the group already exists (409),
// it queries by name and returns the existing id. Returns the group id.
func (c *Client) EnsureGroup(ctx context.Context, name string) (string, error) {
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	err := c.post(ctx, "/api/v1/groups", map[string]string{"name": name}, &created)
	if err == nil {
		return created.ID, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		return c.findGroupByName(ctx, name)
	}
	return "", err
}

func (c *Client) findGroupByName(ctx context.Context, name string) (string, error) {
	var groups []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.get(ctx, "/api/v1/groups", &groups); err != nil {
		return "", err
	}
	for _, g := range groups {
		if g.Name == name {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("owui: group %q not found after 409", name)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("owui: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Path: req.URL.Path, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("owui: decode %s: %w", req.URL.Path, err)
		}
	}
	return nil
}

type APIError struct {
	Status int
	Path   string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("owui: %s returned %d: %s", e.Path, e.Status, e.Body)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/owui/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS` on all three tests.

- [ ] **Step 5: Commit**

```
git add apps/control-plane/internal/owui/
git commit -m "feat(phase-19): add Open WebUI admin Go client with EnsureGroup idempotence"
```

---

## Task 2: Tenant resolution strategies

**Files:**
- Create: `apps/control-plane/internal/signup/resolver.go`
- Test: `apps/control-plane/internal/signup/resolver_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/signup/resolver_test.go
package signup_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/signup"
)

func TestResolver_InviteTokenWins(t *testing.T) {
	want := uuid.New()
	r := signup.NewResolver(signup.ResolverDeps{
		InviteLookup: func(ctx context.Context, tok string) (uuid.UUID, error) {
			if tok != "tok-abc" {
				return uuid.Nil, signup.ErrNoMatch
			}
			return want, nil
		},
	})
	got, err := r.Resolve(context.Background(), signup.Input{
		InviteToken: "tok-abc",
		Email:       "x@y.example",
	})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResolver_EmailDomainFallback(t *testing.T) {
	want := uuid.New()
	r := signup.NewResolver(signup.ResolverDeps{
		InviteLookup: func(ctx context.Context, tok string) (uuid.UUID, error) {
			return uuid.Nil, signup.ErrNoMatch
		},
		DomainLookup: func(ctx context.Context, domain string) (uuid.UUID, error) {
			if domain != "office.example" {
				return uuid.Nil, signup.ErrNoMatch
			}
			return want, nil
		},
	})
	got, err := r.Resolve(context.Background(), signup.Input{Email: "user@office.example"})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResolver_NoneMatch_ReturnsErrNoMatch(t *testing.T) {
	r := signup.NewResolver(signup.ResolverDeps{
		InviteLookup: func(ctx context.Context, tok string) (uuid.UUID, error) { return uuid.Nil, signup.ErrNoMatch },
		DomainLookup: func(ctx context.Context, domain string) (uuid.UUID, error) { return uuid.Nil, signup.ErrNoMatch },
	})
	_, err := r.Resolve(context.Background(), signup.Input{Email: "stranger@unknown.example"})
	require.True(t, errors.Is(err, signup.ErrNoMatch))
}
```

- [ ] **Step 2: Run test, verify failure**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/signup/... -count=1 -short -buildvcs=false"
```

Expected: compile error — package `signup` not found.

- [ ] **Step 3: Write `resolver.go`**

```go
// apps/control-plane/internal/signup/resolver.go
package signup

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrNoMatch = errors.New("signup: no tenant match")

type Input struct {
	Email       string
	InviteToken string
}

type LookupFunc func(ctx context.Context, key string) (uuid.UUID, error)

type ResolverDeps struct {
	InviteLookup LookupFunc
	DomainLookup LookupFunc
}

type Resolver struct {
	deps ResolverDeps
}

func NewResolver(deps ResolverDeps) *Resolver { return &Resolver{deps: deps} }

// Resolve picks the tenant id in priority order: invite token first
// (explicit user choice), then email-domain mapping (EnterpriseEdge
// default), then ErrNoMatch (signin is rejected with NO_TENANT until
// an admin invites the user).
func (r *Resolver) Resolve(ctx context.Context, in Input) (uuid.UUID, error) {
	if in.InviteToken != "" && r.deps.InviteLookup != nil {
		id, err := r.deps.InviteLookup(ctx, in.InviteToken)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, ErrNoMatch) {
			return uuid.Nil, err
		}
	}
	if r.deps.DomainLookup != nil {
		if at := strings.IndexByte(in.Email, '@'); at >= 0 && at < len(in.Email)-1 {
			domain := strings.ToLower(in.Email[at+1:])
			id, err := r.deps.DomainLookup(ctx, domain)
			if err == nil {
				return id, nil
			}
			if !errors.Is(err, ErrNoMatch) {
				return uuid.Nil, err
			}
		}
	}
	return uuid.Nil, ErrNoMatch
}
```

- [ ] **Step 4: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/signup/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```
git add apps/control-plane/internal/signup/resolver.go \
        apps/control-plane/internal/signup/resolver_test.go
git commit -m "feat(phase-19): add signup tenant resolver (invite token then email domain)"
```

---

## Task 3: Signup webhook handler

**Files:**
- Create: `apps/control-plane/internal/signup/webhook.go`
- Test: `apps/control-plane/internal/signup/webhook_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/signup/webhook_test.go
package signup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/audit"
	"hive/control-plane/internal/signup"
)

func TestWebhook_HappyPath_InsertsMembershipAndAudits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantID := mustInsertTenant(t, ctx, pool, "office", "ENTERPRISE_EDGE")
	userID := mustInsertAuthUser(t, ctx, pool, "ada@office.example")

	addedUser := ""
	addedGroup := ""
	groupAdder := func(ctx context.Context, group, email string) error {
		addedUser = email
		addedGroup = group
		return nil
	}
	groupEnsurer := func(ctx context.Context, name string) (string, error) {
		return "grp-" + name, nil
	}

	resolver := signup.NewResolver(signup.ResolverDeps{
		DomainLookup: func(ctx context.Context, domain string) (uuid.UUID, error) {
			if domain == "office.example" {
				return tenantID, nil
			}
			return uuid.Nil, signup.ErrNoMatch
		},
	})

	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  &noopWAL{},
	})

	h := signup.NewWebhook(signup.WebhookDeps{
		Pool:         pool,
		Resolver:     resolver,
		EnsureGroup:  groupEnsurer,
		AddUser:      groupAdder,
		Audit:        logger,
		SharedSecret: "shh",
	})

	body, _ := json.Marshal(map[string]any{
		"user_id": userID,
		"email":   "ada@office.example",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/auth/user-created", bytes.NewReader(body))
	req.Header.Set("X-Hive-Signup-Secret", "shh")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "ada@office.example", addedUser)
	require.Equal(t, "grp-tenant_"+tenantID.String(), addedGroup)

	var role string
	err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID).Scan(&role)
	require.NoError(t, err)
	require.Equal(t, "MEMBER", role)

	var actions []string
	rows, err := pool.Query(ctx,
		`SELECT action FROM public.audit_log WHERE tenant_id=$1 ORDER BY seq`, tenantID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		actions = append(actions, a)
	}
	require.Contains(t, actions, "AUTH_SIGNUP_SUCCESS")
	require.Contains(t, actions, "TENANT_USER_ADD")
	require.Contains(t, actions, "OWUI_GROUP_ADD_SUCCESS")
}

func TestWebhook_BadSecret_401(t *testing.T) {
	h := signup.NewWebhook(signup.WebhookDeps{SharedSecret: "expected"})
	req := httptest.NewRequest(http.MethodPost, "/internal/auth/user-created", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Hive-Signup-Secret", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

type noopWAL struct{}

func (noopWAL) Write(ctx context.Context, e audit.Event) error { return nil }

func mustInsertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, deployment string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO public.tenants(slug, name, deployment) VALUES ($1, $1, $2) RETURNING id`,
		slug, deployment).Scan(&id)
	require.NoError(t, err)
	return id
}

func mustInsertAuthUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		email).Scan(&id)
	require.NoError(t, err)
	return id
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

- [ ] **Step 2: Write `webhook.go`**

```go
// apps/control-plane/internal/signup/webhook.go
package signup

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hive/control-plane/internal/audit"
)

type EnsureGroupFunc func(ctx context.Context, name string) (string, error)
type AddUserFunc func(ctx context.Context, groupID, email string) error

type WebhookDeps struct {
	Pool         *pgxpool.Pool
	Resolver     *Resolver
	EnsureGroup  EnsureGroupFunc
	AddUser      AddUserFunc
	Audit        *audit.Logger
	SharedSecret string
}

type Webhook struct{ deps WebhookDeps }

func NewWebhook(deps WebhookDeps) *Webhook { return &Webhook{deps: deps} }

type webhookBody struct {
	UserID      uuid.UUID `json:"user_id"`
	Email       string    `json:"email"`
	InviteToken string    `json:"invite_token,omitempty"`
}

func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare(
		[]byte(r.Header.Get("X-Hive-Signup-Secret")),
		[]byte(h.deps.SharedSecret),
	) != 1 {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if h.deps.Pool == nil || h.deps.Resolver == nil || h.deps.Audit == nil {
		http.Error(w, `{"error":"misconfigured"}`, http.StatusInternalServerError)
		return
	}

	var body webhookBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	if body.UserID == uuid.Nil || body.Email == "" {
		http.Error(w, `{"error":"missing user_id or email"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tenantID, err := h.deps.Resolver.Resolve(ctx, Input{Email: body.Email, InviteToken: body.InviteToken})
	if err != nil {
		// Audit but reply 204 — Supabase Database Webhooks retry on non-2xx.
		_ = h.deps.Audit.Log(ctx, audit.Event{
			Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
			Action:   "AUTH_SIGNIN_FAILURE_NO_TENANT",
			Severity: audit.SeverityWarning,
			Before:   map[string]string{"email": body.Email},
		})
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.provision(ctx, tenantID, body); err != nil {
		_ = h.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
			Action:   "AUTH_SIGNUP_SUCCESS",
			Severity: audit.SeverityError,
			Before:   map[string]string{"email": body.Email, "stage": "provision_failed", "error": err.Error()},
		})
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Webhook) provision(ctx context.Context, tenantID uuid.UUID, body webhookBody) error {
	_, err := h.deps.Pool.Exec(ctx, `
		INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
		VALUES ($1, $2, 'MEMBER', 'ACTIVE')
		ON CONFLICT DO NOTHING
	`, tenantID, body.UserID)
	if err != nil {
		return fmt.Errorf("insert tenant_users: %w", err)
	}

	_ = h.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
		Action:   "AUTH_SIGNUP_SUCCESS",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"email": body.Email},
	})
	_ = h.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
		Action:   "TENANT_USER_ADD",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"role": "MEMBER"},
	})

	if h.deps.EnsureGroup == nil || h.deps.AddUser == nil {
		return nil
	}
	groupName := "tenant_" + tenantID.String()
	groupID, err := h.deps.EnsureGroup(ctx, groupName)
	if err != nil {
		_ = h.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Action:   "OWUI_GROUP_CREATE_FAILURE",
			Severity: audit.SeverityError,
			Before:   map[string]string{"name": groupName, "error": err.Error()},
		})
		return errors.New("ensure group: " + err.Error())
	}
	if err := h.deps.AddUser(ctx, groupID, body.Email); err != nil {
		_ = h.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
			Action:   "OWUI_GROUP_ADD_FAILURE",
			Severity: audit.SeverityError,
			Before:   map[string]string{"group_id": groupID, "email": body.Email, "error": err.Error()},
		})
		return errors.New("add user: " + err.Error())
	}
	_ = h.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
		Action:   "OWUI_GROUP_ADD_SUCCESS",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"group_id": groupID, "email": body.Email},
	})
	return nil
}
```

- [ ] **Step 3: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/signup/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 4: Commit**

```
git add apps/control-plane/internal/signup/webhook.go \
        apps/control-plane/internal/signup/webhook_test.go
git commit -m "feat(phase-19): add signup webhook with tenant membership + OWUI group provisioning"
```

---

## Task 4: Tenant CRUD + switch handlers

**Files:**
- Create: `apps/control-plane/internal/tenants/http.go`
- Test: `apps/control-plane/internal/tenants/http_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/tenants/http_test.go
package tenants_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/audit"
	"hive/control-plane/internal/tenants"
)

func TestSwitch_Allowed_UpdatesMetadataAndAudits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newTenantsPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantA := mustInsertTenant(t, ctx, pool, "team-a", "HIVE_CLOUD")
	tenantB := mustInsertTenant(t, ctx, pool, "team-b", "HIVE_CLOUD")
	userID := mustInsertAuthUser(t, ctx, pool, "u@y.example")
	mustInsertMembership(t, ctx, pool, tenantA, userID, "MEMBER")
	mustInsertMembership(t, ctx, pool, tenantB, userID, "MEMBER")

	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  &noopWAL{},
	})
	h := tenants.NewHandler(tenants.Deps{Pool: pool, Audit: logger})

	body, _ := json.Marshal(map[string]string{"tenant_id": tenantB.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/switch", bytes.NewReader(body))
	req = req.WithContext(tenants.WithUser(req.Context(), tenants.User{ID: userID, TenantID: tenantA}))
	rec := httptest.NewRecorder()
	h.Switch(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var selected string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT raw_user_meta_data->>'selected_tenant_id' FROM auth.users WHERE id=$1`, userID).Scan(&selected))
	require.Equal(t, tenantB.String(), selected)

	var actions []string
	rows, err := pool.Query(ctx,
		`SELECT action FROM public.audit_log WHERE actor_id=$1 ORDER BY seq`, userID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		actions = append(actions, a)
	}
	require.Contains(t, actions, "TENANT_SWITCH")
}

func TestSwitch_NonMember_403CrossTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newTenantsPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantA := mustInsertTenant(t, ctx, pool, "team-a2", "HIVE_CLOUD")
	tenantB := mustInsertTenant(t, ctx, pool, "team-b2", "HIVE_CLOUD")
	userID := mustInsertAuthUser(t, ctx, pool, "y@z.example")
	mustInsertMembership(t, ctx, pool, tenantA, userID, "MEMBER")

	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  &noopWAL{},
	})
	h := tenants.NewHandler(tenants.Deps{Pool: pool, Audit: logger})

	body, _ := json.Marshal(map[string]string{"tenant_id": tenantB.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/switch", bytes.NewReader(body))
	req = req.WithContext(tenants.WithUser(req.Context(), tenants.User{ID: userID, TenantID: tenantA}))
	rec := httptest.NewRecorder()
	h.Switch(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var errBody map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &errBody)
	require.Equal(t, "CROSS_TENANT", errBody["error"].(map[string]any)["code"])

	var actions []string
	rows, _ := pool.Query(ctx,
		`SELECT action FROM public.audit_log WHERE actor_id=$1 ORDER BY seq`, userID)
	defer rows.Close()
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		actions = append(actions, a)
	}
	require.Contains(t, actions, "CROSS_TENANT_ATTEMPT")
}

type noopWAL struct{}

func (noopWAL) Write(ctx context.Context, e audit.Event) error { return nil }

func newTenantsPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}

func mustInsertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, deployment string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO public.tenants(slug, name, deployment) VALUES ($1, $1, $2) RETURNING id`,
		slug, deployment).Scan(&id)
	require.NoError(t, err)
	return id
}

func mustInsertAuthUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		email).Scan(&id)
	require.NoError(t, err)
	return id
}

func mustInsertMembership(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, role string) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, $3, 'ACTIVE')`,
		tenantID, userID, role)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Write `http.go`**

```go
// apps/control-plane/internal/tenants/http.go
package tenants

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hive/control-plane/internal/audit"
)

type User struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Role     string
}

type ctxKey struct{}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

type Deps struct {
	Pool  *pgxpool.Pool
	Audit *audit.Logger
}

type Handler struct{ deps Deps }

func NewHandler(deps Deps) *Handler { return &Handler{deps: deps} }

type switchBody struct {
	TenantID uuid.UUID `json:"tenant_id"`
}

func (h *Handler) Switch(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing user context")
		return
	}
	var body switchBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TenantID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "tenant_id required")
		return
	}

	var membershipCount int
	if err := h.deps.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM public.tenant_users
		  WHERE user_id=$1 AND tenant_id=$2 AND status='ACTIVE'`,
		user.ID, body.TenantID).Scan(&membershipCount); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "membership lookup failed")
		return
	}
	if membershipCount == 0 {
		_ = h.deps.Audit.Log(r.Context(), audit.Event{
			TenantID: user.TenantID,
			Actor:    audit.Actor{ID: user.ID, Type: audit.ActorUser},
			Action:   "CROSS_TENANT_ATTEMPT",
			Severity: audit.SeverityCritical,
			Before:   map[string]string{"requested_tenant_id": body.TenantID.String()},
		})
		writeError(w, http.StatusForbidden, "CROSS_TENANT", "not a member of the requested tenant")
		return
	}

	if _, err := h.deps.Pool.Exec(r.Context(),
		`UPDATE auth.users
		    SET raw_user_meta_data = COALESCE(raw_user_meta_data, '{}'::jsonb)
		      || jsonb_build_object('selected_tenant_id', $1::text)
		  WHERE id = $2`,
		body.TenantID, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "metadata update failed")
		return
	}

	_ = h.deps.Audit.Log(r.Context(), audit.Event{
		TenantID: body.TenantID,
		Actor:    audit.Actor{ID: user.ID, Type: audit.ActorUser},
		Action:   "TENANT_SWITCH",
		Severity: audit.SeverityInfo,
		Before:   map[string]string{"from": user.TenantID.String()},
		After:    map[string]string{"to": body.TenantID.String()},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg, "type": typeFor(status)},
	})
}

func typeFor(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case status == http.StatusForbidden:
		return "FORBIDDEN"
	case status == http.StatusBadRequest:
		return "INVALID_REQUEST"
	case status >= 500:
		return "INTERNAL"
	default:
		return "INTERNAL"
	}
}
```

- [ ] **Step 3: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/tenants/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 4: Commit**

```
git add apps/control-plane/internal/tenants/
git commit -m "feat(phase-19): add tenant switch endpoint with cross-tenant audit"
```

---

## Task 5: edge-api JWT validator (JWKS-backed)

**Files:**
- Create: `apps/edge-api/internal/auth/jwt_supabase.go`
- Test: `apps/edge-api/internal/auth/jwt_supabase_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/edge-api/internal/auth/jwt_supabase_test.go
package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/require"

	"hive/edge-api/internal/auth"
)

func TestJWTValidator_ValidToken_PopulatesContext(t *testing.T) {
	priv, set, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	tid := uuid.New()
	uid := uuid.New()
	token := signToken(t, priv, set, "https://test.supabase.co/auth/v1", map[string]any{
		"sub":       uid.String(),
		"email":     "ada@office.example",
		"aud":       "authenticated",
		"tenant_id": tid.String(),
		"role":      "ADMIN",
	})

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)

	claims, err := v.Parse(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, uid, claims.Sub)
	require.Equal(t, tid, claims.TenantID)
	require.Equal(t, "ADMIN", claims.Role)
}

func TestJWTValidator_ExpiredToken_ReturnsErrExpired(t *testing.T) {
	priv, set, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	token := signTokenWithExp(t, priv, set, "https://test.supabase.co/auth/v1", time.Now().Add(-time.Hour))

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)
	_, err = v.Parse(context.Background(), token)
	require.ErrorIs(t, err, auth.ErrJWTExpired)
}

func TestJWTValidator_BadIssuer_Rejected(t *testing.T) {
	priv, set, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	token := signToken(t, priv, set, "https://attacker.example/auth/v1", nil)

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)
	_, err = v.Parse(context.Background(), token)
	require.Error(t, err)
}

// Test helpers ------------------------------------------------------------

func newTestKey(t *testing.T) (*rsa.PrivateKey, jwk.Set, []byte) {
	t.Helper()
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	priv, err := jwk.FromRaw(raw)
	require.NoError(t, err)
	require.NoError(t, priv.Set(jwk.KeyIDKey, "kid-test"))
	require.NoError(t, priv.Set(jwk.AlgorithmKey, jwa.RS256))
	pub, err := priv.PublicKey()
	require.NoError(t, err)
	set := jwk.NewSet()
	_ = set.AddKey(pub)
	raw2, err := json.Marshal(set)
	require.NoError(t, err)
	return raw, set, raw2
}

func jwksServer(t *testing.T, jwksJSON []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksJSON)
	})
	return httptest.NewServer(mux)
}

func signToken(t *testing.T, raw *rsa.PrivateKey, set jwk.Set, iss string, extra map[string]any) string {
	return signTokenWithExp(t, raw, set, iss, time.Now().Add(time.Hour), extra)
}

func signTokenWithExp(t *testing.T, raw *rsa.PrivateKey, set jwk.Set, iss string, exp time.Time, extra ...map[string]any) string {
	t.Helper()
	b := jwt.NewBuilder().
		Issuer(iss).
		Audience([]string{"authenticated"}).
		IssuedAt(time.Now()).
		Expiration(exp)
	for _, m := range extra {
		for k, v := range m {
			b = b.Claim(k, v)
		}
	}
	tok, err := b.Build()
	require.NoError(t, err)

	signKey, err := jwk.FromRaw(raw)
	require.NoError(t, err)
	require.NoError(t, signKey.Set(jwk.KeyIDKey, "kid-test"))

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, signKey))
	require.NoError(t, err)
	return fmt.Sprintf("%s", signed)
}
```

- [ ] **Step 2: Write `jwt_supabase.go`**

```go
// apps/edge-api/internal/auth/jwt_supabase.go
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

var ErrJWTExpired = errors.New("auth: jwt expired")

type SupabaseJWTConfig struct {
	Issuer  string
	JWTAudience string
	JWKSURL string
	JWKSTTL time.Duration
}

type Claims struct {
	Sub      uuid.UUID
	Email    string
	TenantID uuid.UUID
	Role     string
	Tenants  []TenantMembership
}

type TenantMembership struct {
	ID   uuid.UUID
	Role string
}

type SupabaseJWTValidator struct {
	cfg   SupabaseJWTConfig
	cache *jwk.Cache
}

func NewSupabaseJWTValidator(ctx context.Context, cfg SupabaseJWTConfig) (*SupabaseJWTValidator, error) {
	if cfg.Issuer == "" || cfg.JWKSURL == "" {
		return nil, errors.New("auth: SupabaseJWTConfig.Issuer and JWKSURL required")
	}
	if cfg.JWTAudience == "" {
		cfg.JWTAudience = "authenticated"
	}
	if cfg.JWKSTTL == 0 {
		cfg.JWKSTTL = 24 * time.Hour
	}
	cache := jwk.NewCache(ctx)
	if err := cache.Register(cfg.JWKSURL, jwk.WithRefreshInterval(cfg.JWKSTTL)); err != nil {
		return nil, fmt.Errorf("auth: jwks register: %w", err)
	}
	if _, err := cache.Refresh(ctx, cfg.JWKSURL); err != nil {
		return nil, fmt.Errorf("auth: jwks initial refresh: %w", err)
	}
	return &SupabaseJWTValidator{cfg: cfg, cache: cache}, nil
}

// Parse validates the token and returns the extracted claims.
func (v *SupabaseJWTValidator) Parse(ctx context.Context, raw string) (Claims, error) {
	set, err := v.cache.Get(ctx, v.cfg.JWKSURL)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: jwks fetch: %w", err)
	}
	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(set),
		jwt.WithIssuer(v.cfg.Issuer),
		jwt.WithAudience(v.cfg.JWTAudience),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired()) {
			return Claims{}, ErrJWTExpired
		}
		return Claims{}, err
	}

	out := Claims{}
	if sub := tok.Subject(); sub != "" {
		if id, err := uuid.Parse(sub); err == nil {
			out.Sub = id
		}
	}
	if v, ok := tok.Get("email"); ok {
		if s, _ := v.(string); s != "" {
			out.Email = s
		}
	}
	if v, ok := tok.Get("tenant_id"); ok {
		if s, _ := v.(string); s != "" {
			if id, err := uuid.Parse(s); err == nil {
				out.TenantID = id
			}
		}
	}
	if v, ok := tok.Get("role"); ok {
		out.Role, _ = v.(string)
	}
	if v, ok := tok.Get("tenants"); ok {
		if arr, _ := v.([]any); arr != nil {
			for _, e := range arr {
				m, _ := e.(map[string]any)
				idS, _ := m["id"].(string)
				roleS, _ := m["role"].(string)
				if id, err := uuid.Parse(idS); err == nil {
					out.Tenants = append(out.Tenants, TenantMembership{ID: id, Role: roleS})
				}
			}
		}
	}
	return out, nil
}
```

- [ ] **Step 3: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/auth/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 4: Commit**

```
git add apps/edge-api/internal/auth/jwt_supabase.go \
        apps/edge-api/internal/auth/jwt_supabase_test.go
git commit -m "feat(phase-19): add Supabase JWT validator with JWKS cache"
```

---

## Task 6: edge-api request context + selector middleware

**Files:**
- Create: `apps/edge-api/internal/auth/user_context.go`
- Create: `apps/edge-api/internal/auth/user_context_test.go`
- Create: `apps/edge-api/internal/auth/selector.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/edge-api/internal/auth/user_context_test.go
package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"hive/edge-api/internal/auth"
)

func TestContext_RoundTrip(t *testing.T) {
	uid := uuid.New()
	tid := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{
		ID: uid, TenantID: tid, Role: "ADMIN", Email: "u@x.example",
	})
	got, ok := auth.UserFrom(ctx)
	require.True(t, ok)
	require.Equal(t, uid, got.ID)
	require.Equal(t, tid, got.TenantID)
}

func TestSelector_BearerHK_ChoosesAPIKeyPath(t *testing.T) {
	var hits struct{ jwt, apiKey int }
	jwtH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.jwt++ })
	keyH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.apiKey++ })
	mux := auth.Selector(jwtH, keyH)

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	req.Header.Set("Authorization", "Bearer hk_test_key")
	mux.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, 0, hits.jwt)
	require.Equal(t, 1, hits.apiKey)
}

func TestSelector_BearerJWT_ChoosesJWTPath(t *testing.T) {
	var hits struct{ jwt, apiKey int }
	jwtH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.jwt++ })
	keyH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.apiKey++ })
	mux := auth.Selector(jwtH, keyH)

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOi...")
	mux.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, 1, hits.jwt)
	require.Equal(t, 0, hits.apiKey)
}
```

- [ ] **Step 2: Write `user_context.go`**

```go
// apps/edge-api/internal/auth/user_context.go
package auth

import (
	"context"

	"github.com/google/uuid"
)

type User struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Role     string
	Email    string
}

type ctxKey struct{}

func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func UserFrom(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*User)
	return u, ok
}

// TenantID returns the tenant id on the request context. Callers MUST go
// through this getter; reading tenant_id from any other source is blocked
// by lint (tools/lint-no-direct-tenant-id.mjs).
func TenantID(ctx context.Context) uuid.UUID {
	if u, ok := UserFrom(ctx); ok && u != nil {
		return u.TenantID
	}
	return uuid.Nil
}
```

- [ ] **Step 3: Write `selector.go`**

```go
// apps/edge-api/internal/auth/selector.go
package auth

import (
	"net/http"
	"strings"
)

// Selector routes a request to the API-key path when the Authorization
// header starts with "Bearer hk_" and to the JWT path otherwise. Unauthed
// requests fall through to the JWT path, which fails them with 401.
func Selector(jwtHandler, apiKeyHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer hk_") {
			apiKeyHandler.ServeHTTP(w, r)
			return
		}
		jwtHandler.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/auth/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```
git add apps/edge-api/internal/auth/user_context.go \
        apps/edge-api/internal/auth/user_context_test.go \
        apps/edge-api/internal/auth/selector.go
git commit -m "feat(phase-19): add edge-api user context + JWT/API-key selector"
```

---

## Task 7: edge-api permission set extension

**Files:**
- Create: `apps/edge-api/internal/authz/permissions_phase19.go`
- Create: `apps/edge-api/internal/authz/role_policy_phase19.go`
- Test: `apps/edge-api/internal/authz/policy_test_phase19.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/edge-api/internal/authz/policy_test_phase19.go
package authz_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"hive/edge-api/internal/authz"
)

func TestPolicy_Phase19_Permissions(t *testing.T) {
	cases := []struct {
		role  authz.Role
		perm  authz.Permission
		allow bool
	}{
		{authz.RoleOwner, authz.PermChatInvoke, true},
		{authz.RoleAdmin, authz.PermTenantSettingWrite, true},
		{authz.RoleMember, authz.PermTenantSettingWrite, false},
		{authz.RoleViewer, authz.PermChatInvoke, true},
		{authz.RoleViewer, authz.PermTenantSwitch, false},
		{authz.RoleAdmin, authz.PermAuditRead, true},
		{authz.RoleMember, authz.PermAuditRead, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.role)+"/"+string(tc.perm), func(t *testing.T) {
			require.Equal(t, tc.allow, authz.RoleHas(tc.role, tc.perm))
		})
	}
}
```

- [ ] **Step 2: Write `permissions_phase19.go`**

```go
// apps/edge-api/internal/authz/permissions_phase19.go
package authz

const (
	PermChatInvoke         Permission = "CHAT_INVOKE"
	PermTenantSettingRead  Permission = "TENANT_SETTING_READ"
	PermTenantSettingWrite Permission = "TENANT_SETTING_WRITE"
	PermTenantSwitch       Permission = "TENANT_SWITCH"
	PermAuditRead          Permission = "AUDIT_READ"
)
```

- [ ] **Step 3: Write `role_policy_phase19.go`**

```go
// apps/edge-api/internal/authz/role_policy_phase19.go
//
// Extends the Phase 18 role-permission map with the Phase 19 permissions.
// This file appends to the existing policy table at package init so the
// shipped Phase 18 helpers (Allow, AllGranted, etc.) keep working unchanged.

package authz

func init() {
	addPerms(RoleOwner,
		PermChatInvoke, PermTenantSettingRead, PermTenantSettingWrite,
		PermTenantSwitch, PermAuditRead)
	addPerms(RoleAdmin,
		PermChatInvoke, PermTenantSettingRead, PermTenantSettingWrite,
		PermTenantSwitch, PermAuditRead)
	addPerms(RoleMember,
		PermChatInvoke, PermTenantSwitch)
	addPerms(RoleViewer,
		PermChatInvoke)
}

// addPerms is exposed by the Phase 18 authz package (see policy.go). If the
// shipped Phase 18 code does not export an additive API, replace this with
// the equivalent direct assignment to `rolePermissions[role]`.
```

If the Phase 18 package does not expose `addPerms`, the implementing engineer adds a one-line helper in `policy.go`:

```go
// apps/edge-api/internal/authz/policy.go (existing — add this helper at bottom)
func addPerms(r Role, ps ...Permission) {
	rolePermissions[r] = append(rolePermissions[r], ps...)
}
```

- [ ] **Step 4: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```
git add apps/edge-api/internal/authz/permissions_phase19.go \
        apps/edge-api/internal/authz/role_policy_phase19.go \
        apps/edge-api/internal/authz/policy_test_phase19.go \
        apps/edge-api/internal/authz/policy.go
git commit -m "feat(phase-19): extend authz with CHAT_INVOKE / TENANT_SETTING_* / AUDIT_READ"
```

---

## Task 8: Cross-tenant guard

**Files:**
- Create: `apps/edge-api/internal/authz/cross_tenant.go`
- Test: `apps/edge-api/internal/authz/cross_tenant_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/edge-api/internal/authz/cross_tenant_test.go
package authz_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"hive/edge-api/internal/auth"
	"hive/edge-api/internal/authz"
)

func TestRequireOwnTenant_SameTenant_NilError(t *testing.T) {
	tid := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{TenantID: tid})
	logged := false
	require.NoError(t, authz.RequireOwnTenant(ctx, tid, func(action string) { logged = true }))
	require.False(t, logged)
}

func TestRequireOwnTenant_DifferentTenant_AuditsAndReturnsForbidden(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{TenantID: a})
	logged := ""
	err := authz.RequireOwnTenant(ctx, b, func(action string) { logged = action })
	require.ErrorIs(t, err, authz.ErrForbidden)
	require.Equal(t, "CROSS_TENANT_ATTEMPT", logged)
}
```

- [ ] **Step 2: Write `cross_tenant.go`**

```go
// apps/edge-api/internal/authz/cross_tenant.go
package authz

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"hive/edge-api/internal/auth"
)

var ErrForbidden = errors.New("authz: forbidden")

// AuditFunc is the minimal callback shape so this package stays free of an
// import on the audit package (avoids a cycle). The caller wires the real
// audit.Log emitter at the HTTP boundary.
type AuditFunc func(action string)

// RequireOwnTenant fails when the request's tenant does not match the
// caller's resolved tenant id. It calls auditFn with "CROSS_TENANT_ATTEMPT"
// on denial; the caller is responsible for emitting the actual audit row.
func RequireOwnTenant(ctx context.Context, requested uuid.UUID, auditFn AuditFunc) error {
	resolved := auth.TenantID(ctx)
	if resolved == requested && resolved != uuid.Nil {
		return nil
	}
	if auditFn != nil {
		auditFn("CROSS_TENANT_ATTEMPT")
	}
	return ErrForbidden
}
```

- [ ] **Step 3: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 4: Commit**

```
git add apps/edge-api/internal/authz/cross_tenant.go \
        apps/edge-api/internal/authz/cross_tenant_test.go
git commit -m "feat(phase-19): add RequireOwnTenant guard with audit-callback hook"
```

---

## Task 9: Wire signup webhook + tenants router into control-plane `main.go`

**Files:**
- Modify: `apps/control-plane/cmd/control-plane/main.go`

- [ ] **Step 1: Add the new routes and wiring**

Inside the existing router setup (location varies by current code; locate the chi router instance — typically `r := chi.NewRouter()`), add:

```go
// apps/control-plane/cmd/control-plane/main.go (additions inside main / setupRouter)

owuiClient := owui.New(owui.Config{
    BaseURL:    os.Getenv("OWUI_BASE_URL"),
    AdminToken: os.Getenv("OWUI_ADMIN_TOKEN"),
})

settingsResolver := settings.NewResolver(pool, 30*time.Second)
go settingsResolver.StartListener(ctx)

auditSync := audit.NewSyncWriter(pool, audit.WriterConfig{
    DeploySHA: os.Getenv("DEPLOY_SHA"),
    Env:       os.Getenv("HIVE_ENV"),
})
auditWAL, err := audit.NewWALWriter(audit.WALConfig{
    Dir:  os.Getenv("AUDIT_WAL_DIR"),
    Sync: auditSync,
})
if err != nil { return fmt.Errorf("audit WAL: %w", err) }
auditLogger := audit.NewLogger(audit.LoggerDeps{Sync: auditSync, WAL: auditWAL})

signupResolver := signup.NewResolver(signup.ResolverDeps{
    InviteLookup: signupLookupInvite(pool),
    DomainLookup: signupLookupDomain(pool),
})

signupWebhook := signup.NewWebhook(signup.WebhookDeps{
    Pool:         pool,
    Resolver:     signupResolver,
    EnsureGroup:  owuiClient.EnsureGroup,
    AddUser:      owuiClient.AddUserToGroup,
    Audit:        auditLogger,
    SharedSecret: os.Getenv("HIVE_SIGNUP_WEBHOOK_SECRET"),
})

tenantsHandler := tenants.NewHandler(tenants.Deps{Pool: pool, Audit: auditLogger})

r.Post("/internal/auth/user-created", signupWebhook.ServeHTTP)
r.Post("/v1/tenants/switch",         tenantsHandler.Switch)

_ = settingsResolver // exported via DI elsewhere when later phases need it
```

Helpers `signupLookupInvite` and `signupLookupDomain` are added in the same file:

```go
func signupLookupInvite(pool *pgxpool.Pool) signup.LookupFunc {
    return func(ctx context.Context, token string) (uuid.UUID, error) {
        var id uuid.UUID
        err := pool.QueryRow(ctx,
            `SELECT tenant_id FROM public.tenant_invites
              WHERE token=$1 AND consumed_at IS NULL AND expires_at > now()`,
            token).Scan(&id)
        if err != nil { return uuid.Nil, signup.ErrNoMatch }
        return id, nil
    }
}

func signupLookupDomain(pool *pgxpool.Pool) signup.LookupFunc {
    return func(ctx context.Context, domain string) (uuid.UUID, error) {
        var id uuid.UUID
        err := pool.QueryRow(ctx,
            `SELECT tenant_id FROM public.tenant_email_domains WHERE domain=$1`,
            domain).Scan(&id)
        if err != nil { return uuid.Nil, signup.ErrNoMatch }
        return id, nil
    }
}
```

Migrations for `tenant_invites` and `tenant_email_domains` are intentionally deferred to Plan 03 alongside the Hive Cloud invite flow — Plan 02 ships the resolver shape only, gated to error path. Add stub migrations in Plan 03 Task 1 or earlier.

- [ ] **Step 2: Build the binary**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go build ./apps/control-plane/..."
```

Expected: builds cleanly.

- [ ] **Step 3: Run the full control-plane test suite**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS` across all packages.

- [ ] **Step 4: Commit**

```
git add apps/control-plane/cmd/control-plane/main.go
git commit -m "feat(phase-19): wire signup webhook and tenant switch routes into control-plane"
```

---

## Task 10: Wire selector + JWT validator into edge-api `main.go`

**Files:**
- Modify: `apps/edge-api/cmd/edge-api/main.go`

- [ ] **Step 1: Insert middleware setup**

In the router construction (e.g. inside `setupRouter`), add:

```go
// apps/edge-api/cmd/edge-api/main.go (additions inside main / setupRouter)

jwtValidator, err := auth.NewSupabaseJWTValidator(ctx, auth.SupabaseJWTConfig{
    Issuer:      os.Getenv("SUPABASE_URL") + "/auth/v1",
    JWKSURL:     os.Getenv("SUPABASE_URL") + "/auth/v1/keys",
    JWTAudience: "authenticated",
})
if err != nil { return fmt.Errorf("jwt validator: %w", err) }

// existing API-key middleware kept as-is.
jwtMW := jwtMiddleware(jwtValidator, auditLogger)
selector := auth.Selector(jwtMW(protectedRouter), apiKeyMW(protectedRouter))

r.Mount("/v1", selector)
```

The new `jwtMiddleware` helper lives in `apps/edge-api/internal/auth/middleware.go`:

```go
// apps/edge-api/internal/auth/middleware.go
package auth

import (
	"errors"
	"net/http"
	"strings"
)

// jwtMiddleware validates the bearer token, populates context, audits failures.
func jwtMiddleware(v *SupabaseJWTValidator, auditFail func(action, reason, ip string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if raw == "" {
				writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing bearer")
				return
			}
			claims, err := v.Parse(r.Context(), raw)
			if err != nil {
				code := "UNAUTHENTICATED"
				action := "AUTH_JWT_INVALID"
				if errors.Is(err, ErrJWTExpired) {
					code = "JWT_EXPIRED"
					action = "AUTH_JWT_EXPIRED"
				}
				if auditFail != nil {
					auditFail(action, err.Error(), r.RemoteAddr)
				}
				writeAuthError(w, http.StatusUnauthorized, code, "invalid token")
				return
			}
			ctx := WithUser(r.Context(), &User{
				ID: claims.Sub, TenantID: claims.TenantID, Role: claims.Role, Email: claims.Email,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + msg + `","type":"UNAUTHORIZED"}}`))
}
```

The audit callback is wired at the call site from the existing edge-api `audit.Logger` instance — same pattern as control-plane.

- [ ] **Step 2: Build the binary**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go build ./apps/edge-api/..."
```

Expected: builds cleanly.

- [ ] **Step 3: Run the full edge-api test suite**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/edge-api/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`.

- [ ] **Step 4: Commit**

```
git add apps/edge-api/internal/auth/middleware.go apps/edge-api/cmd/edge-api/main.go
git commit -m "feat(phase-19): wire Supabase JWT validator + selector into edge-api"
```

---

## Task 11: Lint script — no direct tenant_id from request

**Files:**
- Create: `tools/lint-no-direct-tenant-id.mjs`
- Modify: `package.json`

- [ ] **Step 1: Write the lint script**

```javascript
// tools/lint-no-direct-tenant-id.mjs
// Block Go handlers that take tenant_id from the request body, query string,
// or header. tenant_id must always come from the resolved auth context via
// auth.TenantID(ctx) so RLS, RBAC, and audit cannot be spoofed.

import { readFileSync } from 'node:fs';
import { execSync } from 'node:child_process';

const ALLOWLIST_DIRS = [
  'apps/control-plane/internal/tenants/',     // tenant-switch handler reads from body deliberately
  'apps/control-plane/internal/signup/',      // webhook receives user_id (not tenant_id) from Supabase
  'apps/edge-api/internal/auth/',             // ctx writers
  'supabase/migrations/',
  'tools/lint-no-direct-tenant-id.mjs',
];

const FORBIDDEN = [
  /\.FormValue\(\s*"tenant_id"\s*\)/,
  /\.Get\(\s*"X-Tenant-Id"\s*\)/i,
  /r\.URL\.Query\(\)\.Get\(\s*"tenant_id"\s*\)/,
  /json:"tenant_id"/,    // a Go struct receiving tenant_id from the wire is a smell
];

const FILE_GLOB = "{apps,packages}/**/*.go";

const files = execSync(`git ls-files -- ${FILE_GLOB}`, { encoding: 'utf8' })
  .split('\n')
  .filter(Boolean);

let violations = 0;
for (const file of files) {
  if (ALLOWLIST_DIRS.some(p => file.startsWith(p))) continue;
  const text = readFileSync(file, 'utf8');
  for (const re of FORBIDDEN) {
    if (re.test(text)) {
      const lines = text.split('\n');
      lines.forEach((line, i) => {
        if (re.test(line)) {
          console.error(`${file}:${i + 1}: forbidden direct tenant_id read — use auth.TenantID(ctx)`);
          violations++;
        }
      });
    }
  }
}

if (violations > 0) {
  console.error(`\n${violations} tenant-id lint violation(s).`);
  process.exit(1);
}
console.log('lint-no-direct-tenant-id: PASS');
```

- [ ] **Step 2: Add npm script to `package.json`**

```json
"lint:tenant-id": "node tools/lint-no-direct-tenant-id.mjs"
```

- [ ] **Step 3: Run lint locally**

```
npm run lint:tenant-id
```

Expected: `lint-no-direct-tenant-id: PASS`.

- [ ] **Step 4: Commit**

```
git add tools/lint-no-direct-tenant-id.mjs package.json
git commit -m "ci(phase-19): add lint blocking direct tenant_id reads from request"
```

---

## Plan 02 Self-Review Checklist

Walk through the spec (`docs/superpowers/specs/2026-05-16-phase-19-foundation-design.md`) with fresh eyes:

- [ ] Spec §6 — identity bridge — every component implemented: OIDC config (deferred to Plan 03 compose work), custom_access_token_hook (Plan 01), signup webhook (Task 3), OWUI admin client (Task 1), tenant switch endpoint (Task 4).
- [ ] Spec §6 — JWT pass-through edge-api validator (Task 5), selector for the two auth modes (Task 6), ctx getters (Task 6), user context preserved through chain.
- [ ] Spec §9 — Phase 18 RBAC reuse — permission additions (Task 7), role-permission map extended (Task 7), cross-tenant guard (Task 8).
- [ ] Lint coverage — direct tenant_id reads (Task 11) plus the two lints from Plan 01.
- [ ] Coverage gates met: identity-bridge handlers ≥ 90% line. Run:
  ```
  cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/signup/... ./apps/control-plane/internal/tenants/... ./apps/edge-api/internal/auth/... -cover -count=1 -short -buildvcs=false"
  ```
- [ ] All new audit actions are in the Plan 01 `securityActions` set: `AUTH_SIGNUP_SUCCESS`, `TENANT_USER_ADD`, `OWUI_GROUP_CREATE_SUCCESS`, `OWUI_GROUP_CREATE_FAILURE`, `OWUI_GROUP_ADD_SUCCESS`, `OWUI_GROUP_ADD_FAILURE`, `TENANT_SWITCH`, `CROSS_TENANT_ATTEMPT`, `AUTH_JWT_INVALID`, `AUTH_JWT_EXPIRED`, `AUTH_SIGNIN_FAILURE_NO_TENANT`. Spot-check `actions.go` — all present.
- [ ] No reliance on `tenant_invites` or `tenant_email_domains` tables existing yet — resolver returns `ErrNoMatch` cleanly when their migrations are absent. Plan 03 introduces them (Task 1 of Plan 03).

## Hand-off to Plan 03

Plan 03 (`2026-05-16-phase-19-03-deploy-and-chat.md`) consumes:

* `auth.SupabaseJWTValidator` + `auth.Selector` + the `User` ctx — used by the chat dispatch path.
* `owui.Client` — pipeline filter integration smoke tests reuse the same admin token.
* `signup.Webhook` is already routed; Plan 03 wires the OWUI compose service against it.
* `authz.PermChatInvoke` + `authz.RequireOwnTenant` — chat dispatch middleware uses both.

Plan 02 is independently testable and shippable; no Plan 03 code is required for Plan 02 tests to pass.
