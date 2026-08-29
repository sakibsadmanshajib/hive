package signup

// Tenant-role propagation (issue #1245).
//
// public.tenant_users.role used to be written exactly once, at signup or
// reconciliation time (Provisioner.provision, insertPersonalMembership), and
// never touched again: grep the repository before this file and there is no
// UPDATE public.tenant_users statement anywhere. Everything that decides
// workspace-administrator authority off it --
// platform.TenantRoleService.IsTenantOwner, and through it
// platform.WorkspaceAdminGate plus egress.Service's owner-gated write path --
// therefore kept honouring whatever role a user held the moment they were
// first added to a tenant, even after accounts.Service.UpdateMemberRole
// (the Members page) explicitly demoted them on public.account_memberships,
// the table that IS kept current. A demoted owner's session, or a still-valid
// API key, retained backend-enforced owner authority indefinitely.
//
// SyncTenantMembershipRole is the fix: the one and only writer of
// public.tenant_users.role after signup, called from
// accounts.Service.UpdateMemberRole every time an account_memberships role
// actually changes. account_memberships stays the authoritative store (it is
// the only one any live product flow ever writes); this function keeps
// tenant_users.role from drifting away from it rather than trying to make
// tenant_users a second source of truth WorkspaceAdminGate reads through a
// new join. See the accounts.Service.UpdateMemberRole call site for why a
// read-side join was rejected: public.tenant_users deliberately hardcodes
// 'MEMBER' for every personal (single-member) tenant's sole user even though
// that user owns their own account (personal_tenant.go,
// insertPersonalMembership), specifically so a personal tenant never reaches
// WorkspaceAdminGate's feature-gate/marketplace admin surfaces. A read-side
// join through account_memberships would have silently granted every
// self-serve Hive Cloud signup that access.
//
// Syncing on write does not get that protection for free either, which is why
// the promotion arm of the statement below is gated on
// public.tenants.personal_owner_user_id IS NULL. An earlier revision of this
// file argued the gate was unnecessary because UpdateMemberRole always
// rejects a role change against a single-member account (ErrLastOwner or
// ErrSelfRoleChange). That premise is false: nothing keeps a personal
// account single-member. Service.CreateInvitation has no account_type guard
// and 20260727_02 widened public.account_invitations.role to include 'owner',
// so the account's own user can invite a second identity as a co-owner, and
// that co-owner can then promote the original user through the public
// Members-page endpoint. Without the gate, that sequence writes 'OWNER' onto
// the personal tenant's row and hands the exact feature-gate, marketplace and
// egress authority the 'MEMBER' hardcode exists to withhold. The gate is on
// the promotion arm only: demotion stays unconditional, because removing
// authority is always the safe direction.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncTenantMembershipRole propagates userID's CURRENT
// public.account_memberships.role on accountID onto the public.tenant_users
// row for the tenant public.tenant_billing_accounts maps accountID to.
//
// Deliberately does not take the caller's requested role as a parameter and
// write it directly: accounts.Service.UpdateMemberRole already committed the
// account_memberships write before calling this, so re-reading it fresh,
// inside the same UPDATE statement that writes tenant_users, NARROWS the
// reordering window between two concurrent UpdateMemberRole calls for the
// same user. Two overlapping calls (a promote and a demote for the same
// account_memberships row, however unlikely) can commit their
// account_memberships writes in either order; whichever commits LAST is,
// correctly, the final account_memberships value. If this function instead
// trusted its own caller's newRole parameter, the two resulting
// tenant_users writes could themselves be reordered independently of that,
// and the loser could leave tenant_users disagreeing with the
// account_memberships row that actually won -- reproducing exactly the bug
// this function exists to close (issue #1245).
//
// Narrows, not closes. Do not read the paragraph above as a claim of
// race-safety. The read of account_memberships is not serialized against the
// write to tenant_users: under READ COMMITTED this statement takes its
// snapshot when it starts, so a promoting call that reads am.role = 'owner',
// is then overtaken by a demoting call that commits both its
// account_memberships write and its own sync, can still land its 'OWNER' on
// tenant_users afterwards and leave the two tables disagreeing until the next
// role change. Closing it needs the two writes in one transaction, which is
// what issue #1295 proposes and why that issue is not merely an atomicity
// nicety. A cheaper partial close, if #1295 stalls, is SELECT ... FOR UPDATE
// on the account_memberships row inside this statement.
//
// A single UPDATE ... FROM, not a SELECT then an UPDATE: one round trip, and
// no separate "resolve the tenant" step to race against a mapping that
// changes between two queries (it does not, tenant_billing_accounts rows are
// never reassigned, but there is no reason to take two round trips for what
// one accomplishes).
//
// The CASE moves the OWNER bit and nothing else. public.tenant_users.role has
// a four-value domain (OWNER, ADMIN, MEMBER, VIEWER -- the CHECK in
// 20260516_03_phase19_tenant_users.sql), while public.account_memberships.role
// has two (accounts.NormalizeRole accepts only owner and member), so a naive
// CASE am.role WHEN 'owner' THEN 'OWNER' ELSE 'MEMBER' END would flatten the
// larger domain onto the smaller one on every billing role change: ADMIN would
// silently lose the INSERT/UPDATE/DELETE grant the tenant_users RLS policies
// give role IN ('OWNER','ADMIN'), and VIEWER, a genuinely lower tier that
// public.owui_role passes through unchanged (20260823_03) and Open WebUI's
// OAUTH_ALLOWED_ROLES lists separately, would be silently WIDENED to MEMBER.
// Neither tier is written by a live product path today, but both are reachable
// through PostgREST for a tenant OWNER or ADMIN, so this statement declines to
// have an opinion about tiers it was never told about: ELSE tu.role.
//
// Two guards on the promotion arm, neither on the demotion arm:
// personal_owner_user_id IS NULL (see the package comment above), and
// tu.status = 'ACTIVE'. The status guard exists because suspension is how a
// tenant removes a member's authority without deleting the row, and stamping
// OWNER onto a SUSPENDED or INVITED row would restore an owner the tenant
// never granted the moment anything reactivates it (reconcile.go's insert is
// ON CONFLICT DO NOTHING and will not correct the role on an existing row).
// The demotion arm is deliberately left unguarded rather than moved into the
// WHERE clause as the backfill migration does it: a WHERE-side status filter
// would also stop a stale OWNER on a suspended row from ever being demoted,
// which is the unsafe half of the same trade.
//
// RowsAffected() == 0 covers three ordinary, non-fatal, transitional states
// at once, all reported as reason == "no_match" rather than err: accountID
// has no public.tenant_billing_accounts row yet (mapping has not converged,
// or a non-HIVE_CLOUD deployment that never maps at all -- see
// EnsureTenantBillingAccount's doc), userID has no ACTIVE row in
// public.account_memberships for accountID (an invited-not-yet-accepted seat
// grants nothing, matching every other account_memberships read in this
// codebase -- accounts/repository.go's activeMemberships and this file's own
// backfill migration both apply the same predicate; should not happen for a
// caller that just updated an active row, but is not this function's job to
// assume), or userID has no public.tenant_users row on the resolved tenant
// (a race with signup provisioning). err is reserved for a genuine,
// unexpected database fault.
//
// One string for three states is a real operator limitation, called out here
// rather than papered over: reason == "no_match" in a log line does not say
// whether an unmapped tenant was skipped harmlessly or a demotion failed to
// land, and those are not equally serious. Splitting them costs a second
// query on a path that runs once per Members-page role change, so it is not
// done here; an operator chasing a specific user reads the three predicates
// above directly. Note that the demotion direction cannot silently no-op
// through the arm this function controls: whenever the row is found at all,
// a tenant_users OWNER whose account_memberships row is not an active owner
// is demoted regardless of tenant type or tenant_users status.
//
// Issue #896: this statement joins public.account_memberships (am), which
// carries its own hive_app RLS policy since
// 20260829_04_account_memberships_hive_app_scope.sql. That policy is an OR
// of two session-variable-scoped predicates (app.current_actor_user_id,
// app.current_account_id -- see accounts.pgxRepository's withActorTx /
// withAccountTx doc comments for the full shape enumeration); this
// statement's own WHERE/ON clauses already pin the row it needs to exactly
// one (account_id, user_id) pair, which is BOTH shapes at once, so both
// variables are set before running it. Without this, the am join would be
// RLS-invisible regardless of whether a real active membership row exists,
// the INNER JOIN would produce zero rows for every call, and every
// promotion AND demotion this function exists to perform (issue #1245)
// would silently report reason == "no_match" -- indistinguishable from the
// three genuinely benign no-match cases this function's doc already
// documents. LOCAL scope inside an explicit transaction is required, not
// incidental: see egress/repository.go's withTenantTx comment for why a
// bare Exec then a separate statement loses it.
func SyncTenantMembershipRole(ctx context.Context, pool *pgxpool.Pool, accountID, userID uuid.UUID) (synced bool, reason string, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, "", fmt.Errorf("signup: begin tenant role sync tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_account_id', $1, true)", accountID.String()); err != nil {
		return false, "", fmt.Errorf("signup: set account scope: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_actor_user_id', $1, true)", userID.String()); err != nil {
		return false, "", fmt.Errorf("signup: set actor scope: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE public.tenant_users tu
		   SET role = CASE
		                WHEN am.role = 'owner'
		                 AND t.personal_owner_user_id IS NULL
		                 AND tu.status = 'ACTIVE'  THEN 'OWNER'
		                WHEN tu.role = 'OWNER'     THEN 'MEMBER'
		                ELSE tu.role
		              END
		  FROM public.tenant_billing_accounts tba
		  JOIN public.tenants t
		    ON t.id = tba.tenant_id
		  JOIN public.account_memberships am
		    ON am.account_id = tba.account_id
		   AND am.user_id    = $2
		   AND am.status     = 'active'
		 WHERE tba.account_id = $1
		   AND tu.tenant_id   = tba.tenant_id
		   AND tu.user_id     = $2
	`, accountID, userID)
	if err != nil {
		return false, "", fmt.Errorf("signup: sync tenant_users role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, "no_match", nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", fmt.Errorf("signup: commit tenant role sync tx: %w", err)
	}
	return true, "", nil
}
