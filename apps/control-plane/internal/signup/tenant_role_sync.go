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
// self-serve Hive Cloud signup that access. Syncing on write, from the one
// call site that ever changes account_memberships.role after signup, cannot
// touch a personal tenant's row at all: UpdateMemberRole always rejects a
// role change against a single-member account (ErrLastOwner or
// ErrSelfRoleChange), so this function is simply never invoked for one.

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
// inside the same UPDATE statement that writes tenant_users, is what makes
// this call race-safe against a second, concurrent UpdateMemberRole call for
// the same user. Two overlapping calls (a promote and a demote for the same
// account_memberships row, however unlikely) can commit their
// account_memberships writes in either order; whichever commits LAST is,
// correctly, the final account_memberships value. If this function instead
// trusted its own caller's newRole parameter, the two resulting
// tenant_users writes could themselves be reordered independently of that,
// and the loser could leave tenant_users disagreeing with the
// account_memberships row that actually won -- reproducing a narrow window
// of exactly the bug this function exists to close (issue #1245). Reading
// account_memberships fresh inside the UPDATE means every call converges on
// whatever account_memberships currently says at the moment it runs, not
// what its own caller asked for.
//
// A single UPDATE ... FROM, not a SELECT then an UPDATE: one round trip, and
// no separate "resolve the tenant" step to race against a mapping that
// changes between two queries (it does not, tenant_billing_accounts rows are
// never reassigned, but there is no reason to take two round trips for what
// one accomplishes).
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
func SyncTenantMembershipRole(ctx context.Context, pool *pgxpool.Pool, accountID, userID uuid.UUID) (synced bool, reason string, err error) {
	tag, err := pool.Exec(ctx, `
		UPDATE public.tenant_users tu
		   SET role = CASE am.role WHEN 'owner' THEN 'OWNER' ELSE 'MEMBER' END
		  FROM public.tenant_billing_accounts tba
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
	return true, "", nil
}
