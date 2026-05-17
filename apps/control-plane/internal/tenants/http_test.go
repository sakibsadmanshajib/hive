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

	"github.com/hivegpt/hive/apps/control-plane/internal/audit"
	"github.com/hivegpt/hive/apps/control-plane/internal/tenants"
)

// noopWAL satisfies the audit.WALWriter interface for tests. The switch
// endpoint emits INFO/CRITICAL events that the logger routes to the sync
// writer, so the WAL is never written — but a non-nil implementation is
// still required by audit.NewLogger.
type noopWAL struct{}

func (noopWAL) Write(_ context.Context, _ audit.Event) error { return nil }

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
		WAL:  noopWAL{},
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
		require.NoError(t, rows.Scan(&a))
		actions = append(actions, a)
	}
	require.NoError(t, rows.Err())
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
		WAL:  noopWAL{},
	})
	h := tenants.NewHandler(tenants.Deps{Pool: pool, Audit: logger})

	body, _ := json.Marshal(map[string]string{"tenant_id": tenantB.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/switch", bytes.NewReader(body))
	req = req.WithContext(tenants.WithUser(req.Context(), tenants.User{ID: userID, TenantID: tenantA}))
	rec := httptest.NewRecorder()
	h.Switch(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	var actions []string
	rows, err := pool.Query(ctx,
		`SELECT action FROM public.audit_log WHERE actor_id=$1 ORDER BY seq`, userID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		actions = append(actions, a)
	}
	require.NoError(t, rows.Err())
	require.Contains(t, actions, "CROSS_TENANT_ATTEMPT")
}

func TestSwitch_MissingUser_401(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newTenantsPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  noopWAL{},
	})
	h := tenants.NewHandler(tenants.Deps{Pool: pool, Audit: logger})
	body, _ := json.Marshal(map[string]string{"tenant_id": uuid.New().String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/switch", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Switch(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSwitch_BadBody_400(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newTenantsPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  noopWAL{},
	})
	h := tenants.NewHandler(tenants.Deps{Pool: pool, Audit: logger})
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/switch", bytes.NewReader([]byte(`{"tenant_id":""}`)))
	req = req.WithContext(tenants.WithUser(req.Context(), tenants.User{ID: uuid.New(), TenantID: uuid.New()}))
	rec := httptest.NewRecorder()
	h.Switch(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSwitch_NilDeps_503(t *testing.T) {
	h := tenants.NewHandler(tenants.Deps{})
	body, _ := json.Marshal(map[string]string{"tenant_id": uuid.New().String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/switch", bytes.NewReader(body))
	req = req.WithContext(tenants.WithUser(req.Context(), tenants.User{ID: uuid.New(), TenantID: uuid.New()}))
	rec := httptest.NewRecorder()
	h.Switch(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
