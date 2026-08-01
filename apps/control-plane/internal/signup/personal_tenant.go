package signup

// Personal-tenant provisioning (issue #625).
//
// Reconcile's original invariant was that it never creates a tenant: it only
// attaches a user to a tenant that already claims them, by unconsumed invite
// token or by verified email domain. That invariant is deliberately relaxed
// here, and only here, for one case: a Hive Cloud signup that no tenant
// claims. Such a user used to reach OutcomeNoTenant and stay there forever,
// which was merely "unentitled" until PR #620 made API-key tenant resolution
// fail closed, at which point it became "every request 403s, permanently".
//
// The relaxation is narrow by construction:
//
//   - Resolution still runs FIRST. An invite token or a registered email
//     domain still wins, so invite-based and domain-based attachment are
//     byte-for-byte unchanged and a personal tenant is only ever minted when
//     neither matched.
//   - It is gated on WebhookDeps.SelfServeTenants, which is false for Hive
//     Enterprise, whose posture is that membership is administered.
//   - It creates a tenant of one, which "one org equals one tenant" (D-007)
//     already covers. It is not a new tenancy concept.
//
// Billing mapping is deliberately NOT reimplemented here. The existing
// two-call-site convergence (Provisioner.ensureTenantBillingAccount and
// accounts.Service.provisionDefaultWorkspace, both calling the conservative
// EnsureTenantBillingAccount) already maps whichever half of the
// tenant_users/account_memberships pairing lands second, in either order. A
// signup whose account does not exist yet is simply mapped later, by the
// console visit that creates it.

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
)

// personalTenantName is the display name every personal tenant carries. It is
// deliberately not derived from the user's name or email: tenants.name is
// readable wherever a tenant is listed, and an email local part there would
// spread an identifier that belongs in auth.users alone.
const personalTenantName = "Personal workspace"

// provisionSelfServeTenant handles the Hive Cloud no-match case: mint the
// user's personal tenant, then run the same provision() the invite and domain
// paths run, so membership, billing mapping, audit events and Open WebUI group
// wiring are identical no matter which path produced the tenant.
//
// Audit classification mirrors the rest of Reconcile: the event carries a
// fixed stage and error string, never the raw database error, which may embed
// SQL fragments or DSN substrings that auditor_ro must not read.
func (p *Provisioner) provisionSelfServeTenant(
	ctx context.Context, in ReconcileInput, emailToken, emailDomain string,
) (Outcome, error) {
	tenantID, err := ensurePersonalTenant(ctx, p.deps.Pool, in.UserID)
	if err != nil {
		log.Printf("signup: personal tenant provisioning failed user=%s: %v",
			in.UserID, fmt.Errorf("personal_tenant_db: %w", err))
		_ = p.deps.Audit.Log(ctx, audit.Event{
			Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
			Action:   "AUTH_SIGNIN_FAILURE_NO_TENANT",
			Severity: audit.SeverityError,
			Before: map[string]string{"email_sha256": emailToken, "domain": emailDomain,
				"stage": "personal_tenant", "error": "personal_tenant_db"},
		})
		return OutcomeNoTenant, fmt.Errorf("signup: provision personal tenant: %w", err)
	}

	if err := p.provision(ctx, tenantID, in, emailToken, emailDomain); err != nil {
		log.Printf("signup: provision failed user=%s tenant=%s: %v",
			in.UserID, tenantID, fmt.Errorf("provision_db: %w", err))
		return OutcomeNoTenant, fmt.Errorf("signup: provision membership: %w", err)
	}
	return OutcomeProvisioned, nil
}

// ensurePersonalTenant is the single writer of a personal tenant, shared by
// the live signup path and BackfillPersonalTenants so the two cannot drift.
//
// Concurrency is resolved by the DATABASE, not by a check-then-act here. The
// slug is derived from userID, so concurrent callers for the same user collide
// on tenants_slug_key, and the partial unique index
// tenants_personal_owner_user_id_key (migration
// 20260801_10_tenants_personal_owner.sql) independently rejects a second
// personal tenant for that user. A bare ON CONFLICT DO NOTHING covers both
// without having to guess which index Postgres checks first. The loser inserts
// nothing, reads the winner's row, and returns the same id, so eight racing
// callers produce exactly one tenant.
//
// A read that finds nothing after a conflict means the slug collided with a
// tenant that is not this user's personal tenant. That cannot happen for a
// uuid-derived slug, so it is reported as an error rather than papered over.
func ensurePersonalTenant(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, errors.New("signup: nil pool")
	}
	if userID == uuid.Nil {
		return uuid.Nil, errors.New("signup: personal tenant requires a user id")
	}

	slug := "personal-" + userID.String()

	var tenantID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO public.tenants (slug, name, deployment, personal_owner_user_id)
		VALUES ($1, $2, 'HIVE_CLOUD', $3)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, slug, personalTenantName, userID).Scan(&tenantID)
	if err == nil {
		return tenantID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("insert personal tenant: %w", err)
	}

	// Lost the race (or already provisioned on an earlier call). Read the
	// winner's row.
	err = pool.QueryRow(ctx, `
		SELECT id FROM public.tenants WHERE personal_owner_user_id = $1
	`, userID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("personal tenant slug %q is taken by another tenant", slug)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read personal tenant: %w", err)
	}
	return tenantID, nil
}

// insertPersonalMembership attaches userID to tenantID.
//
// Role is MEMBER, matching (*Provisioner).provision, and that is deliberate
// even though the user is the only member of their own tenant. OWNER would be
// the intuitive choice, but public.custom_access_token_hook remaps an OWNER
// role to owui_role ADMIN for Open WebUI's OAUTH_ROLES_CLAIM, so making every
// self-serve signup an OWNER would silently make every self-serve signup an
// Open WebUI administrator. Workspace-level authority already lives on
// public.account_memberships, where the same user is 'owner' of their own
// account, so nothing is lost by keeping the tenant role minimal.
func insertPersonalMembership(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
		VALUES ($1, $2, 'MEMBER', 'ACTIVE')
		ON CONFLICT DO NOTHING
	`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("insert tenant_users: %w", err)
	}
	return nil
}

// BackfillReport records what a BackfillPersonalTenants sweep did, keyed by
// account id. Skipped carries a reason per account precisely because those
// accounts are the ones an operator has to look at: they stay locked out, and
// a wrong mapping would be permanent and unrecoverable, so they are reported
// rather than guessed.
type BackfillReport struct {
	// Provisioned lists accounts that resolve a tenant after the sweep, and
	// therefore now pass PR #620's fail-closed API-key gate.
	Provisioned []uuid.UUID
	// Skipped maps an account left unmapped to the reason it was left alone.
	Skipped map[uuid.UUID]string
}

// BackfillPersonalTenants gives a tenant to every account that holds an
// active API key but has no billing mapping, which is the state that answers
// 403 account_not_provisioned on every request after PR #620.
//
// It runs the SAME mechanism the live signup path runs (ensurePersonalTenant
// then EnsureTenantBillingAccount) rather than a bespoke SQL predicate. That
// is the point: PR #624's backfill migration refused to map accounts it could
// not resolve unambiguously, and that guard was correct, so the fix is to make
// a tenant exist rather than to widen the predicate that decides which tenant
// an account belongs to.
//
// Two cases, both conservative:
//
//   - The owner already holds an ACTIVE tenant membership. No second tenant is
//     minted. The sweep retries EnsureTenantBillingAccount against the tenant
//     they already have, which is the same call both live call sites make, and
//     which frequently succeeds now because the account side of the pairing
//     has since landed.
//   - The owner holds no tenant at all. A personal tenant is created for them,
//     exactly as a fresh Hive Cloud signup would now get.
//
// In both cases the mapping itself is still decided by
// EnsureTenantBillingAccount, so an owner whose accounts are genuinely
// ambiguous (two active account memberships, no single billing answer) is left
// UNMAPPED with a reason. A wrong mapping is worse than a 403.
//
// Safe to re-run: an account that acquired a mapping is no longer a candidate,
// and the partial unique index makes a repeat tenant insert a no-op.
func BackfillPersonalTenants(ctx context.Context, pool *pgxpool.Pool) (BackfillReport, error) {
	report := BackfillReport{Skipped: map[uuid.UUID]string{}}
	if pool == nil {
		return report, errors.New("signup: nil pool")
	}

	type candidate struct{ accountID, ownerID uuid.UUID }

	rows, err := pool.Query(ctx, `
		SELECT DISTINCT a.id, a.owner_user_id
		  FROM public.accounts a
		  JOIN public.api_keys k
		    ON k.account_id = a.id
		   AND k.status = 'active'
		 WHERE NOT EXISTS (
		       SELECT 1 FROM public.tenant_billing_accounts tba
		        WHERE tba.account_id = a.id
		 )
		 ORDER BY a.id
	`)
	if err != nil {
		return report, fmt.Errorf("signup: list backfill candidates: %w", err)
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.accountID, &c.ownerID); err != nil {
			rows.Close()
			return report, fmt.Errorf("signup: scan backfill candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("signup: read backfill candidates: %w", err)
	}

	for _, c := range candidates {
		if c.ownerID == uuid.Nil {
			report.Skipped[c.accountID] = "no_owner_identity"
			continue
		}

		tenantID, err := backfillTenantFor(ctx, pool, c.ownerID)
		if err != nil {
			return report, fmt.Errorf("signup: resolve tenant for account %s: %w", c.accountID, err)
		}

		mapped, reason, err := EnsureTenantBillingAccount(ctx, pool, tenantID)
		if err != nil {
			return report, fmt.Errorf("signup: map billing account %s: %w", c.accountID, err)
		}
		if !mapped {
			report.Skipped[c.accountID] = reason
			continue
		}
		// The tenant is mapped, but confirm it is mapped to THIS account
		// before reporting this account unblocked. A tenant whose owner has
		// several accounts maps to one of them, and the others stay locked
		// out and must be reported as such.
		var mine bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM public.tenant_billing_accounts
				 WHERE tenant_id = $1 AND account_id = $2
			)
		`, tenantID, c.accountID).Scan(&mine); err != nil {
			return report, fmt.Errorf("signup: confirm mapping for account %s: %w", c.accountID, err)
		}
		if !mine {
			report.Skipped[c.accountID] = "tenant_already_billed_by_another_account"
			continue
		}
		report.Provisioned = append(report.Provisioned, c.accountID)
	}

	return report, nil
}

// backfillTenantFor returns the tenant a backfill candidate's owner should
// bill to: the ACTIVE tenant they already belong to if there is one, otherwise
// a freshly provisioned personal tenant. Never mints a second tenant for an
// owner who already has one.
//
// The ORDER BY is not an arbitrary tie-break. public.custom_access_token_hook
// selects a multi-tenant user's default tenant with exactly this ordering
// (joined_at ASC, tenant_id ASC), so the tenant chosen here is the one that
// user's token already carries by default. Picking any other row would bill an
// account to a tenant the user is not acting under. Choosing the tenant is
// still not the same as choosing the mapping: EnsureTenantBillingAccount
// applies its own conservative predicate to whatever tenant comes back, and
// refuses if that tenant's members do not converge on a single account.
func backfillTenantFor(ctx context.Context, pool *pgxpool.Pool, ownerID uuid.UUID) (uuid.UUID, error) {
	var tenantID uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT tu.tenant_id
		  FROM public.tenant_users tu
		  JOIN public.tenants t ON t.id = tu.tenant_id
		 WHERE tu.user_id     = $1
		   AND tu.status      = 'ACTIVE'
		   AND t.archived_at IS NULL
		 ORDER BY tu.joined_at ASC, tu.tenant_id ASC
		 LIMIT 1
	`, ownerID).Scan(&tenantID)
	if err == nil {
		return tenantID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("read existing tenant: %w", err)
	}

	tenantID, err = ensurePersonalTenant(ctx, pool, ownerID)
	if err != nil {
		return uuid.Nil, err
	}
	if err := insertPersonalMembership(ctx, pool, tenantID, ownerID); err != nil {
		return uuid.Nil, err
	}
	return tenantID, nil
}
