package compliance_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	// 3. Stale state removed, not merely hidden. The migration deletes rows
	//    that were written before the control was retired, so no workspace
	//    keeps a stored value that reports a control it does not have.
	var lingering int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*)::int
		   FROM public.tenant_settings
		  WHERE key::text LIKE 'ENABLE_AUDIT_SINK%'`).Scan(&lingering))
	require.Zero(t, lingering, "stored audit sink settings must be deleted, not left orphaned")
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
		_, err := pool.Exec(ctx,
			`INSERT INTO public.tenant_settings(tenant_id, key, enabled)
			 VALUES ($1, 'ENABLE_AUDIT_SINK_ELK'::public.tenant_setting_key, true)
			 ON CONFLICT (tenant_id, key) DO UPDATE SET enabled = true`,
			tenantID)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(),
				`DELETE FROM public.tenant_settings
				  WHERE tenant_id = $1
				    AND key = 'ENABLE_AUDIT_SINK_ELK'::public.tenant_setting_key`,
				tenantID)
		})

		configured := sinkconfig.FromEnv()
		require.Empty(t, configured,
			"no sink may be configured while ENABLE_AUDIT_SINK_ELK is unset, whatever tenant_settings says")

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

		configured := sinkconfig.FromEnv()
		require.Len(t, configured, 1)
		require.Equal(t, "elk", configured[0].Name())

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
func runWorker(t *testing.T, ctx context.Context, pool *pgxpool.Pool, configured []auditworker.Sink) {
	t.Helper()
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	worker := auditworker.New(auditworker.Config{
		Pool:         pool,
		Sinks:        configured,
		MaxAttempts:  32,
		BackoffStart: 5 * time.Second,
		BackoffMax:   30 * time.Second,
		PollInterval: 50 * time.Millisecond,
		LeaseTTL:     time.Minute,
	})
	go worker.Run(runCtx)
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
