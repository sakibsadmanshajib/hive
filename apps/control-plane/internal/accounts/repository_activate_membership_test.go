package accounts_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
)

// ActivateMembership is the write that stops the escalation fix from turning
// into a lockout: an invited row confers nothing, so acceptance has to be able
// to promote it. The statement runs against the real schema here because its
// whole behaviour is in its WHERE clause.
func TestActivateMembership_PromotesOnlyAnInvitedRow(t *testing.T) {
	pool := newMembershipOrderTestPool(t)
	repo := accounts.NewPgxRepository(pool)
	ctx := context.Background()

	var userID, accountID uuid.UUID
	marker := "activate-" + uuid.NewString()
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		marker+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert auth user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, userID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, $1, 'business', $2) RETURNING id`,
		marker, userID).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE id = $1`, accountID) })

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.account_memberships(id, account_id, user_id, role, status)
		 VALUES (gen_random_uuid(), $1, $2, 'member', 'invited')`,
		accountID, userID); err != nil {
		t.Fatalf("insert invited membership: %v", err)
	}

	if err := repo.ActivateMembership(ctx, accountID, userID, accounts.RoleOwner); err != nil {
		t.Fatalf("ActivateMembership: %v", err)
	}

	var role, status string
	if err := pool.QueryRow(ctx,
		`SELECT role, status FROM public.account_memberships WHERE account_id = $1 AND user_id = $2`,
		accountID, userID).Scan(&role, &status); err != nil {
		t.Fatalf("read membership: %v", err)
	}
	if status != accounts.StatusActive || role != accounts.RoleOwner {
		t.Fatalf("membership after activation = (%s, %s), want (owner, active)", role, status)
	}

	// Already active: nothing pending to accept, and the role must not be
	// re-stamped by a replayed acceptance.
	if err := repo.ActivateMembership(ctx, accountID, userID, accounts.RoleMember); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("second ActivateMembership error = %v, want ErrNotFound", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.account_memberships WHERE account_id = $1 AND user_id = $2`,
		accountID, userID).Scan(&role); err != nil {
		t.Fatalf("re-read membership: %v", err)
	}
	if role != accounts.RoleOwner {
		t.Fatalf("role after replayed activation = %s, want it unchanged", role)
	}
}
