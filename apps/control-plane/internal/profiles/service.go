package profiles

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Service encapsulates all profile business logic.
type Service struct {
	repo Repository
}

// NewService returns a new profiles Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetAccountProfile returns the current-account core profile.
func (s *Service) GetAccountProfile(ctx context.Context, accountID uuid.UUID) (AccountProfile, error) {
	return s.repo.GetAccountProfile(ctx, accountID)
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

// profile_setup_complete is true only when the six required core profile fields are present.
func isProfileSetupComplete(input UpdateAccountProfileInput) bool {
	return input.OwnerName != "" &&
		input.LoginEmail != "" &&
		input.DisplayName != "" &&
		input.AccountType != "" &&
		input.CountryCode != "" &&
		input.StateRegion != ""
}
