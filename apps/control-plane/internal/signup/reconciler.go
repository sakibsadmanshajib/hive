package signup

// Provisioning sweep: the shipped replacement for the Supabase Database
// Webhook (D-023).
//
// The webhook that used to drive provisioning was created in the hosted
// project's dashboard, so it was console state, not repository state. Deleting
// the project removes it with no diff, no error and no failing test, and new
// identities then stop being provisioned entirely. Reconciler closes that by
// making the control-plane itself responsible: it periodically asks the
// database which identities hold no tenant membership and runs the SAME
// Provisioner.Reconcile the webhook and the console route run. There is still
// exactly one writer.
//
// Why a sweep rather than the database calling us back. The mechanism a
// Supabase Database Webhook actually is, a trigger invoking pg_net through the
// supabase_functions schema, does not exist on the self-hosted data plane this
// repo deploys: that database image offers only the vector extension, with no
// pg_net, no http extension and no such schema. Even where it did exist, the
// trigger would need the shared secret in database state, which in a public
// repository means either a committed secret or an out-of-band console step,
// the same failure mode wearing a different hat, and its delivery failures
// land in a table nobody reads. Reimplementing provisioning as SQL in a
// trigger was rejected too: it would duplicate the disposable backstop, the
// resolver precedence, the deployment posture and the audit trail in a second
// language (D-005).
//
// Why a bounded window. The operator backfill command exists for historical
// membership-less identities and is deliberately NOT wired into startup,
// because a tenancy write that happens on every deploy is one nobody reviews.
// The sweep therefore only looks at identities created inside a lookback
// window, and never at ones whose age the database cannot establish
// (auth.users.created_at is nullable with no default on real GoTrue, so a
// hand-written row has no age). Older stragglers stay the operator's business.
//
// Concurrency and duplication are already settled by the write path: the
// existing-membership short-circuit, the tenant_users primary key with ON
// CONFLICT DO NOTHING, and the partial unique index behind personal tenants
// mean two processes sweeping the same database produce exactly one
// membership.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultSweepInterval = 5 * time.Minute
	defaultSweepLookback = 24 * time.Hour
	defaultSweepBatch    = 200

	// noTenantCooldown is how long a terminal no_tenant determination is
	// trusted before it is attempted again. Reconcile documents that outcome
	// as terminal until an administrator invites the identity or registers
	// its email domain, and every attempt writes an immutable, hash-chained
	// audit row, so re-deciding it every interval is pure noise. The cooldown
	// keeps an administered (Hive Enterprise) deployment quiet while still
	// picking the identity up once an administrator does act.
	noTenantCooldown = time.Hour

	// sweepDeadline bounds one pass. Without it a sweep that hangs on the
	// database never returns, never records a failure, and the health
	// endpoint keeps reporting ready while nobody is being provisioned,
	// which is the exact silent shape this type exists to remove. Shorter
	// than the default interval so a stuck pass cannot overlap the next one.
	sweepDeadline = 2 * time.Minute
)

// ReconcilerConfig tunes the sweep. Every field is optional.
type ReconcilerConfig struct {
	// Interval between sweeps. Defaults to five minutes.
	Interval time.Duration
	// Lookback bounds how recently an identity must have been created to be
	// swept. Defaults to 24 hours. See the package comment for why this is
	// bounded at all.
	Lookback time.Duration
	// BatchLimit caps candidates per sweep. Defaults to 200.
	BatchLimit int
}

func (c ReconcilerConfig) withDefaults() ReconcilerConfig {
	if c.Interval <= 0 {
		c.Interval = defaultSweepInterval
	}
	if c.Lookback <= 0 {
		c.Lookback = defaultSweepLookback
	}
	if c.BatchLimit <= 0 {
		c.BatchLimit = defaultSweepBatch
	}
	return c
}

// SweepReport is what one sweep did. Counts only: this is logged, and an
// identity's address belongs in auth.users alone.
type SweepReport struct {
	// Candidates is how many identities the sweep considered.
	Candidates int
	// Provisioned is how many hold an ACTIVE membership afterwards.
	Provisioned int
	// NoTenant is how many no tenant claims, a terminal determination.
	NoTenant int
	// Cooled is how many identities the listing excluded because a recent sweep
	// already reached a terminal no-tenant determination for them.
	Cooled int
	// Failed is how many hit a transient or unexpected fault.
	Failed int
}

// Reconciler sweeps for identities with no tenant membership and provisions
// them through Provisioner.
type Reconciler struct {
	pool *pgxpool.Pool
	prov *Provisioner
	cfg  ReconcilerConfig

	mu           sync.Mutex
	failures     int
	lastNoTenant map[uuid.UUID]time.Time
}

// NewReconciler constructs a Reconciler over the same Provisioner the webhook
// and console routes use, so no third implementation of the write exists.
func NewReconciler(pool *pgxpool.Pool, prov *Provisioner, cfg ReconcilerConfig) *Reconciler {
	return &Reconciler{
		pool:         pool,
		prov:         prov,
		cfg:          cfg.withDefaults(),
		lastNoTenant: map[uuid.UUID]time.Time{},
	}
}

// Message formats live here rather than inline at each call site so the sweep
// has one place to audit for anything an operator or an audit reader should not
// see. None of them carries an address, a tenant id or a driver error string.
const (
	logSweepFailed    = "signup: provisioning sweep failed: %v"
	logSweepReport    = "signup: provisioning sweep candidates=%d provisioned=%d no_tenant=%d cooled=%d failed=%d"
	errSweepNotWired  = "signup: provisioning sweep not wired"
	errListCandidates = "signup: list provisioning candidates: %w"
	errScanCandidate  = "signup: scan provisioning candidate: %w"
	errReadCandidates = "signup: read provisioning candidates: %w"
	reasonUnwired     = "signup provisioning unwired"
)

// Start sweeps once immediately and then on the configured interval until ctx
// is cancelled. Bind it to the process-lifetime context, not a startup one.
func (r *Reconciler) Start(ctx context.Context) {
	if r == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(r.cfg.Interval)
		defer ticker.Stop()
		for {
			// The deadline is per pass, so a hung pass is reported as a failed
			// sweep and counted. Only the parent context ending means shutdown.
			sweepCtx, cancelSweep := context.WithTimeout(ctx, sweepDeadline)
			report, err := r.Sweep(sweepCtx)
			cancelSweep()
			if ctx.Err() != nil {
				return
			}
			switch {
			case err != nil:
				log.Printf(logSweepFailed, err)
			case report.Candidates > 0:
				log.Printf(logSweepReport,
					report.Candidates, report.Provisioned, report.NoTenant, report.Cooled, report.Failed)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Ready reports whether provisioning is WIRED, and nothing else. The health
// endpoint asks it, so its answer cannot be missed the way a startup log line
// was.
//
// A nil receiver reports false on purpose. The failure this whole type exists to
// prevent was an absence nobody noticed, so nothing-wired must read as broken
// rather than as silence. That condition is a programming error rather than a
// runtime one: it cannot appear or clear by itself, so answering an unhealthy
// readiness probe on it costs nothing that was working a moment ago.
//
// A failing sweep is deliberately NOT reported here. Provisioning can be broken
// while API-key resolution, routing, accounting and payment webhooks all still
// work, and this endpoint is the container healthcheck: degrading it on a
// runtime provisioning fault would convert a signup outage into an inference and
// billing outage, and a restart would reset the counter and repeat. That state is
// exported through ConsecutiveFailures for the telemetry listener instead, where
// an alert can fire without taking the process out of service.
func (r *Reconciler) Ready() (ok bool, reason string) {
	if r == nil || r.pool == nil || r.prov == nil {
		return false, reasonUnwired
	}
	// reason stays at its zero value: there is nothing to report.
	return true, reason
}

// ConsecutiveFailures is how many sweeps in a row have failed, for the metric on
// the telemetry listener. Zero means the last sweep completed its candidates.
func (r *Reconciler) ConsecutiveFailures() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failures
}

type sweepCandidate struct {
	userID uuid.UUID
	email  string
}

// Sweep runs one pass. It returns an error only when the sweep itself could not
// run; an identity no tenant claims is a successful determination, and a single
// identity that faulted is reported in SweepReport.Failed.
func (r *Reconciler) Sweep(ctx context.Context) (SweepReport, error) {
	var report SweepReport
	if r == nil || r.pool == nil || r.prov == nil {
		return report, errors.New(errSweepNotWired)
	}

	// Excluded in SQL rather than skipped in Go: the batch limit is applied by
	// the database, so an identity filtered afterwards would still consume a
	// slot and, on a deployment where hundreds of identities are permanently
	// unclaimable, would starve the ones behind it until they aged out of the
	// window (review finding, Greptile on PR 993).
	cooled := r.cooledIdentities()
	report.Cooled = len(cooled)
	candidates, err := r.candidates(ctx, cooled)
	if err != nil {
		r.recordSweep(false)
		return report, err
	}
	report.Candidates = len(candidates)

	for _, c := range candidates {
		if ctx.Err() != nil {
			// A pass that ran out of time or was cancelled did not finish its
			// work, so it is a failed sweep rather than a quiet one.
			r.recordSweep(false)
			return report, ctx.Err()
		}
		outcome, err := r.prov.Reconcile(ctx, ReconcileInput{UserID: c.userID, Email: c.email})
		switch {
		case err != nil:
			// Reconcile has already audited the classification and logged the
			// raw error, so this stays a count.
			report.Failed++
		case outcome == OutcomeProvisioned:
			report.Provisioned++
		default:
			report.NoTenant++
			r.cool(c.userID)
		}
	}

	// A sweep that faulted on an identity is a provisioning outage in progress,
	// not a quiet afternoon: it counts against the failure gauge exactly like a
	// failed listing does. See Ready for why that is a metric and an alert
	// rather than an unhealthy readiness probe.
	r.recordSweep(report.Failed == 0)
	return report, nil
}

func (r *Reconciler) recordSweep(ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ok {
		r.failures = 0
		return
	}
	r.failures++
}

// cooledIdentities returns the identities whose terminal no-tenant determination
// is still trusted, and prunes the ones whose cooldown has expired. Bounded by
// the lookback window: an identity that ages out of the window stops being
// listed, and its entry is dropped on the next pass.
func (r *Reconciler) cooledIdentities() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.lastNoTenant))
	for id, at := range r.lastNoTenant {
		if time.Since(at) >= noTenantCooldown {
			delete(r.lastNoTenant, id)
			continue
		}
		ids = append(ids, id.String())
	}
	return ids
}

func (r *Reconciler) cool(userID uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// ponytail: in-memory, per process, so a restart re-attempts every
	// determination once. Pruning happens in cooledIdentities, which runs on
	// every pass, and the lookback window bounds the map regardless. Move it
	// onto the identity row only if a deployment ever needs the back-off to
	// survive a restart.
	r.lastNoTenant[userID] = time.Now()
}

// candidateQuery lists identities with no ACTIVE membership on a non-archived
// tenant. The NOT EXISTS predicate is deliberately the same one
// Provisioner.activeMembership and public.custom_access_token_hook use, so a
// candidate is precisely an identity whose next token would carry no tenant
// claim. Keep the three in step.
//
// The lifecycle filters are not decoration. A soft-deleted or banned identity
// must never be provisioned. An identity whose created_at is NULL is excluded
// by the window comparison itself, since a comparison against NULL is not
// true, and that is deliberate rather than incidental: auth.users.created_at
// is nullable with no default on real GoTrue, so NULL means a row somebody
// wrote by hand rather than a signup, and its age cannot be established. An
// explicit IS NOT NULL clause was dropped after a mutation test showed it
// could not fail, which made it decoration standing next to load-bearing
// filters. The assertion covering it lives in the sweep suite.
const candidateQuery = `
	SELECT u.id, u.email
	  FROM auth.users u
	 WHERE u.email IS NOT NULL
	   AND u.email <> ''
	   AND u.deleted_at IS NULL
	   AND (u.banned_until IS NULL OR u.banned_until <= now())
	   AND u.created_at > now() - make_interval(secs => $1)
	   AND NOT EXISTS (
	       SELECT 1
	         FROM public.tenant_users tu
	         JOIN public.tenants t ON t.id = tu.tenant_id
	        WHERE tu.user_id     = u.id
	          AND tu.status      = 'ACTIVE'
	          AND t.archived_at IS NULL
	   )
	   AND NOT (u.id = ANY($3::uuid[]))
	 ORDER BY u.created_at DESC
	 LIMIT $2
`

func (r *Reconciler) candidates(ctx context.Context, cooled []string) ([]sweepCandidate, error) {
	rows, err := r.pool.Query(ctx, candidateQuery, r.cfg.Lookback.Seconds(), r.cfg.BatchLimit, cooled)
	if err != nil {
		return nil, fmt.Errorf(errListCandidates, err)
	}
	defer rows.Close()

	var out []sweepCandidate
	for rows.Next() {
		var c sweepCandidate
		if err := rows.Scan(&c.userID, &c.email); err != nil {
			return nil, fmt.Errorf(errScanCandidate, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errReadCandidates, err)
	}
	return out, nil
}
