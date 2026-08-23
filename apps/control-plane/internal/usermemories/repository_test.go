package usermemories_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usermemories"
)

// newRLSTestPool connects as the hive_app role, NOT BYPASSRLS, so the
// user_memories_tenant_isolation policy is actually exercised. Mirrors
// agenttask/repository_test.go's helper of the same name.
func newRLSTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse HIVE_TEST_DB_URL: %v", err)
	}
	cfg.MaxConns = 1
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(ctx, "SET ROLE hive_app"); err != nil {
		pool.Close()
		t.Skipf("SET ROLE hive_app failed: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenant mirrors agenttask/repository_test.go: a short-lived, unscoped
// connection inserts the FK row public.tenants requires, since hive_app has
// no INSERT policy on that table.
func seedTenant(t *testing.T, id uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()
	_, err = setup.Exec(ctx,
		`INSERT INTO public.tenants (id, slug, name, deployment)
		 VALUES ($1, $2, 'usermemories test tenant', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		id, "usermemories-test-"+id.String())
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, id)
	})
}

// seedUser inserts a minimal auth.users row so user_memories.user_id's FK
// is satisfiable. Mirrors agenttask/repository_test.go's helper.
func seedUser(t *testing.T) uuid.UUID {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()

	var id uuid.UUID
	email := "usermemories-test-" + uuid.NewString() + "@example.invalid"
	err = setup.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		email).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, id)
	})
	return id
}

func TestRepository_RoundTrip(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := usermemories.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userID := seedUser(t)

	src := "chat-abc"
	m, err := repo.Create(ctx, tenantID, userID, "prefers terse answers", &src)
	require.NoError(t, err)
	require.Equal(t, tenantID, m.TenantID)
	require.NotNil(t, m.SourceChatID)

	got, err := repo.Get(ctx, tenantID, userID, m.ID)
	require.NoError(t, err)
	require.Equal(t, "prefers terse answers", got.Content)

	list, err := repo.List(ctx, tenantID, userID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	upd, err := repo.Update(ctx, tenantID, userID, m.ID, "prefers verbose answers")
	require.NoError(t, err)
	require.Equal(t, "prefers verbose answers", upd.Content)

	require.NoError(t, repo.Delete(ctx, tenantID, userID, m.ID))
	_, err = repo.Get(ctx, tenantID, userID, m.ID)
	require.ErrorIs(t, err, usermemories.ErrNotFound)
}

func TestRepository_UserScopingWithinTenant(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := usermemories.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userA, userB := seedUser(t), seedUser(t)

	m, err := repo.Create(ctx, tenantID, userA, "user A private fact", nil)
	require.NoError(t, err)

	_, err = repo.Get(ctx, tenantID, userB, m.ID)
	require.ErrorIs(t, err, usermemories.ErrNotFound)

	listB, err := repo.List(ctx, tenantID, userB)
	require.NoError(t, err)
	require.Empty(t, listB)

	_, err = repo.Update(ctx, tenantID, userB, m.ID, "hijacked")
	require.ErrorIs(t, err, usermemories.ErrNotFound)

	err = repo.Delete(ctx, tenantID, userB, m.ID)
	require.ErrorIs(t, err, usermemories.ErrNotFound)

	got, err := repo.Get(ctx, tenantID, userA, m.ID)
	require.NoError(t, err)
	require.Equal(t, "user A private fact", got.Content)
}

func TestRepository_CrossTenantIsDeniedByRLS(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := usermemories.NewPgxRepository(pool)
	ctx := context.Background()

	tenantA, tenantB := uuid.New(), uuid.New()
	seedTenant(t, tenantA)
	seedTenant(t, tenantB)
	userID := seedUser(t)

	m, err := repo.Create(ctx, tenantA, userID, "tenant A only", nil)
	require.NoError(t, err)

	// Same user id presented under the other tenant's scope: RLS filters
	// every row out, so reads and writes collapse to not-found.
	_, err = repo.Get(ctx, tenantB, userID, m.ID)
	require.ErrorIs(t, err, usermemories.ErrNotFound)
	err = repo.Delete(ctx, tenantB, userID, m.ID)
	require.ErrorIs(t, err, usermemories.ErrNotFound)

	list, err := repo.List(ctx, tenantB, userID)
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestRepository_EvictOldestKeepsNewest(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := usermemories.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userID := seedUser(t)

	var last uuid.UUID
	for i := 0; i < 105; i++ {
		m, err := repo.Create(ctx, tenantID, userID, strings.Repeat("m", 10)+string(rune('a'+i%26)), nil)
		require.NoError(t, err)
		last = m.ID
	}
	evicted, err := repo.EvictOldest(ctx, tenantID, userID, 100)
	require.NoError(t, err)
	require.Equal(t, int64(5), evicted)

	list, err := repo.List(ctx, tenantID, userID)
	require.NoError(t, err)
	require.Len(t, list, 100)

	// The newest row survives eviction.
	_, err = repo.Get(ctx, tenantID, userID, last)
	require.NoError(t, err)
}

func TestRepository_ContentOver500RejectedByCheck(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := usermemories.NewPgxRepository(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userID := seedUser(t)

	long := strings.Repeat("x", 501)
	_, err := repo.Create(ctx, tenantID, userID, long, nil)
	require.Error(t, err)
}
