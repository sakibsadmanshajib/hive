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
	GetBillingProfile(ctx context.Context, accountID uuid.UUID) (BillingProfile, error)
	UpsertBillingProfile(ctx context.Context, accountID uuid.UUID, input UpdateBillingProfileInput) error
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

func (r *pgxRepository) GetBillingProfile(ctx context.Context, accountID uuid.UUID) (BillingProfile, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			COALESCE(bp.billing_contact_name, ap.owner_name, ''),
			COALESCE(bp.billing_contact_email, ap.login_email, ''),
			COALESCE(bp.legal_entity_name, ''),
			COALESCE(
				bp.legal_entity_type,
				CASE
					WHEN a.account_type = 'personal' THEN 'individual'
					ELSE 'private_company'
				END
			),
			COALESCE(bp.business_registration_number, ''),
			COALESCE(bp.vat_number, ''),
			COALESCE(bp.tax_id_type, ''),
			COALESCE(bp.tax_id_value, ''),
			COALESCE(bp.country_code, ap.country_code, ''),
			COALESCE(bp.state_region, ap.state_region, '')
		FROM public.accounts a
		JOIN public.account_profiles ap ON ap.account_id = a.id
		LEFT JOIN public.account_billing_profiles bp ON bp.account_id = a.id
		WHERE a.id = $1
	`, accountID)

	var profile BillingProfile
	if err := row.Scan(
		&profile.BillingContactName,
		&profile.BillingContactEmail,
		&profile.LegalEntityName,
		&profile.LegalEntityType,
		&profile.BusinessRegistrationNumber,
		&profile.VATNumber,
		&profile.TaxIDType,
		&profile.TaxIDValue,
		&profile.CountryCode,
		&profile.StateRegion,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BillingProfile{}, ErrNotFound
		}
		return BillingProfile{}, err
	}

	return profile, nil
}

func (r *pgxRepository) UpsertBillingProfile(ctx context.Context, accountID uuid.UUID, input UpdateBillingProfileInput) error {
	commandTag, err := r.pool.Exec(ctx, `
		INSERT INTO public.account_billing_profiles (
			account_id,
			billing_contact_name,
			billing_contact_email,
			legal_entity_name,
			legal_entity_type,
			business_registration_number,
			vat_number,
			tax_id_type,
			tax_id_value,
			country_code,
			state_region,
			created_at,
			updated_at
		)
		VALUES (
			$1,
			NULLIF($2, ''),
			NULLIF($3, ''),
			NULLIF($4, ''),
			$5,
			NULLIF($6, ''),
			NULLIF($7, ''),
			NULLIF($8, ''),
			NULLIF($9, ''),
			NULLIF($10, ''),
			NULLIF($11, ''),
			now(),
			now()
		)
		ON CONFLICT (account_id) DO UPDATE
		SET billing_contact_name = NULLIF(EXCLUDED.billing_contact_name, ''),
		    billing_contact_email = NULLIF(EXCLUDED.billing_contact_email, ''),
		    legal_entity_name = NULLIF(EXCLUDED.legal_entity_name, ''),
		    legal_entity_type = EXCLUDED.legal_entity_type,
		    business_registration_number = NULLIF(EXCLUDED.business_registration_number, ''),
		    vat_number = NULLIF(EXCLUDED.vat_number, ''),
		    tax_id_type = NULLIF(EXCLUDED.tax_id_type, ''),
		    tax_id_value = NULLIF(EXCLUDED.tax_id_value, ''),
		    country_code = NULLIF(EXCLUDED.country_code, ''),
		    state_region = NULLIF(EXCLUDED.state_region, ''),
		    updated_at = now()
	`, accountID,
		input.BillingContactName,
		input.BillingContactEmail,
		input.LegalEntityName,
		input.LegalEntityType,
		input.BusinessRegistrationNumber,
		input.VATNumber,
		input.TaxIDType,
		input.TaxIDValue,
		input.CountryCode,
		input.StateRegion,
	)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}
