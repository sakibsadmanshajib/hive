package accounts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
)

// UNIQUE (account_id, user_id) is what arbitrates two concurrent first-time
// acceptances, so the second insert has to come back as a truthful
// already-a-member rather than as raw pgx text the handler answers with a 500.
func TestCreateMembership_DuplicateSeatIsAlreadyMember(t *testing.T) {
	pool := newMembershipOrderTestPool(t)
	repo := accounts.NewPgxRepository(pool)
	ctx := context.Background()

	var userID, accountID uuid.UUID
	marker := "dup-seat-" + uuid.NewString()
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

	seat := accounts.Membership{
		ID: uuid.New(), AccountID: accountID, UserID: userID,
		Role: accounts.RoleMember, Status: accounts.StatusActive,
	}
	if err := repo.CreateMembership(ctx, seat); err != nil {
		t.Fatalf("first CreateMembership: %v", err)
	}
	seat.ID = uuid.New()
	if err := repo.CreateMembership(ctx, seat); !errors.Is(err, accounts.ErrAlreadyMember) {
		t.Fatalf("second CreateMembership = %v, want ErrAlreadyMember", err)
	}
}

// AcceptInvitation must consume an invitation exactly once. Without the
// accepted_at IS NULL predicate a double-clicked link updates the row twice,
// and the service cannot tell the winner of that race from the loser.
func TestAcceptInvitation_ConsumesAnInvitationOnce(t *testing.T) {
	pool := newMembershipOrderTestPool(t)
	repo := accounts.NewPgxRepository(pool)
	ctx := context.Background()

	var inviterID, accountID uuid.UUID
	marker := "consume-once-" + uuid.NewString()
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		marker+"@example.test").Scan(&inviterID); err != nil {
		t.Fatalf("insert auth user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, inviterID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, $1, 'business', $2) RETURNING id`,
		marker, inviterID).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE id = $1`, accountID) })

	inv := accounts.Invitation{
		ID:              uuid.New(),
		AccountID:       accountID,
		Email:           marker + "@invitee.test",
		Role:            accounts.RoleMember,
		TokenHash:       accounts.HashToken(marker),
		ExpiresAt:       time.Now().Add(time.Hour),
		InvitedByUserID: inviterID,
	}
	if err := repo.CreateInvitation(ctx, inv); err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}

	if err := repo.AcceptInvitation(ctx, inv.ID, time.Now()); err != nil {
		t.Fatalf("first AcceptInvitation: %v", err)
	}
	if err := repo.AcceptInvitation(ctx, inv.ID, time.Now()); !errors.Is(err, accounts.ErrAlreadyAccepted) {
		t.Fatalf("second AcceptInvitation error = %v, want ErrAlreadyAccepted", err)
	}
}

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

	// Replayed acceptance, which is what the loser of a double-click race runs:
	// it must succeed, because the seat really is active, and it must not
	// re-stamp the role of a member who is already in.
	if err := repo.ActivateMembership(ctx, accountID, userID, accounts.RoleMember); err != nil {
		t.Fatalf("replayed ActivateMembership: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.account_memberships WHERE account_id = $1 AND user_id = $2`,
		accountID, userID).Scan(&role); err != nil {
		t.Fatalf("re-read membership: %v", err)
	}
	if role != accounts.RoleOwner {
		t.Fatalf("role after replayed activation = %s, want it unchanged", role)
	}

	// No membership row at all is a genuine miss, and the caller has to be able
	// to tell that apart from the replay above.
	if err := repo.ActivateMembership(ctx, accountID, uuid.New(), accounts.RoleMember); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("ActivateMembership for a stranger = %v, want ErrNotFound", err)
	}
}
