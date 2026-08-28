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
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncTenantMembershipRole propagates accountRole (an
// accounts.RoleOwner/accounts.RoleMember value) for userID onto the
// public.tenant_users row for the tenant public.tenant_billing_accounts maps
// accountID to.
//
// accountID resolving to no tenant, or userID holding no tenant_users row on
// the resolved tenant, are both ordinary, non-fatal, transitional states --
// mirroring EnsureTenantBillingAccount's own contract -- reported via reason
// rather than err, so the caller can log without treating them as a fault:
//
//   - reason == "unmapped_account": no public.tenant_billing_accounts row for
//     accountID yet. Common for a tenant whose billing mapping has not
//     converged (see EnsureTenantBillingAccount's doc for when that
//     resolves), and for any non-HIVE_CLOUD deployment that never maps at
//     all.
//   - reason == "no_tenant_membership_row": accountID resolved to a tenant,
//     but userID has no row in public.tenant_users for it (a race with
//     signup provisioning, or a membership predating it). Nothing to sync
//     yet.
//
// err is reserved for a genuine, unexpected database fault.
func SyncTenantMembershipRole(ctx context.Context, pool *pgxpool.Pool, accountID, userID uuid.UUID, accountRole string) (synced bool, reason string, err error) {
	var tenantRole string
	switch accountRole {
	case "owner":
		tenantRole = "OWNER"
	case "member":
		tenantRole = "MEMBER"
	default:
		return false, "", fmt.Errorf("signup: sync tenant membership role: unsupported account role %q", accountRole)
	}

	// account_id is UNIQUE on public.tenant_billing_accounts
	// (20260728_01_tenant_billing_account.sql), so this mapping is never
	// ambiguous: at most one tenant funds a given account.
	var tenantID uuid.UUID
	err = pool.QueryRow(ctx, `
		SELECT tenant_id FROM public.tenant_billing_accounts WHERE account_id = $1
	`, accountID).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "unmapped_account", nil
		}
		return false, "", fmt.Errorf("signup: resolve tenant for account: %w", err)
	}

	tag, err := pool.Exec(ctx, `
		UPDATE public.tenant_users
		   SET role = $3
		 WHERE tenant_id = $1 AND user_id = $2
	`, tenantID, userID, tenantRole)
	if err != nil {
		return false, "", fmt.Errorf("signup: sync tenant_users role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, "no_tenant_membership_row", nil
	}
	return true, "", nil
}
