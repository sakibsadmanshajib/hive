package compliance_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAuditRetention_ColdArchiveManifestExists confirms the cold-archive
// manifest table has the expected shape. The cold-archive job itself is
// wired in Plan 03 main.go but does not run on every CI tick — full
// archive smoke is exercised in scheduled CI only. Here we only assert
// schema, which keeps the test fast and deterministic.
func TestAuditRetention_ColdArchiveManifestExists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	rows, err := pool.Query(ctx, `
		SELECT column_name
		  FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name = 'audit_cold_archive_manifest'
		 ORDER BY ordinal_position`)
	require.NoError(t, err)
	defer rows.Close()

	got := map[string]bool{}
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		got[n] = true
	}
	require.NoError(t, rows.Err())

	expected := []string{
		"id",
		"tenant_id",
		"partition_month",
		"object_key",
		"sha256_hash",
		"row_count",
		"first_seq",
		"last_seq",
		"archived_at",
		"purge_after",
	}
	for _, c := range expected {
		require.True(t, got[c], "missing column %s on public.audit_cold_archive_manifest", c)
	}
}
