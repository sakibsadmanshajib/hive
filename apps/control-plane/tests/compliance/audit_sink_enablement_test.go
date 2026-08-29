package compliance_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker/sinkconfig"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

// auditSinkGateKeys are the six keys issue #755 retired. They are written as
// literals rather than as settings.Key constants on purpose: the constants
// were deleted with the registry rows, and a test that could still name them
// would quietly come back to life if someone re-declared them.
var auditSinkGateKeys = []string{
	"ENABLE_AUDIT_SINK_ELK",
	"ENABLE_AUDIT_SINK_LOKI",
	"ENABLE_AUDIT_SINK_DATADOG",
	"ENABLE_AUDIT_SINK_SPLUNK",
	"ENABLE_AUDIT_SINK_SENTRY",
	"ENABLE_AUDIT_SINK_LANGFUSE",
}

// TestAuditSinkGatesAreNotARenderedControl is the issue #755 acceptance
// criterion 1 in its outcome-B form: the six audit sink toggles are off the
// rendered control set entirely, and there is no write path that stores a
// per-tenant enablement value for them.
//
// It runs against the real migrated schema rather than a fake store, because
// the removal IS a migration: settings.Resolver.Registry backs what the
// console renders and settings.Resolver.Set backs what the console writes,
// and both read public.feature_gate_keys. A fake store would prove only that
// the test author remembered to leave the rows out of the fixture.
func TestAuditSinkGatesAreNotARenderedControl(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(pool.Close)

	resolver := settings.NewResolver(pool, time.Minute)

	// 1. Nothing in the rendered registry.
	registry, err := resolver.Registry(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, registry, "registry must not be empty; an empty one would pass every assertion below vacuously")
	for _, gate := range registry {
		require.NotEqualf(t, "audit_sink", gate.Category,
			"gate %q still renders under the retired audit_sink category", gate.Key)
	}
	byKey := make(map[string]bool, len(registry))
	for _, gate := range registry {
		byKey[string(gate.Key)] = true
	}
	for _, key := range auditSinkGateKeys {
		require.Falsef(t, byKey[key], "%s is still a rendered feature gate", key)
	}

	// 2. No write path. Set is the only sanctioned writer of tenant_settings
	//    and the admin PUT route goes through it, so a rejection here is a 400
	//    on the wire rather than a silent success.
	tenantID := insertTenant(t, ctx, pool, "audit-sink-retired-"+uuid.NewString()[:8])
	for _, key := range auditSinkGateKeys {
		err := resolver.Set(ctx, tenantID, settings.Key(key), true, uuid.Nil)
		require.ErrorIsf(t, err, settings.ErrUnknownGateKey,
			"Set(%s) must be refused as an unknown gate key", key)
	}
	var stored int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*)::int FROM public.tenant_settings WHERE tenant_id = $1`,
		tenantID).Scan(&stored))
	require.Zero(t, stored, "a refused Set must leave no tenant_settings row behind")

	// 3. Stale state removed, not merely hidden. This is the half that needs
	//    a seeded row to mean anything. A throwaway CI database has never had
	//    an operator write one of these settings, so asserting "no such row
	//    exists" against it passes because nothing wrote one, not because the
	//    migration deleted it. Delete the migration's first statement and
	//    that assertion would still be green while a real demo box kept six
	//    rows reporting a control that no longer exists.
	//
	//    So: write the row the way a pre-retirement deployment would have,
	//    then re-apply the migration and assert it is gone. Re-applying is
	//    safe because the migration is two set-based deletes with no DDL.
	seedRetiredSetting(t, ctx, pool, tenantID)

	// A registered key alongside it, written through the sanctioned path. It
	// is the other half of the guard: running the shipped file rather than a
	// pasted copy is what catches an edit to that file, and the edit worth
	// catching most is a DELETE that loses its WHERE clause. Without a row
	// that must survive, a migration reduced to `DELETE FROM
	// public.tenant_settings` would be executed here and would pass.
	require.NoError(t, resolver.Set(ctx, tenantID, settings.EnableRAG, true, uuid.Nil))

	var seeded int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*)::int FROM public.tenant_settings WHERE tenant_id = $1`,
		tenantID).Scan(&seeded))
	require.Equal(t, 2, seeded, "the seeds themselves must land, or the assertions below prove nothing")

	applyRetirementMigration(t, ctx, pool)

	var lingering int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*)::int
		   FROM public.tenant_settings
		  WHERE key::text LIKE 'ENABLE_AUDIT_SINK%'`).Scan(&lingering))
	require.Zero(t, lingering, "stored audit sink settings must be deleted, not left orphaned")

	var survivors int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*)::int
		   FROM public.tenant_settings
		  WHERE tenant_id = $1
		    AND key = 'ENABLE_RAG'::public.tenant_setting_key`,
		tenantID).Scan(&survivors))
	require.Equal(t, 1, survivors,
		"the migration must delete the six retired keys and nothing else")
}

// seedRetiredSetting writes a tenant_settings row for a retired audit sink key
// the way a deployment that predates the retirement would carry one. Raw SQL
// is the only way: settings.Resolver.Set refuses the key, which is half of
// what this suite asserts.
func seedRetiredSetting(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_settings(tenant_id, key, enabled)
		 VALUES ($1, 'ENABLE_AUDIT_SINK_ELK'::public.tenant_setting_key, true)
		 ON CONFLICT (tenant_id, key) DO UPDATE SET enabled = true`,
		tenantID)
	require.NoError(t, err)
}

// applyRetirementMigration executes the retirement migration's own SQL against
// the test database, so what is under test is the file that ships rather than
// a copy of its statements pasted into a test that would not notice if the
// file changed.
//
// It goes through PgConn().Exec because the file is a multi-statement script
// (BEGIN, two DELETEs, COMMIT) and pgx's normal Exec path uses the extended
// protocol, which refuses more than one command per call.
func applyRetirementMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "supabase", "migrations",
		"20260829_03_retire_audit_sink_feature_gates.sql")
	script, err := os.ReadFile(path)
	require.NoErrorf(t, err, "the migration under test must be readable at %s", path)

	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	_, err = conn.Conn().PgConn().Exec(ctx, string(script)).ReadAll()
	require.NoError(t, err)
}

// TestAuditSinkEnablementIsEnvironmentOnly is issue #755 acceptance criterion
// 2: flip the setting in tenant_settings, and assert an audit event is or is
// not exported. Both directions are exercised against a real receiver so the
// claim is about bytes leaving the process, not about a slice length.
//
// The sink set comes from sinkconfig.FromEnv, which is the same constructor
// apps/control-plane/cmd/server/main.go calls, so this cannot pass while
// production disagrees.
func TestAuditSinkEnablementIsEnvironmentOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(pool.Close)

	tenantID := insertTenant(t, ctx, pool, "audit-sink-env-"+uuid.NewString()[:8])

	t.Run("a tenant_settings row cannot enable a sink the environment has not", func(t *testing.T) {
		received := newReceiver(t)

		// Credentials present, enable flag absent: the zero-egress default.
		t.Setenv("AUDIT_SINK_ELK_URL", received.URL)
		t.Setenv("AUDIT_SINK_ELK_API_KEY", "not-a-real-key")

		// The database says yes, as loudly as it can. This row is written by
		// raw SQL because Set now refuses the key; that is the point. Even a
		// row that predates the retirement, or one an operator inserts by
		// hand, must not reach the egress decision.
		seedRetiredSetting(t, ctx, pool, tenantID)
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(),
				`DELETE FROM public.tenant_settings
				  WHERE tenant_id = $1
				    AND key = 'ENABLE_AUDIT_SINK_ELK'::public.tenant_setting_key`,
				tenantID)
		})

		configured, skipped := sinkconfig.FromEnv()
		require.Empty(t, configured,
			"no sink may be configured while ENABLE_AUDIT_SINK_ELK is unset, whatever tenant_settings says")
		require.Empty(t, skipped,
			"an unset flag is not a skipped sink; a tenant_settings row must not make it read as one")

		outboxID := enqueueAuditEvent(t, ctx, pool, tenantID, "elk")
		runWorker(t, ctx, pool, configured)

		// Give the worker several poll intervals to do the wrong thing.
		require.Never(t, func() bool {
			return received.count() > 0 || delivered(t, ctx, pool, outboxID)
		}, 3*time.Second, 100*time.Millisecond,
			"the audit event must not be exported: the environment never enabled the sink")
	})

	t.Run("the environment flag alone enables, with no tenant_settings row", func(t *testing.T) {
		received := newReceiver(t)

		t.Setenv("ENABLE_AUDIT_SINK_ELK", "true")
		t.Setenv("AUDIT_SINK_ELK_URL", received.URL)
		t.Setenv("AUDIT_SINK_ELK_API_KEY", "not-a-real-key")

		// Deliberately no tenant_settings row at all. If the decision had any
		// database input, this direction would be the one that broke.
		var rows int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*)::int FROM public.tenant_settings
			  WHERE tenant_id = $1
			    AND key = 'ENABLE_AUDIT_SINK_ELK'::public.tenant_setting_key`,
			tenantID).Scan(&rows))
		require.Zero(t, rows)

		configured, skipped := sinkconfig.FromEnv()
		require.Len(t, configured, 1)
		require.Equal(t, "elk", configured[0].Name())
		require.Empty(t, skipped)

		outboxID := enqueueAuditEvent(t, ctx, pool, tenantID, "elk")
		runWorker(t, ctx, pool, configured)

		require.Eventually(t, func() bool {
			return received.count() > 0 && delivered(t, ctx, pool, outboxID)
		}, 15*time.Second, 100*time.Millisecond,
			"the audit event must be exported once the environment enables the sink")
	})
}

// receiver is a stand-in for the operator's ELK endpoint that counts the
// audit records actually posted to it.
type receiver struct {
	URL  string
	hits atomic.Int64
}

func (r *receiver) count() int64 { return r.hits.Load() }

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil && payload["action"] != nil {
			r.hits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	r.URL = srv.URL
	return r
}

// insertTenant creates a throwaway tenant so the tenant_settings rows this
// suite writes cannot collide with another job's fixtures.
func insertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO public.tenants(slug, name, deployment) VALUES ($1, $1, 'HIVE_CLOUD') RETURNING id`,
		slug).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, id)
	})
	return id
}

// enqueueAuditEvent writes one audit_log row and one audit_outbox row aimed at
// sinkName, returning the outbox id. It exists because nothing in production
// enqueues audit_outbox today (tracked separately); the fan-out path can only
// be exercised by putting a row there by hand.
func enqueueAuditEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, sinkName string) int64 {
	t.Helper()
	requestID := uuid.New()
	writer := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "ci", Env: "ci"})
	require.NoError(t, writer.Write(ctx, audit.Event{
		TenantID:  tenantID,
		Action:    "CHAT_REQUEST",
		Severity:  audit.SeverityInfo,
		Actor:     audit.Actor{Type: audit.ActorSystem},
		RequestID: requestID,
	}))

	var outboxID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO public.audit_outbox(audit_id, audit_ts, sink)
		 SELECT id, ts, $2
		   FROM public.audit_log
		  WHERE request_id = $1
		  ORDER BY ts DESC
		  LIMIT 1
		 RETURNING id`,
		requestID, sinkName).Scan(&outboxID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.audit_outbox WHERE id = $1`, outboxID)
	})
	return outboxID
}

// runWorker starts a fan-out worker for the duration of the subtest. The
// retry budget is generous so a row under test stays in audit_outbox rather
// than moving to the DLQ mid-assertion.
//
// Cleanup waits for the goroutine to exit rather than only cancelling its
// context. Worker.Run notices cancellation on its next ticker tick, so a
// cleanup that returns immediately leaves a worker from the previous subtest
// still claiming rows: drainOnce claims any eligible row on the deployment,
// not one scoped to a sink or a tenant, so it would compete for the next
// subtest's freshly enqueued row. Today that resolves itself (the leaked
// worker holds an empty sink set and its failure path clears the claim), but
// depending on that is depending on an accident.
func runWorker(t *testing.T, ctx context.Context, pool *pgxpool.Pool, configured []auditworker.Sink) {
	t.Helper()
	runCtx, cancel := context.WithCancel(ctx)
	worker := auditworker.New(auditworker.Config{
		Pool:         pool,
		Sinks:        configured,
		MaxAttempts:  32,
		BackoffStart: 5 * time.Second,
		BackoffMax:   30 * time.Second,
		PollInterval: 50 * time.Millisecond,
		LeaseTTL:     time.Minute,
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func delivered(t *testing.T, ctx context.Context, pool *pgxpool.Pool, outboxID int64) bool {
	t.Helper()
	var deliveredAt *time.Time
	err := pool.QueryRow(ctx,
		`SELECT delivered_at FROM public.audit_outbox WHERE id = $1`, outboxID).Scan(&deliveredAt)
	if err != nil {
		return false
	}
	return deliveredAt != nil
}
