package licensing_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/licensing"
	"github.com/stretchr/testify/require"
)

// newTestPool mirrors apps/control-plane/internal/tenant/settings/testhelpers_test.go:
// skips unless HIVE_TEST_DB_URL is set, and refuses to run against a DSN that
// doesn't look like a test database.
func newTestPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool, pool.Close
}

func TestPgxRecorder_UpsertsSingletonRow(t *testing.T) {
	ctx := context.Background()
	pool, teardown := newTestPool(t, ctx)
	defer teardown()
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.license_state`)
	}()

	rec := licensing.PgxRecorder{Pool: pool}
	now := time.Now().UTC().Truncate(time.Second)
	e := licensing.Entitlement{
		Tier: "enterprise", Seats: 25,
		IssuedAt: now, ExpiresAt: now.AddDate(1, 0, 0), ValidatedAt: now,
		Valid: true,
	}
	require.NoError(t, rec.Record(ctx, e))

	var tier string
	var seats int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT tier, seats FROM public.license_state WHERE singleton = TRUE`,
	).Scan(&tier, &seats))
	require.Equal(t, "enterprise", tier)
	require.Equal(t, 25, seats)

	// A second write upserts the same singleton row rather than inserting a
	// new one -- exactly one row must ever exist.
	e.Seats = 30
	require.NoError(t, rec.Record(ctx, e))
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM public.license_state`).Scan(&count))
	require.Equal(t, 1, count)
}
