package profiles

import (
	"context"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Service encapsulates all profile business logic.
type Service struct {
	repo Repository
}

var billingIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 /-]{1,63}$`)

// NewService returns a new profiles Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetAccountProfile returns the current-account core profile.
func (s *Service) GetAccountProfile(ctx context.Context, accountID uuid.UUID) (AccountProfile, error) {
	return s.repo.GetAccountProfile(ctx, accountID)
}

// GetBillingProfile returns the current-account billing profile.
func (s *Service) GetBillingProfile(ctx context.Context, accountID uuid.UUID) (BillingProfile, error) {
	return s.repo.GetBillingProfile(ctx, accountID)
}

// UpdateAccountProfile validates and persists the current-account core profile.
func (s *Service) UpdateAccountProfile(ctx context.Context, accountID uuid.UUID, input UpdateAccountProfileInput) (AccountProfile, error) {
	normalized, err := validateUpdateAccountProfileInput(input)
	if err != nil {
		return AccountProfile{}, err
	}

	profileSetupComplete := isProfileSetupComplete(normalized)
	if err := s.repo.UpdateAccountProfile(ctx, accountID, normalized, profileSetupComplete); err != nil {
		return AccountProfile{}, fmt.Errorf("profiles: update account profile: %w", err)
	}

	profile, err := s.repo.GetAccountProfile(ctx, accountID)
	if err != nil {
		return AccountProfile{}, fmt.Errorf("profiles: get updated account profile: %w", err)
	}
	return profile, nil
}

// UpdateBillingProfile validates and persists the current-account billing profile.
func (s *Service) UpdateBillingProfile(ctx context.Context, accountID uuid.UUID, input UpdateBillingProfileInput) (BillingProfile, error) {
	accountProfile, err := s.repo.GetAccountProfile(ctx, accountID)
	if err != nil {
		return BillingProfile{}, fmt.Errorf("profiles: get account profile: %w", err)
	}

	normalized, err := validateUpdateBillingProfileInput(input, accountProfile)
	if err != nil {
		return BillingProfile{}, err
	}

	if err := s.repo.UpsertBillingProfile(ctx, accountID, normalized); err != nil {
		return BillingProfile{}, fmt.Errorf("profiles: upsert billing profile: %w", err)
	}

	profile, err := s.repo.GetBillingProfile(ctx, accountID)
	if err != nil {
		return BillingProfile{}, fmt.Errorf("profiles: get updated billing profile: %w", err)
	}

	return profile, nil
}

func validateUpdateAccountProfileInput(input UpdateAccountProfileInput) (UpdateAccountProfileInput, error) {
	normalized := UpdateAccountProfileInput{
		OwnerName:   strings.TrimSpace(input.OwnerName),
		LoginEmail:  strings.TrimSpace(input.LoginEmail),
		DisplayName: strings.TrimSpace(input.DisplayName),
		AccountType: strings.ToLower(strings.TrimSpace(input.AccountType)),
		CountryCode: strings.TrimSpace(input.CountryCode),
		StateRegion: strings.TrimSpace(input.StateRegion),
	}

	required := []struct {
		field string
		value string
	}{
		{field: "owner_name", value: normalized.OwnerName},
		{field: "login_email", value: normalized.LoginEmail},
		{field: "display_name", value: normalized.DisplayName},
		{field: "account_type", value: normalized.AccountType},
		{field: "country_code", value: normalized.CountryCode},
		{field: "state_region", value: normalized.StateRegion},
	}
	for _, item := range required {
		if item.value == "" {
			return UpdateAccountProfileInput{}, &ValidationError{
				Field:   item.field,
				Message: item.field + " is required",
			}
		}
	}

	if normalized.AccountType != "personal" && normalized.AccountType != "business" {
		return UpdateAccountProfileInput{}, &ValidationError{
			Field:   "account_type",
			Message: "account_type must be personal or business",
		}
	}

	return normalized, nil
}

func validateUpdateBillingProfileInput(input UpdateBillingProfileInput, accountProfile AccountProfile) (UpdateBillingProfileInput, error) {
	normalized := UpdateBillingProfileInput{
		BillingContactName:         strings.TrimSpace(input.BillingContactName),
		BillingContactEmail:        strings.TrimSpace(input.BillingContactEmail),
		LegalEntityName:            strings.TrimSpace(input.LegalEntityName),
		LegalEntityType:            strings.ToLower(strings.TrimSpace(input.LegalEntityType)),
		BusinessRegistrationNumber: strings.TrimSpace(input.BusinessRegistrationNumber),
		VATNumber:                  strings.ToUpper(strings.TrimSpace(input.VATNumber)),
		TaxIDType:                  strings.ToLower(strings.TrimSpace(input.TaxIDType)),
		TaxIDValue:                 strings.ToUpper(strings.TrimSpace(input.TaxIDValue)),
		CountryCode:                strings.ToUpper(strings.TrimSpace(input.CountryCode)),
		StateRegion:                strings.TrimSpace(input.StateRegion),
	}

	if normalized.BillingContactEmail != "" {
		if _, err := mail.ParseAddress(normalized.BillingContactEmail); err != nil {
			return UpdateBillingProfileInput{}, &ValidationError{
				Field:   "billing_contact_email",
				Message: "billing_contact_email must be a valid email address",
			}
		}
	}

	if normalized.LegalEntityType == "" {
		normalized.LegalEntityType = defaultLegalEntityType(accountProfile.AccountType)
	}

	if !isAllowedLegalEntityType(normalized.LegalEntityType) {
		return UpdateBillingProfileInput{}, &ValidationError{
			Field:   "legal_entity_type",
			Message: "legal_entity_type must be one of individual, sole_proprietor, private_company, public_company, or non_profit",
		}
	}

	if normalized.VATNumber != "" && !billingIdentifierPattern.MatchString(normalized.VATNumber) {
		return UpdateBillingProfileInput{}, &ValidationError{
			Field:   "vat_number",
			Message: "vat_number must contain only letters, numbers, spaces, slashes, or hyphens",
		}
	}

	if normalized.TaxIDValue != "" && !billingIdentifierPattern.MatchString(normalized.TaxIDValue) {
		return UpdateBillingProfileInput{}, &ValidationError{
			Field:   "tax_id_value",
			Message: "tax_id_value must contain only letters, numbers, spaces, slashes, or hyphens",
		}
	}

	if normalized.CountryCode == "" {
		normalized.CountryCode = accountProfile.CountryCode
	}
	if normalized.StateRegion == "" {
		normalized.StateRegion = accountProfile.StateRegion
	}

	return normalized, nil
}

func defaultLegalEntityType(accountType string) string {
	if accountType == "personal" {
		return "individual"
	}

	return "private_company"
}

func isAllowedLegalEntityType(value string) bool {
	switch value {
	case "individual", "sole_proprietor", "private_company", "public_company", "non_profit":
		return true
	default:
		return false
	}
}

// profile_setup_complete is true only when the six required core profile fields are present.
func isProfileSetupComplete(input UpdateAccountProfileInput) bool {
	return input.OwnerName != "" &&
		input.LoginEmail != "" &&
		input.DisplayName != "" &&
		input.AccountType != "" &&
		input.CountryCode != "" &&
		input.StateRegion != ""
}
