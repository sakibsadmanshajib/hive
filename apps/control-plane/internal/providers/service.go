package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

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
func validate(p Provider) error {
	if p.Slug == "" {
		return &ErrValidation{Field: "slug", Message: "must not be empty"}
	}
	if p.APIKeyEnv == "" {
		return &ErrValidation{Field: "api_key_env", Message: "must not be empty"}
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
