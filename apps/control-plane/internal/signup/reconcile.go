package signup

// Tenant membership reconciliation.
//
// public.tenant_users is the table public.custom_access_token_hook reads to
// mint the tenant_id, tenants, role and owui_role claims. Until a user has an
// ACTIVE row there, joined to a non-archived tenant, their token carries no
// tenant claims and is inert everywhere tenant-scoped.
//
// That row was originally written by one caller only: the Supabase Database
// Webhook on auth.users insert, which POSTs /internal/auth/user-created. A
// Database Webhook is dashboard state, not repository state, so a deployment
// that never created it provisions nobody, and every signup on it lands in the
// tenant-less state permanently. That is exactly what happened on the live
// project. Hive Enterprise makes this worse rather than better, because it
// deploys onto customer-operated infrastructure where a forgotten dashboard
// step is a silent deployment landmine.
//
// The fix is to make provisioning reachable from code that ships with the
// repository. Provisioner below is the single implementation of the write.
// Both entry points call it:
//
//   - Webhook (POST /internal/auth/user-created), kept working so a
//     deployment that DOES have the Database Webhook configured behaves
//     exactly as before. Belt and braces, not a replacement.
//   - ViewerHandler (POST /api/v1/viewer/tenant-provision), which the console
//     calls on the first authenticated request from a tenant-less user.
//
// Reconcile attaches a user to a tenant that already claims them, by
// unconsumed invite token or by verified email domain, which is the behaviour
// Resolver has always implemented. When neither matches AND the deployment is
// Hive Cloud, it now also provisions a personal tenant for that user rather
// than leaving them tenant-less forever (issue #625); see personal_tenant.go
// for why that relaxation is narrow and where the concurrency guarantee comes
// from. On Hive Enterprise nothing is created and the outcome is unchanged.

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
)

// Outcome reports what Reconcile concluded about a user's tenant membership.
// It is deliberately coarse: the caller learns whether the user now has a
// membership, never which tenants exist or which one was inspected.
type Outcome string

const (
	// OutcomeProvisioned means the user has an ACTIVE membership on a
	// non-archived tenant, either because one already existed or because
	// Reconcile created it. The next token issued for this user will carry
	// tenant claims.
	OutcomeProvisioned Outcome = "provisioned"

	// OutcomeNoTenant means no tenant claims this user. This is terminal
	// until an administrator invites them or registers their email domain.
	// Retrying will not change the answer.
	OutcomeNoTenant Outcome = "no_tenant"
)

// ReconcileInput is the identity Reconcile provisions for. Every field must
// come from a trusted source: the shared-secret webhook body, or the Supabase
// token the auth middleware already validated. Never populate it from an
// unauthenticated request body.
type ReconcileInput struct {
	UserID uuid.UUID
	Email  string
	// InviteToken is optional and only supplied by the webhook path, which
	// receives it from Supabase. The authenticated console path leaves it
	// empty: letting a caller name an invite token would let any signed-in
	// user attach themselves to any tenant whose token they can guess or
	// obtain. Invitation redemption has its own audited endpoint.
	InviteToken string
}

// Provisioner is the one implementation of tenant membership provisioning.
type Provisioner struct{ deps WebhookDeps }

// NewProvisioner constructs a Provisioner over the same dependency surface the
// webhook uses, so the two entry points cannot drift.
func NewProvisioner(deps WebhookDeps) *Provisioner { return &Provisioner{deps: deps} }

// Reconcile idempotently ensures in.UserID holds an ACTIVE tenant_users row.
//
// Ordering matters:
//
//  1. An existing ACTIVE membership short-circuits with no writes. The
//     predicate is deliberately identical to the one in
//     public.custom_access_token_hook, so OutcomeProvisioned means precisely
//     "the hook will now emit a tenant claim for this user" rather than
//     "a row exists somewhere". It also makes repeat calls from the console
//     cheap and stops a second membership being attached to an already-placed
//     user.
//  2. The disposable-domain backstop runs before any resolution or write.
//  3. Resolver attaches to an existing tenant, or reports ErrNoMatch.
//
// Deployment posture. WebhookDeps.SelfServeTenants branches step 3's
// no-match case. On Hive Cloud a signup that no tenant claims is an org of
// one and gets a personal tenant, because leaving it tenant-less is now a
// permanent 403 rather than merely an absent entitlement (issue #625, and PR
// #620 for the fail-closed gate that made it fatal). On Hive Enterprise,
// whose posture is that membership is administered, nothing is created and
// the outcome stays OutcomeNoTenant exactly as before. The flag is derived in
// cmd/server/main.go from platform/config.Config.LicenseFilePath, the same
// switch that already selects licensing.FileSource (Hive Enterprise) over
// licensing.CloudSource (Hive Cloud), so posture has one source of truth.
//
// Concurrency. Two callers racing for the same user both fall through the
// short-circuit, both resolve to the same tenant because resolution is a pure
// function of the invite token and the email domain, and both insert. The
// tenant_users primary key (tenant_id, user_id) plus ON CONFLICT DO NOTHING
// makes the second insert a no-op, so the outcome is exactly one membership.
// On the personal-tenant path the tenant itself is created too, and the race
// there is settled by the database rather than by any check in Go: see
// ensurePersonalTenant, which relies on the partial unique index
// tenants_personal_owner_user_id_key.
//
// A nil error with OutcomeNoTenant is a successful determination, not a
// failure. Only transient or unexpected faults return a non-nil error.
func (p *Provisioner) Reconcile(ctx context.Context, in ReconcileInput) (Outcome, error) {
	if p == nil {
		return OutcomeNoTenant, errors.New("signup: nil provisioner")
	}
	if in.UserID == uuid.Nil || in.Email == "" {
		return OutcomeNoTenant, errors.New("signup: reconcile requires user id and email")
	}

	emailToken, emailDomain := emailAuditToken(in.Email)

	// Disposable-domain backstop (issue #116). Deliberately the first gate, so
	// a throwaway address never reaches a tenant read, a membership write, or a
	// free-credit grant. A check error is treated as "not disposable" and
	// logged, so a bug in the list never blocks all signups: availability wins
	// over a soft abuse signal at this backstop layer. The audit detail carries
	// a hash token and the domain only, never the raw address.
	//
	// Running ahead of the existing-membership short-circuit below means a user
	// who somehow already holds a membership on a disposable address would be
	// told no_tenant. That is unreachable from the console, which only calls
	// Reconcile when the token carries no tenant claim, and a token for a user
	// with a live membership always carries one.
	if p.deps.DisposableCheck != nil {
		blocked, checkErr := p.deps.DisposableCheck(in.Email)
		if checkErr != nil {
			log.Printf("signup: disposable check error user=%s: %v", in.UserID, checkErr)
		} else if blocked {
			if p.deps.Audit != nil {
				_ = p.deps.Audit.Log(ctx, audit.Event{
					Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
					Action:   "AUTH_SIGNUP_DISPOSABLE_BLOCKED",
					Severity: audit.SeverityWarning,
					Before:   map[string]string{"stage": "disposable", "email_sha256": emailToken, "domain": emailDomain},
				})
			}
			return OutcomeNoTenant, nil
		}
	}

	if p.deps.Pool == nil || p.deps.Resolver == nil || p.deps.Audit == nil {
		return OutcomeNoTenant, errors.New("signup: provisioner misconfigured")
	}

	existing, err := p.activeMembership(ctx, in.UserID)
	if err != nil {
		return OutcomeNoTenant, fmt.Errorf("signup: read existing membership: %w", err)
	}
	if existing {
		return OutcomeProvisioned, nil
	}

	tenantID, err := p.deps.Resolver.Resolve(ctx, Input{Email: in.Email, InviteToken: in.InviteToken})
	if err != nil {
		if errors.Is(err, ErrNoMatch) {
			// No existing tenant claims this user. On Hive Cloud that is a
			// self-serve signup and gets a personal tenant (issue #625);
			// resolution above ran first, so an invite or a registered email
			// domain still wins and this only ever fires when neither matched.
			if p.deps.SelfServeTenants {
				return p.provisionSelfServeTenant(ctx, in, emailToken, emailDomain)
			}
			_ = p.deps.Audit.Log(ctx, audit.Event{
				Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
				Action:   "AUTH_SIGNIN_FAILURE_NO_TENANT",
				Severity: audit.SeverityWarning,
				Before:   map[string]string{"email_sha256": emailToken, "domain": emailDomain},
			})
			return OutcomeNoTenant, nil
		}
		// Audit payload carries a fixed classification string only. The real
		// error may embed SQL fragments, DSN substrings or upstream provider
		// details and surfaces via the process log instead; auditor_ro must
		// not read raw pgx/fmt errors.
		log.Printf("signup: resolver error user=%s: %v", in.UserID, fmt.Errorf("resolver_transient: %w", err))
		_ = p.deps.Audit.Log(ctx, audit.Event{
			Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
			Action:   "AUTH_SIGNIN_FAILURE_NO_TENANT",
			Severity: audit.SeverityError,
			Before:   map[string]string{"email_sha256": emailToken, "domain": emailDomain, "stage": "resolver_error", "error": "resolver_transient"},
		})
		return OutcomeNoTenant, fmt.Errorf("signup: resolve tenant: %w", err)
	}

	if err := p.provision(ctx, tenantID, in, emailToken, emailDomain); err != nil {
		log.Printf("signup: provision failed user=%s tenant=%s: %v", in.UserID, tenantID, fmt.Errorf("provision_db: %w", err))
		_ = p.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
			Action:   "AUTH_SIGNUP_SUCCESS",
			Severity: audit.SeverityError,
			Before:   map[string]string{"email_sha256": emailToken, "domain": emailDomain, "stage": "provision_failed", "error": "provision_db"},
		})
		return OutcomeNoTenant, fmt.Errorf("signup: provision membership: %w", err)
	}

	return OutcomeProvisioned, nil
}

// activeMembership reports whether the user already holds a membership the
// token hook would accept. The predicate mirrors
// public.custom_access_token_hook exactly: ACTIVE status on a tenant whose
// archived_at is null. Keep the two in step.
func (p *Provisioner) activeMembership(ctx context.Context, userID uuid.UUID) (bool, error) {
	var exists bool
	err := p.deps.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM public.tenant_users tu
			  JOIN public.tenants t ON t.id = tu.tenant_id
			 WHERE tu.user_id     = $1
			   AND tu.status      = 'ACTIVE'
			   AND t.archived_at IS NULL
		)
	`, userID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// provision writes the membership and wires the Open WebUI group. Unchanged in
// behaviour from the webhook-only implementation it replaces; the insert stays
// ON CONFLICT DO NOTHING against the (tenant_id, user_id) primary key, which
// is what makes a concurrent retry a no-op rather than a duplicate.
func (p *Provisioner) provision(ctx context.Context, tenantID uuid.UUID, in ReconcileInput, emailToken, emailDomain string) error {
	_, err := p.deps.Pool.Exec(ctx, `
		INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
		VALUES ($1, $2, 'MEMBER', 'ACTIVE')
		ON CONFLICT DO NOTHING
	`, tenantID, in.UserID)
	if err != nil {
		return fmt.Errorf("insert tenant_users: %w", err)
	}

	// Backend metering (vault spec-2026-07-28-backend-metering.md section
	// 9.2, Step 1) needs every tenant mapped to a billing account before the
	// gate can meter it. Best-effort and non-fatal: a billing-account gap is
	// caught later by the metering gate's own fail-closed check, so it must
	// never block a user from getting their tenant membership.
	if err := p.ensureTenantBillingAccount(ctx, tenantID); err != nil {
		log.Printf("signup: tenant billing account not resolved tenant=%s: %v", tenantID, err)
	}

	_ = p.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
		Action:   "AUTH_SIGNUP_SUCCESS",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"email_sha256": emailToken, "domain": emailDomain},
	})
	_ = p.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
		Action:   "TENANT_USER_ADD",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"role": "MEMBER"},
	})

	if p.deps.EnsureGroup == nil || p.deps.AddUser == nil {
		log.Printf("signup: OWUI provisioning skipped (deps not configured) user=%s tenant=%s",
			in.UserID, tenantID)
		return nil
	}
	groupName := "tenant_" + tenantID.String()
	groupID, err := p.deps.EnsureGroup(ctx, groupName)
	if err != nil {
		// Audit payload carries the classification only. The raw Open WebUI
		// upstream error, which may echo back Authorization headers on some
		// 401/403 paths, goes to the process log and never to
		// auditor_ro-readable rows.
		log.Printf("signup: owui ensure group tenant=%s: %v", tenantID, fmt.Errorf("owui_ensure_group: %w", err))
		_ = p.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Action:   "OWUI_GROUP_CREATE_FAILURE",
			Severity: audit.SeverityError,
			Before:   map[string]string{"name": groupName, "error": "owui_ensure_group"},
		})
		return fmt.Errorf("ensure group: %w", err)
	}
	if err := p.deps.AddUser(ctx, groupID, in.Email); err != nil {
		log.Printf("signup: owui add user tenant=%s group=%s: %v", tenantID, groupID, fmt.Errorf("owui_add_user: %w", err))
		_ = p.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
			Action:   "OWUI_GROUP_ADD_FAILURE",
			Severity: audit.SeverityError,
			Before:   map[string]string{"group_id": groupID, "email_sha256": emailToken, "domain": emailDomain, "error": "owui_add_user"},
		})
		return fmt.Errorf("add user: %w", err)
	}
	_ = p.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: in.UserID, Type: audit.ActorUser},
		Action:   "OWUI_GROUP_ADD_SUCCESS",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"group_id": groupID, "email_sha256": emailToken, "domain": emailDomain},
	})
	return nil
}

// ensureTenantBillingAccount maps tenantID to a billing account by delegating
// to the exported EnsureTenantBillingAccount, over this Provisioner's own
// pool. See that function's doc for the mapping rule.
//
// Deliberately does not log on a miss here. This is usually the first of the
// two racing call sites to run (see EnsureTenantBillingAccount's doc), so a
// miss here is the expected, common, temporary state, not a failure — the
// accounts package's call site is where a miss is worth a signal.
func (p *Provisioner) ensureTenantBillingAccount(ctx context.Context, tenantID uuid.UUID) error {
	_, _, err := EnsureTenantBillingAccount(ctx, p.deps.Pool, tenantID)
	return err
}

// EnsureTenantBillingAccount maps tenantID to a billing account in
// public.tenant_billing_accounts, the table introduced by migration
// 20260728_01_tenant_billing_account.sql (vault
// spec-2026-07-28-backend-metering.md section 3.2). It is deliberately
// conservative and mirrors the backfill migrations
// (20260728_03_tenant_billing_account_backfill.sql and
// 20260731_01_tenant_billing_account_backfill_v2.sql) rather than inventing a
// second rule: a mapping is only ever created when it is unambiguous, never
// guessed.
//
// Exported so more than one creation path can attempt it. tenant_users rows
// and account_memberships rows are written by two different, independently
// timed processes (Reconcile attaches a user to a tenant; the accounts
// package lazily provisions a personal workspace on first console visit), and
// in practice either one can land first. A single call site that only checks
// "is my own tenant_users insert unambiguous right now" misses every case
// where the matching account_membership row does not exist yet at that
// instant — confirmed live on 2026-07-31: freshly created single-member,
// single-account HIVE_CLOUD tenants stayed unmapped because the membership
// row was written 28-60s after the tenant_users row that triggered the old
// check. Calling this again from wherever the account_membership side of the
// pairing completes (accounts.Service.provisionDefaultWorkspace) closes that
// race: whichever of the two writes happens second finds the data complete
// and succeeds. Both callers hit the same idempotent, conservative predicate,
// so calling it twice for the same tenant is always safe.
//
// A mapping is resolvable when tenantID has no mapping yet, every ACTIVE
// member of the tenant has an ACTIVE account membership (no stragglers still
// mid-provisioning — see the unresolvedMembers check below), and all of
// those memberships converge on exactly one distinct account. Unlike the
// single-member restriction this function used to enforce, a tenant that
// already has two or more members is still eligible as long as they agree —
// that is the same predicate the backfill migrations use, so a tenant this
// function cannot map today (because a member hasn't reconciled yet, or the
// account side of the race hasn't landed for one of them) becomes mappable
// the next time either caller runs for it, rather than being permanently
// excluded after its first member. Per D-005, this function does NOT fall
// back to picking an account by owner_user_id, and does NOT create a new
// account: an unresolvable tenant is left unmapped, exactly like the
// backfill migrations leave ambiguous tenants unmapped, and surfaces the
// same way (the metering gate's fail-closed 403, not silently here).
//
// Deployment-agnostic by construction: this function has never filtered on
// tenants.deployment, it only ever operates on the tenantID its caller
// already resolved. Both call sites (signup.Provisioner and
// accounts.Service.provisionDefaultWorkspace) already invoke it for every
// tenant regardless of deployment, so Enterprise (ENTERPRISE_EDGE) tenants
// were always reachable here — the deployment scoping lived only in the
// backfill migrations, not in this function. See
// 20260731_02_tenant_billing_account_all_deployments.sql for why extending
// the sweep to non-Cloud tenants reuses this same table rather than adding a
// second one.
//
// Never fatal, but a miss is never invisible either: the old version's
// INSERT could match zero rows and return a nil error with no trace anywhere
// that anything was attempted, which is exactly why this went unnoticed
// while it was failing live. Returns whether tenantID ended up mapped, and
// when it did not, a short reason string a caller may log. Logging is left
// to the caller rather than done in here: with two call sites racing the
// same tenant (see doc above), the first one to run legitimately matches
// nothing most of the time, so a WARN on every miss here would be noise, not
// signal — see (*Provisioner).ensureTenantBillingAccount and
// accounts.Service's WithBillingPool call site for who logs and why.
func EnsureTenantBillingAccount(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (mapped bool, reason string, err error) {
	var alreadyMapped bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM public.tenant_billing_accounts WHERE tenant_id = $1)
	`, tenantID).Scan(&alreadyMapped); err != nil {
		return false, "", fmt.Errorf("check existing billing mapping: %w", err)
	}
	if alreadyMapped {
		return true, "", nil
	}

	var candidateAccountID *uuid.UUID
	var distinctAccounts, unresolvedMembers int
	if err := pool.QueryRow(ctx, `
		SELECT
			(array_agg(DISTINCT am.account_id) FILTER (WHERE am.account_id IS NOT NULL))[1],
			count(DISTINCT am.account_id) FILTER (WHERE am.account_id IS NOT NULL),
			count(*) FILTER (WHERE am.account_id IS NULL)
		FROM public.tenant_users tu
		LEFT JOIN public.account_memberships am
		  ON am.user_id = tu.user_id
		 AND am.status = 'active'
		WHERE tu.tenant_id = $1
		  AND tu.status = 'ACTIVE'
	`, tenantID).Scan(&candidateAccountID, &distinctAccounts, &unresolvedMembers); err != nil {
		return false, "", fmt.Errorf("resolve billing mapping candidate: %w", err)
	}
	// An ACTIVE tenant_users row with no account_memberships match yet (the
	// LEFT JOIN produced no am row) is invisible to the two counts above, not
	// absent from the tenant. Per review (CodeRabbit, verified independently
	// 2026-07-31): without this check, a multi-member tenant with one
	// resolved member and one not-yet-resolved member reads as
	// distinct_accounts=1 and gets mapped to the resolved member's account.
	// If the unresolved member later provisions a DIFFERENT account, that
	// mapping is already locked in (alreadyMapped short-circuits every future
	// call, on both sides of the pairing) with no way back. Treat any
	// unresolved active member as "not converged yet" regardless of what
	// distinctAccounts currently reads, exactly like the single-member,
	// zero-account case already does.
	if unresolvedMembers > 0 {
		return false, fmt.Sprintf("unresolved_members_pending count=%d", unresolvedMembers), nil
	}
	if distinctAccounts != 1 || candidateAccountID == nil {
		return false, fmt.Sprintf("no_unambiguous_candidate distinct_accounts=%d", distinctAccounts), nil
	}

	tag, err := pool.Exec(ctx, `
		INSERT INTO public.tenant_billing_accounts (tenant_id, account_id)
		SELECT $1, $2
		WHERE NOT EXISTS (
			SELECT 1 FROM public.tenant_billing_accounts existing WHERE existing.account_id = $2
		)
		ON CONFLICT (tenant_id) DO NOTHING
	`, tenantID, *candidateAccountID)
	if err != nil {
		return false, "", fmt.Errorf("insert billing mapping: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, fmt.Sprintf("account_already_claimed_by_another_tenant account=%s", *candidateAccountID), nil
	}
	return true, "", nil
}
