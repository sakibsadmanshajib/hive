package accounts

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines the data-access interface for accounts.
// The concrete pgxRepository uses pgx/v5 against Supabase Postgres.
type Repository interface {
	ListMembershipsByUserID(ctx context.Context, userID uuid.UUID) ([]Membership, error)
	CreateAccount(ctx context.Context, acct Account) error
	CreateMembership(ctx context.Context, m Membership) error
	CreateProfile(ctx context.Context, p AccountProfile) error
	GetAccountByID(ctx context.Context, id uuid.UUID) (*Account, error)
	CreateInvitation(ctx context.Context, inv Invitation) error
	FindInvitationByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error)
	AcceptInvitation(ctx context.Context, invitationID uuid.UUID, acceptedAt time.Time) error
	ListMembersByAccountID(ctx context.Context, accountID uuid.UUID) ([]Member, error)
}

// pgxRepository is the production implementation backed by Supabase Postgres.
type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository returns a Repository backed by the given pgx pool.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) ListMembershipsByUserID(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, user_id, role, status, created_at
		FROM public.account_memberships
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memberships []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ID, &m.AccountID, &m.UserID, &m.Role, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		memberships = append(memberships, m)
	}
	return memberships, rows.Err()
}

func (r *pgxRepository) CreateAccount(ctx context.Context, acct Account) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		VALUES ($1, $2, $3, $4, $5)
	`, acct.ID, acct.Slug, acct.DisplayName, acct.AccountType, acct.OwnerUserID)
	return err
}

func (r *pgxRepository) CreateMembership(ctx context.Context, m Membership) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.account_memberships (id, account_id, user_id, role, status)
		VALUES ($1, $2, $3, $4, $5)
	`, m.ID, m.AccountID, m.UserID, m.Role, m.Status)
	return err
}

func (r *pgxRepository) CreateProfile(ctx context.Context, p AccountProfile) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.account_profiles (account_id, owner_name, login_email, profile_setup_complete)
		VALUES ($1, $2, $3, $4)
	`, p.AccountID, p.OwnerName, p.LoginEmail, p.ProfileSetupComplete)
	return err
}

func (r *pgxRepository) GetAccountByID(ctx context.Context, id uuid.UUID) (*Account, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, account_type, owner_user_id, created_at, updated_at
		FROM public.accounts
		WHERE id = $1
	`, id)

	var a Account
	if err := row.Scan(&a.ID, &a.Slug, &a.DisplayName, &a.AccountType, &a.OwnerUserID, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, ErrNotFound
	}
	return &a, nil
}

func (r *pgxRepository) CreateInvitation(ctx context.Context, inv Invitation) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.account_invitations
		  (id, account_id, email, role, token_hash, expires_at, invited_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, inv.ID, inv.AccountID, inv.Email, inv.Role, inv.TokenHash, inv.ExpiresAt, inv.InvitedByUserID)
	return err
}

func (r *pgxRepository) FindInvitationByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, email, role, token_hash, expires_at, accepted_at, invited_by_user_id, created_at
		FROM public.account_invitations
		WHERE token_hash = $1
	`, tokenHash)

	var inv Invitation
	if err := row.Scan(&inv.ID, &inv.AccountID, &inv.Email, &inv.Role, &inv.TokenHash,
		&inv.ExpiresAt, &inv.AcceptedAt, &inv.InvitedByUserID, &inv.CreatedAt); err != nil {
		return nil, ErrNotFound
	}
	return &inv, nil
}

func (r *pgxRepository) AcceptInvitation(ctx context.Context, invitationID uuid.UUID, acceptedAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE public.account_invitations
		SET accepted_at = $1
		WHERE id = $2
	`, acceptedAt, invitationID)
	return err
}

func (r *pgxRepository) ListMembersByAccountID(ctx context.Context, accountID uuid.UUID) ([]Member, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, role, status
		FROM public.account_memberships
		WHERE account_id = $1
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Role, &m.Status); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
