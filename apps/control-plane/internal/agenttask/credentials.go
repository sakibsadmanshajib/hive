package agenttask

// Per-task gateway credential (issue #1507).
//
// The defect this closes: an agent task charged its tenant nothing. The
// sandbox's model calls do go through edge-api and are metered there, but they
// carry the launcher's one process-wide HIVE_AGENT_ENGINE_LLM_API_KEY, so
// every tenant's inference settles against the single Hive-owned account that
// key belongs to. PR #1466 named this and left it open: "buildAgentEngine sets
// no per-task LLM credential, so there is no attribution seam today." This
// file is that seam.
//
// The submit-time solvency probe in edge-api's agent-task handler is NOT this,
// and is unchanged: it takes a launch-floor hold and hands it straight back in
// the same call, because control-plane owns the task lifecycle and a hold left
// open over a run would strand permanently (public.credit_reservations has no
// expires_at and no reaper, #600). That probe answers "can this tenant pay for
// a task at all". What accounts for the run is the ordinary per-turn
// hold-charge-release the sandbox's own credential now takes, one per model
// call, at the model's real catalog price.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/apikeys"
)

// TaskCredentials mints and revokes the gateway credential one task's sandbox
// spends. Small on purpose: Service and Poller need exactly these two verbs,
// and both take the whole Task because the credential is derived from it
// (tenant chooses the billing account, task id IS the key id).
type TaskCredentials interface {
	// Mint returns the raw secret the sandbox authenticates with. The secret
	// is transient: it is handed to the engine and never persisted, exactly
	// like Task.BearerJWT.
	Mint(ctx context.Context, t Task) (string, error)
	// Revoke ends the credential early. Idempotent: a credential that was
	// never minted, or is already revoked, is not an error.
	Revoke(ctx context.Context, t Task) error
}

// ErrTaskCredentialsNotConfigured is what an unwired Service mints instead of
// a credential. It fails the launch rather than falling back to the launcher's
// process-wide key, because that fallback is the defect (#1507): a sandbox
// spending Hive's own key for a tenant nobody charged.
var ErrTaskCredentialsNotConfigured = errors.New("agenttask: task credentials not configured")

// ErrNoBillingAccount is returned when the task's tenant has no row in
// public.tenant_billing_accounts. Fails closed for the same reason
// sessionbilling refuses a tenant with no billing account rather than serving
// it free (D-034).
var ErrNoBillingAccount = errors.New("agenttask: tenant has no billing account")

// ErrCredentialUnaccountedFor is returned when a revocation cannot find the
// credential it was told to destroy.
//
// It exists because "already revoked" and "I could not find it" are different
// states that a single ErrNotFound collapses into the comfortable one. Collapsed,
// a credential that is still live and still spendable reads as a clean
// revocation, which is the exact shape of #1507 itself: a bad outcome wearing a
// good signal. Callers must treat this as an unrevoked credential.
var ErrCredentialUnaccountedFor = errors.New("agenttask: task credential could not be found to revoke")

// notConfiguredCredentials is NewService's default. Mint refuses; Revoke
// succeeds trivially, since nothing was ever minted to revoke.
type notConfiguredCredentials struct{}

func (notConfiguredCredentials) Mint(context.Context, Task) (string, error) {
	return "", ErrTaskCredentialsNotConfigured
}

func (notConfiguredCredentials) Revoke(context.Context, Task) error { return nil }

// KeyIssuer is the slice of apikeys.Service this package uses. Declared here
// rather than taken as a concrete type so a test can substitute one without a
// database.
type KeyIssuer interface {
	CreateAgentTaskKey(ctx context.Context, accountID, actorUserID, taskID uuid.UUID, expiresAt *time.Time) (apikeys.CreateKeyResult, error)
	RevokeAgentTaskKey(ctx context.Context, taskID uuid.UUID) (apikeys.APIKey, error)
}

// credentialTTL bounds a credential that never gets revoked.
//
// Revocation is the mechanism; this is only the backstop, and it covers
// exactly one window: control-plane dying between minting a credential and
// observing any terminal state for its task. Every other path is already
// covered and much faster. A launcher crash or restart makes the status check
// fail, and the poller's failure budget declares the task dead in about five
// minutes, revoking then; ErrEngineSessionGone revokes on the next pass; a
// launch that errors revokes immediately.
//
// Two hours rather than twelve. The window this bounds is an unmonitored
// crash, not normal runtime, so sizing it against the longest task is sizing
// it against the wrong thing: the longest sandbox run observed on the demo box
// is about sixteen minutes (#886), and two hours is seven times that. What
// happens if a task ever DOES outlive it is the fail-closed direction and is
// the reason this is not shorter still: the sandbox's next model call is
// refused, the task fails, and the poller records it, which is loud. The
// alternative failure, a live spendable credential on a customer's billing
// account for half a day because a process died, is silent, and silence is
// what #1507 is made of.
const credentialTTL = 2 * time.Hour

// AccountDB is the one query this file makes against Postgres. Narrow on
// purpose: *pgxpool.Pool satisfies it, and a test can substitute a row without
// standing up a database for what is otherwise pure lookup-then-mint logic.
type AccountDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PgxTaskCredentials is the production TaskCredentials: it mints a real API
// key on the task's own tenant billing account, so the sandbox's model calls
// settle exactly like any other API-key traffic on that account.
type PgxTaskCredentials struct {
	db   AccountDB
	keys KeyIssuer
}

// Compile-time proof that the production pool still satisfies AccountDB, so a
// pgx upgrade that changes QueryRow's signature fails the build here rather
// than at the one call site in cmd/server.
var _ AccountDB = (*pgxpool.Pool)(nil)

// NewPgxTaskCredentials constructs the production TaskCredentials.
func NewPgxTaskCredentials(db AccountDB, keys KeyIssuer) *PgxTaskCredentials {
	return &PgxTaskCredentials{db: db, keys: keys}
}

// Mint issues the task's credential.
//
// The key id IS the task id. That is deliberate and load bearing: it means the
// task row needs no extra column to remember its credential, revocation needs
// no lookup, and an operator reading public.api_keys can tell at a glance which
// agent task a key belongs to. apikeys enforces uniqueness on the id, so a
// second mint for the same task fails rather than quietly issuing a second
// live credential.
func (c *PgxTaskCredentials) Mint(ctx context.Context, t Task) (string, error) {
	accountID, err := c.accountFor(ctx, t.TenantID)
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(credentialTTL)
	created, err := c.keys.CreateAgentTaskKey(ctx, accountID, t.UserID, t.ID, &expiresAt)
	if err != nil {
		return "", fmt.Errorf("agenttask: mint task credential: %w", err)
	}
	return created.Secret, nil
}

// Revoke ends the task's credential. Already-revoked and never-minted are both
// success: this runs on terminal transitions that can legitimately fire twice
// (a cancel racing the poller), and on tasks that failed before they ever had
// a credential.
func (c *PgxTaskCredentials) Revoke(ctx context.Context, t Task) error {
	// By primary key, and by nothing else. The credential's id IS the task id,
	// so this needs no account and deliberately does not resolve one: Mint
	// already read public.tenant_billing_accounts once, and a SECOND read on
	// the revoke path can disagree with the first. When it does, an
	// account-scoped revoke misses, reports "not found", and a live credential
	// on a real billing account survives its full lifetime with nothing saying
	// so. The lookup that protects nothing is the lookup that breaks this.
	revoked, err := c.keys.RevokeAgentTaskKey(ctx, t.ID)
	if err != nil {
		switch {
		case errors.Is(err, apikeys.ErrRevoked):
			// An observed row in a known terminal state. A terminal transition
			// can legitimately fire twice (a cancel racing the poller), and
			// this is the only shape of "already gone" that is actually
			// evidence of being gone.
			return nil
		case errors.Is(err, apikeys.ErrNotFound):
			return fmt.Errorf("%w: task %s", ErrCredentialUnaccountedFor, t.ID)
		default:
			return fmt.Errorf("agenttask: revoke task credential: %w", err)
		}
	}
	reportZeroCharge(ctx, t, revoked)
	return nil
}

// reportZeroCharge makes a task that cost its tenant nothing loud and
// countable, instead of silent.
//
// This is the whole shape of #1507: agent tasks charged nothing for weeks and
// no signal anywhere said so, because "no usage_charge row" looks exactly like
// "no traffic". A fix that restores the charge without making its absence
// noisy would fail the same way the next time something upstream breaks.
//
// public.api_keys.last_used_at is the exact fact needed, and it already
// exists: accounting.Service.finalizeLocked is its only production writer
// (service.go, in the attempt.APIKeyID branch), so it is stamped when and only
// when a reservation on that key reached a terminal charge. A revoked task
// credential with last_used_at still NULL therefore means, precisely, that not
// one model turn ever settled against this task.
//
// Two records come out of it. The WARN is the loud half, carrying a fixed
// reason string an operator can alert on. The durable, countable half needs no
// new table because the key id IS the task id, so the standing query is:
//
//	SELECT count(*) FROM public.api_keys
//	WHERE nickname LIKE 'agent task %' AND last_used_at IS NULL;
//
// A legitimately empty task (a launch that failed before its first model call)
// lands here too, and that is deliberate. The reason is "settled nothing", not
// "should have settled something", and suppressing the honest zeroes is how
// the dishonest ones get hidden again.
func reportZeroCharge(ctx context.Context, t Task, revoked apikeys.APIKey) {
	if revoked.LastUsedAt != nil {
		return
	}
	slog.Default().WarnContext(ctx, "agenttask: task credential is being revoked having settled no charge at all",
		"reason", "agent_task_credential_settled_nothing",
		"task_id", t.ID, "tenant_id", t.TenantID, "account_id", revoked.AccountID)
}

// accountFor resolves the billing account that settles this tenant's usage.
//
// public.tenant_billing_accounts grants hive_app a permissive SELECT policy
// (20260828_01_tenant_billing_accounts_hive_app_grant.sql), so this needs no
// app.current_tenant_id transaction wrapper the way public.agent_tasks does.
func (c *PgxTaskCredentials) accountFor(ctx context.Context, tenantID uuid.UUID) (uuid.UUID, error) {
	var accountID uuid.UUID
	err := c.db.QueryRow(ctx,
		`SELECT account_id FROM public.tenant_billing_accounts WHERE tenant_id = $1`,
		tenantID).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNoBillingAccount
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("agenttask: resolve billing account: %w", err)
	}
	return accountID, nil
}
