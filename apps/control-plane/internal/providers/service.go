package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"

	"github.com/google/uuid"
)

// slugRe allows lowercase alphanumeric, hyphens, and underscores; must start
// with a letter or digit; max 64 chars. Values flow into LiteLLM YAML config
// (plan 20-03), so the charset is intentionally tight.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// apiKeyEnvRe matches conventional POSIX env-var names: uppercase letters,
// digits, underscores; must start with a letter or underscore; max 128 chars.
var apiKeyEnvRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// litellmPrefixRe allows the characters that appear in valid LiteLLM model
// prefixes (e.g. "openrouter/", "together_ai/", "groq/"): alphanumeric,
// hyphens, underscores, forward-slashes, and dots; max 128 chars.
var litellmPrefixRe = regexp.MustCompile(`^[a-zA-Z0-9_./-]{0,128}$`)

// Service wraps the Repository with input validation.
type Service struct {
	repo Repository
}

// NewService returns a Service backed by the given repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create validates input then delegates to the repository.
func (s *Service) Create(ctx context.Context, p Provider) (Provider, error) {
	if err := validate(p); err != nil {
		return Provider{}, err
	}
	return s.repo.Create(ctx, p)
}

// List returns all providers.
func (s *Service) List(ctx context.Context) ([]Provider, error) {
	return s.repo.List(ctx)
}

// Get returns a single provider by ID.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Provider, error) {
	return s.repo.Get(ctx, id)
}

// Update validates input then delegates to the repository.
func (s *Service) Update(ctx context.Context, id uuid.UUID, p Provider) (Provider, error) {
	if err := validate(p); err != nil {
		return Provider{}, err
	}
	return s.repo.Update(ctx, id, p)
}

// Delete soft-deletes a provider (sets enabled=false).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// ErrValidation wraps a field-level validation message.
type ErrValidation struct {
	Field   string
	Message string
}

func (e *ErrValidation) Error() string {
	return fmt.Sprintf("validation: %s: %s", e.Field, e.Message)
}

// validate checks the required fields on a Provider before persistence.
// Charset rules are strict because slug, api_key_env, and litellm_prefix
// flow verbatim into generated LiteLLM YAML config (plan 20-03).
func validate(p Provider) error {
	if p.Slug == "" {
		return &ErrValidation{Field: "slug", Message: "must not be empty"}
	}
	if !slugRe.MatchString(p.Slug) {
		return &ErrValidation{Field: "slug", Message: "must match ^[a-z0-9][a-z0-9_-]{0,63}$ (lowercase, digits, hyphens, underscores; max 64 chars)"}
	}
	if p.APIKeyEnv == "" {
		return &ErrValidation{Field: "api_key_env", Message: "must not be empty"}
	}
	if !apiKeyEnvRe.MatchString(p.APIKeyEnv) {
		return &ErrValidation{Field: "api_key_env", Message: "must be a valid env var name: uppercase letters, digits, underscores; max 128 chars"}
	}
	if p.LiteLLMPrefix != "" && !litellmPrefixRe.MatchString(p.LiteLLMPrefix) {
		return &ErrValidation{Field: "litellm_prefix", Message: "contains disallowed characters; allowed: alphanumeric, _, -, /, ."}
	}
	if p.BaseURL != "" {
		u, err := url.Parse(p.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return &ErrValidation{Field: "base_url", Message: "must be a valid http or https URL"}
		}
	}
	return nil
}

// IsNotFound reports whether the error is a not-found sentinel.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsSlugConflict reports whether the error is a slug-conflict sentinel.
func IsSlugConflict(err error) bool {
	return errors.Is(err, ErrSlugConflict)
}
