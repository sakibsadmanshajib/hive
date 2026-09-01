package accounts_test

// Regression guard for the writer half of issue #1646, found by that PR's own
// security review: accounts.Service.AcceptInvitation writes
// public.account_memberships (through ActivateMembership or CreateMembership)
// and, until this change, never propagated onto public.tenant_users.
//
// That matters because owner invitations are a shipped feature
// (20260727_02_account_invitations_owner_role.sql widened
// account_invitations.role for issue #536) and every signup path inserts
// 'MEMBER' into tenant_users. So "invite a co-owner, they accept" produced an
// account owner that platform.WorkspaceAdminGate answers 403 to, which is the
// exact defect #1245's fix on UpdateMemberRole was believed to have closed. It
// was not history: it was being manufactured on current code, and two of the
// three rows the backfill migration promotes on the demo box are of this shape.
//
// The assertion that matters is IsTenantOwner, the function the gate actually
// calls, not the column value on its own.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// invitationFixture seeds a business tenant mapped to one account whose owner
// has invited inviteeEmail with invitationRole, and a tenant_users row for the
// invitee carrying the 'MEMBER' signup writes. The invitee deliberately has NO
// account_memberships row, so acceptance takes CreateMembership rather than
// ActivateMembership; the invited-seat path is covered by the second test.
func invitationFixture(t *testing.T, pool *pgxpool.Pool, invitationRole string, seatPending bool) (
	tenantID, inviteeID uuid.UUID, inviteeEmail, rawToken string,
) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	accountID := uuid.New()
	tenantID = uuid.New()
	inviterID := uuid.New()
	inviteeID = uuid.New()
	inviteeEmail = "invite-sync-invitee-" + suffix + "@example.test"
	rawToken = "invite-sync-token-" + suffix

	for _, u := range []struct {
		id    uuid.UUID
		email string
	}{
		{inviterID, "invite-sync-inviter-" + suffix + "@example.test"},
		{inviteeID, inviteeEmail},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES ($1, $2, '{}'::jsonb)`,
			u.id, u.email); err != nil {
			t.Fatalf("seed auth user %s: %v", u.email, err)
		}
		id := u.id
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, id) })
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
		 VALUES ($1, $2, 'Invitation Sync Test', 'business', $3)`,
		accountID, "invite-sync-account-"+suffix, inviterID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id = $1`, accountID) })
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.account_memberships WHERE account_id = $1`, accountID)
	})
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.account_invitations WHERE account_id = $1`, accountID)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.account_memberships(id, account_id, user_id, role, status)
		 VALUES ($1, $2, $3, 'owner', 'active')`,
		uuid.New(), accountID, inviterID); err != nil {
		t.Fatalf("seed inviter membership: %v", err)
	}
	if seatPending {
		// The seat written ahead of acceptance: confers nothing until the
		// invitation is redeemed, and takes the ActivateMembership arm.
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.account_memberships(id, account_id, user_id, role, status)
			 VALUES ($1, $2, $3, 'member', 'invited')`,
			uuid.New(), accountID, inviteeID); err != nil {
			t.Fatalf("seed invited seat: %v", err)
		}
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenants(id, slug, name, deployment) VALUES ($1, $2, $2, 'HIVE_CLOUD')`,
		tenantID, "invite-sync-tenant-"+suffix); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, tenantID) })
	// tenant_billing_accounts.account_id is ON DELETE RESTRICT, so this cleanup
	// must run before the accounts one; t.Cleanup is LIFO, so it is registered
	// after it.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_billing_accounts(tenant_id, account_id) VALUES ($1, $2)`,
		tenantID, accountID); err != nil {
		t.Fatalf("seed tenant_billing_accounts: %v", err)
	}

	// What signup leaves behind: reconcile.go's provision inserts this literal
	// for every path, and nothing updated it afterwards.
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
		 VALUES ($1, $2, 'MEMBER', 'ACTIVE')`,
		tenantID, inviteeID); err != nil {
		t.Fatalf("seed tenant_users: %v", err)
	}

	inv := accounts.Invitation{
		ID:              uuid.New(),
		AccountID:       accountID,
		Email:           inviteeEmail,
		Role:            invitationRole,
		TokenHash:       accounts.HashToken(rawToken),
		ExpiresAt:       time.Now().Add(time.Hour),
		InvitedByUserID: inviterID,
	}
	if _, err := accounts.NewPgxRepository(pool).CreateInvitation(ctx, inv); err != nil {
		t.Fatalf("seed invitation: %v", err)
	}

	return tenantID, inviteeID, inviteeEmail, rawToken
}

// Accepting an OWNER invitation must reach public.tenant_users. Without the
// syncTenantRole call in AcceptInvitation this fails: the membership is owner,
// the tenant row stays MEMBER, and WorkspaceAdminGate denies a real co-owner.
func TestAcceptInvitation_OwnerSeatReachesTenantUsers(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	tenantID, inviteeID, inviteeEmail, rawToken := invitationFixture(t, pool, accounts.RoleOwner, false)

	svc := accounts.NewService(accounts.NewPgxRepository(pool)).WithBillingPool(pool)
	roleSvc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))

	before, err := roleSvc.IsTenantOwner(ctx, inviteeID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner before acceptance: %v", err)
	}
	if before {
		t.Fatal("fixture is wrong: the invitee must not be a tenant owner before accepting")
	}

	viewer := auth.Viewer{UserID: inviteeID, Email: inviteeEmail, EmailVerified: true}
	if _, err := svc.AcceptInvitation(ctx, viewer, rawToken); err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}

	var tenantRole string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, inviteeID).Scan(&tenantRole); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if tenantRole != "OWNER" {
		t.Fatalf("tenant_users.role = %q after an accepted owner invitation, want OWNER (issue #1646: the acceptance path still manufactures the divergence)", tenantRole)
	}

	granted, err := roleSvc.IsTenantOwner(ctx, inviteeID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner after acceptance: %v", err)
	}
	if !granted {
		t.Fatal("WorkspaceAdminGate still denies a co-owner who accepted an owner invitation")
	}
}

// The other arm of the same handler: a seat written ahead of acceptance takes
// ActivateMembership rather than CreateMembership, and must propagate too.
func TestAcceptInvitation_ActivatedInvitedSeatReachesTenantUsers(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	tenantID, inviteeID, inviteeEmail, rawToken := invitationFixture(t, pool, accounts.RoleOwner, true)

	svc := accounts.NewService(accounts.NewPgxRepository(pool)).WithBillingPool(pool)

	viewer := auth.Viewer{UserID: inviteeID, Email: inviteeEmail, EmailVerified: true}
	if _, err := svc.AcceptInvitation(ctx, viewer, rawToken); err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}

	var tenantRole string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, inviteeID).Scan(&tenantRole); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if tenantRole != "OWNER" {
		t.Fatalf("tenant_users.role = %q after activating an invited owner seat, want OWNER", tenantRole)
	}
}

// A member invitation must NOT reach OWNER. The sync re-reads
// account_memberships rather than trusting a caller's parameter, so this is the
// assertion that the new call site cannot become a promotion in disguise.
func TestAcceptInvitation_MemberSeatDoesNotBecomeTenantOwner(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	tenantID, inviteeID, inviteeEmail, rawToken := invitationFixture(t, pool, accounts.RoleMember, false)

	svc := accounts.NewService(accounts.NewPgxRepository(pool)).WithBillingPool(pool)
	roleSvc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))

	viewer := auth.Viewer{UserID: inviteeID, Email: inviteeEmail, EmailVerified: true}
	if _, err := svc.AcceptInvitation(ctx, viewer, rawToken); err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}

	var tenantRole string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, inviteeID).Scan(&tenantRole); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if tenantRole != "MEMBER" {
		t.Fatalf("tenant_users.role = %q after an accepted MEMBER invitation, want MEMBER", tenantRole)
	}

	granted, err := roleSvc.IsTenantOwner(ctx, inviteeID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner: %v", err)
	}
	if granted {
		t.Fatal("accepting a member invitation granted workspace owner authority")
	}
}
