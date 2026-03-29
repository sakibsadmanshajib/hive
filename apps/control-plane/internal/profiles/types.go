package profiles

import "errors"

// AccountProfile is the current-account core profile DTO returned by the API.
type AccountProfile struct {
	OwnerName            string `json:"owner_name"`
	LoginEmail           string `json:"login_email"`
	DisplayName          string `json:"display_name"`
	AccountType          string `json:"account_type"`
	CountryCode          string `json:"country_code"`
	StateRegion          string `json:"state_region"`
	ProfileSetupComplete bool   `json:"profile_setup_complete"`
}

// UpdateAccountProfileInput is the PUT payload for the current-account profile API.
type UpdateAccountProfileInput struct {
	OwnerName   string `json:"owner_name"`
	LoginEmail  string `json:"login_email"`
	DisplayName string `json:"display_name"`
	AccountType string `json:"account_type"`
	CountryCode string `json:"country_code"`
	StateRegion string `json:"state_region"`
}

// ValidationError is returned when a profile update payload is invalid.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// AsValidationError is a helper for errors.As with ValidationError.
func AsValidationError(err error, target **ValidationError) bool {
	return errors.As(err, target)
}

// ErrNotFound is returned when the current account profile does not exist.
var ErrNotFound = errors.New("profiles: not found")
