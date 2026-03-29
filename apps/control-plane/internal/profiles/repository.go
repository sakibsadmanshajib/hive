package profiles

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines the data-access interface for account profiles.
type Repository interface {
	GetAccountProfile(ctx context.Context, accountID uuid.UUID) (AccountProfile, error)
	UpdateAccountProfile(ctx context.Context, accountID uuid.UUID, input UpdateAccountProfileInput, profileSetupComplete bool) error
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository returns a Repository backed by the given pgx pool.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) GetAccountProfile(ctx context.Context, accountID uuid.UUID) (AccountProfile, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT ap.owner_name, ap.login_email, a.display_name, a.account_type,
		       COALESCE(ap.country_code, ''), COALESCE(ap.state_region, ''), ap.profile_setup_complete
		FROM public.account_profiles ap
		JOIN public.accounts a ON a.id = ap.account_id
		WHERE ap.account_id = $1
	`, accountID)

	var profile AccountProfile
	if err := row.Scan(
		&profile.OwnerName,
		&profile.LoginEmail,
		&profile.DisplayName,
		&profile.AccountType,
		&profile.CountryCode,
		&profile.StateRegion,
		&profile.ProfileSetupComplete,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AccountProfile{}, ErrNotFound
		}
		return AccountProfile{}, err
	}

	return profile, nil
}

func (r *pgxRepository) UpdateAccountProfile(ctx context.Context, accountID uuid.UUID, input UpdateAccountProfileInput, profileSetupComplete bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	accountResult, err := tx.Exec(ctx, `
		UPDATE public.accounts
		SET display_name = $2, account_type = $3, updated_at = now()
		WHERE id = $1
	`, accountID, input.DisplayName, input.AccountType)
	if err != nil {
		return err
	}
	if accountResult.RowsAffected() == 0 {
		return ErrNotFound
	}

	profileResult, err := tx.Exec(ctx, `
		UPDATE public.account_profiles
		SET owner_name = $2,
		    login_email = $3,
		    country_code = $4,
		    state_region = $5,
		    profile_setup_complete = $6,
		    updated_at = now()
		WHERE account_id = $1
	`, accountID, input.OwnerName, input.LoginEmail, input.CountryCode, input.StateRegion, profileSetupComplete)
	if err != nil {
		return err
	}
	if profileResult.RowsAffected() == 0 {
		return ErrNotFound
	}

	return tx.Commit(ctx)
}
