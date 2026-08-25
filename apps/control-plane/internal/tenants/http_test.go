package tenants_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenants"
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

// -----------------------------------------------------------------------------
// Concurrency: Switch's single WHERE...EXISTS UPDATE statement is what closed
// the TOCTOU window a prior SELECT/UPDATE split left open (see the handler
// comment above Switch). These tests exercise that under real concurrent
// load against real Postgres: many simultaneous calls must never corrupt
// selected_tenant_id and must never grant a non-member a switch. Those two
// invariants are the membership-check's job and hold deterministically.
//
// The audit write used to be a *separate*, best-effort invariant: a burst of
// N simultaneous Switch calls funnels into audit.SyncWriter.Write, which runs
// under SERIALIZABLE isolation with its own internal retry (3 attempts)
// around a genuine read/write conflict on public.audit_log's per-month
// MAX(seq) read. Diagnosed live against this suite's own Postgres (10
// fully-simultaneous audit.Write calls, no HTTP layer involved): 6 of 10
// still failed with SQLSTATE 40001 after exhausting all 3 retries, because
// the SERIALIZABLE snapshot was taken at the first statement inside the
// transaction (the old pg_advisory_xact_lock call) — BEFORE that lock was
// actually granted — so a blocked writer's snapshot predated its own turn.
// apps/control-plane/internal/tenants/http.go discarded the audit.Log()
// return with a bare `_ = ...` on both call sites, directly contradicting the
// comment above Switch promising "a misconfiguration cannot silently... lose
// a CROSS_TENANT_ATTEMPT event"; that was fixed to log.Printf on failure
// instead of discarding it. #1182 then fixed the root cause in the audit
// package itself: the advisory lock is now session-scoped and acquired
// BEFORE BeginTx, so the transaction's snapshot can only form once this
// writer already has exclusive possession of the month. These tests now
// assert the contract the writer actually holds: exactly N audit rows for N
// fully-simultaneous calls, not merely "at least one".
// -----------------------------------------------------------------------------

func TestSwitch_ConcurrentCallsFromActiveMember_AllSucceedConsistently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := newTenantsPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantA := mustInsertTenant(t, ctx, pool, "concur-a-"+uuid.NewString(), "HIVE_CLOUD")
	tenantB := mustInsertTenant(t, ctx, pool, "concur-b-"+uuid.NewString(), "HIVE_CLOUD")
	userID := mustInsertAuthUser(t, ctx, pool, "concur@y.example")
	mustInsertMembership(t, ctx, pool, tenantB, userID, "MEMBER")

	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  noopWAL{},
	})
	h := tenants.NewHandler(tenants.Deps{Pool: pool, Audit: logger})

	const n = 10
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			body, _ := json.Marshal(map[string]string{"tenant_id": tenantB.String()})
			req := httptest.NewRequest(http.MethodPost, "/v1/tenants/switch", bytes.NewReader(body))
			req = req.WithContext(tenants.WithUser(req.Context(), tenants.User{ID: userID, TenantID: tenantA}))
			rec := httptest.NewRecorder()
			h.Switch(rec, req)
			codes[idx] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("call %d: expected 200 for an active member switching concurrently, got %d", i, code)
		}
	}

	var selected string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT raw_user_meta_data->>'selected_tenant_id' FROM auth.users WHERE id=$1`, userID).Scan(&selected))
	require.Equal(t, tenantB.String(), selected,
		"selected_tenant_id must land on the switched-to tenant regardless of how many concurrent calls raced to set it")

	var switchCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.audit_log WHERE actor_id=$1 AND action='TENANT_SWITCH'`, userID).Scan(&switchCount))
	require.Equal(t, n, switchCount,
		"expected exactly N TENANT_SWITCH audit rows for N fully-simultaneous switch calls (#1182); fewer means the audit writer is still losing rows under concurrency")
}

func TestSwitch_ConcurrentCallsFromNonMember_AllRejectedNoneLeakThrough(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool := newTenantsPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantA := mustInsertTenant(t, ctx, pool, "concur-nm-a-"+uuid.NewString(), "HIVE_CLOUD")
	tenantB := mustInsertTenant(t, ctx, pool, "concur-nm-b-"+uuid.NewString(), "HIVE_CLOUD")
	userID := mustInsertAuthUser(t, ctx, pool, "concur-nm@y.example")
	mustInsertMembership(t, ctx, pool, tenantA, userID, "MEMBER") // member of A, NOT B

	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  noopWAL{},
	})
	h := tenants.NewHandler(tenants.Deps{Pool: pool, Audit: logger})

	const n = 10
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			body, _ := json.Marshal(map[string]string{"tenant_id": tenantB.String()})
			req := httptest.NewRequest(http.MethodPost, "/v1/tenants/switch", bytes.NewReader(body))
			req = req.WithContext(tenants.WithUser(req.Context(), tenants.User{ID: userID, TenantID: tenantA}))
			rec := httptest.NewRecorder()
			h.Switch(rec, req)
			codes[idx] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusForbidden {
			t.Fatalf("call %d: a non-member must never be granted a switch under concurrent load, got %d (want 403)", i, code)
		}
	}

	var selected *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT raw_user_meta_data->>'selected_tenant_id' FROM auth.users WHERE id=$1`, userID).Scan(&selected))
	if selected != nil {
		t.Fatalf("expected selected_tenant_id to remain unset for a user with no successful switch, got %q", *selected)
	}

	var attemptCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.audit_log WHERE actor_id=$1 AND action='CROSS_TENANT_ATTEMPT'`, userID).Scan(&attemptCount))
	require.Equal(t, n, attemptCount,
		"expected exactly N CROSS_TENANT_ATTEMPT audit rows for N fully-simultaneous rejected attempts (#1182); fewer means the audit writer is still losing rows under concurrency")
}
