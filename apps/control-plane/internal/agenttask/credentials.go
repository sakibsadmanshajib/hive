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
	CreateKeyWithID(ctx context.Context, accountID, actorUserID, keyID uuid.UUID, input apikeys.CreateKeyInput) (apikeys.CreateKeyResult, error)
	RevokeKey(ctx context.Context, accountID, actorUserID, keyID uuid.UUID) (apikeys.APIKey, error)
}

// credentialTTL bounds a credential that never gets revoked, which happens
// only when control-plane dies between launching a task and observing its
// terminal state. It has to comfortably exceed the longest a task can run,
// because a credential that expires mid-run breaks the run: nothing bounds a
// sandbox's wall-clock life today, and the longest observed on the demo box is
// about sixteen minutes (#886). Twelve hours is that with a very wide margin,
// and it is a backstop, not the mechanism: Service and Poller revoke on every
// terminal transition they see.
const credentialTTL = 12 * time.Hour

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
	created, err := c.keys.CreateKeyWithID(ctx, accountID, t.UserID, t.ID, apikeys.CreateKeyInput{
		Nickname:  "agent task " + t.ID.String(),
		ExpiresAt: &expiresAt,
	})
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
	accountID, err := c.accountFor(ctx, t.TenantID)
	if err != nil {
		return err
	}
	if _, err := c.keys.RevokeKey(ctx, accountID, t.UserID, t.ID); err != nil {
		if errors.Is(err, apikeys.ErrNotFound) || errors.Is(err, apikeys.ErrRevoked) {
			return nil
		}
		return fmt.Errorf("agenttask: revoke task credential: %w", err)
	}
	return nil
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
