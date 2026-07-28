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
// Reconcile never creates a tenant. It only attaches a user to a tenant that
// already claims them, by unconsumed invite token or by verified email domain,
// which is the behaviour Resolver has always implemented. See the Reconcile
// doc comment for why that is the right posture in both deployment modes.

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"

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
// Deployment posture. Reconcile does not auto-create a tenant in either mode,
// so it needs no posture branch. That is not a simplification, it is the
// existing behaviour of this package: Resolver has only ever performed
// read-only lookups against tenant_invites and tenant_email_domains, and the
// sole production writer of public.tenants is the operator-run
// scripts/seed-demo-owner.py. Attaching only to a tenant that already claims
// the user is the safe default in the customer-hosted posture, where
// membership is administered, and it is unchanged from what Hive Cloud does
// today. Should self-serve tenant creation ever be wanted in the hosted
// posture only, the existing switch to branch on is
// platform/config.Config.LicenseFilePath, which already selects
// licensing.FileSource (Hive Enterprise) over licensing.CloudSource (Hive
// Cloud); no new flag is needed for that either.
//
// Concurrency. Two callers racing for the same user both fall through the
// short-circuit, both resolve to the same tenant because resolution is a pure
// function of the invite token and the email domain, and both insert. The
// tenant_users primary key (tenant_id, user_id) plus ON CONFLICT DO NOTHING
// makes the second insert a no-op, so the outcome is exactly one membership.
// No tenant is created on either path, so no race can produce two tenants.
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

// ensureTenantBillingAccount maps tenantID to a billing account in
// public.tenant_billing_accounts, the table introduced by migration
// 20260728_01_tenant_billing_account.sql (vault
// spec-2026-07-28-backend-metering.md section 3.2). It is deliberately
// conservative and mirrors the one-time backfill migration
// (20260728_03_tenant_billing_account_backfill.sql) rather than inventing a
// second rule: a mapping is only ever created when it is unambiguous, never
// guessed.
//
// A mapping is resolvable here in exactly one case: tenantID has no mapping
// yet, and the member just reconciled is its only ACTIVE member, and that
// member holds exactly one ACTIVE account membership. That combination can
// only occur the first time anyone is reconciled into a brand new tenant, so
// "their one account becomes the tenant's account" is a fact about the data,
// not a per-request policy choice, and it never runs again once a tenant has
// a second member. Per D-005, this function does NOT fall back to picking an
// account by owner_user_id, and does NOT create a new account: an
// unresolvable tenant is left unmapped, exactly like the backfill migration
// leaves ambiguous tenants unmapped, and surfaces the same way (the metering
// gate's fail-closed 403 once that ships, not silently here).
func (p *Provisioner) ensureTenantBillingAccount(ctx context.Context, tenantID uuid.UUID) error {
	var alreadyMapped bool
	if err := p.deps.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM public.tenant_billing_accounts WHERE tenant_id = $1)
	`, tenantID).Scan(&alreadyMapped); err != nil {
		return fmt.Errorf("check existing billing mapping: %w", err)
	}
	if alreadyMapped {
		return nil
	}

	_, err := p.deps.Pool.Exec(ctx, `
		INSERT INTO public.tenant_billing_accounts (tenant_id, account_id)
		SELECT candidate.tenant_id, candidate.account_id
		FROM (
			SELECT
				t.id AS tenant_id,
				(array_agg(DISTINCT am.account_id) FILTER (WHERE am.account_id IS NOT NULL))[1] AS account_id,
				count(DISTINCT tu.user_id) AS distinct_members,
				count(DISTINCT am.account_id) FILTER (WHERE am.account_id IS NOT NULL) AS distinct_accounts
			FROM public.tenants t
			JOIN public.tenant_users tu
			  ON tu.tenant_id = t.id
			 AND tu.status = 'ACTIVE'
			LEFT JOIN public.account_memberships am
			  ON am.user_id = tu.user_id
			 AND am.status = 'active'
			WHERE t.id = $1
			GROUP BY t.id
		) candidate
		WHERE candidate.distinct_members = 1
		  AND candidate.distinct_accounts = 1
		  AND NOT EXISTS (
		      SELECT 1 FROM public.tenant_billing_accounts existing
		       WHERE existing.account_id = candidate.account_id
		  )
		ON CONFLICT (tenant_id) DO NOTHING
	`, tenantID)
	if err != nil {
		return fmt.Errorf("insert billing mapping: %w", err)
	}
	return nil
}
