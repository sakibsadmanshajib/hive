package profiles

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// countryRepo is a Repository that answers GetAccountProfile with whatever the
// test asks for. stubRepo cannot express "the read itself failed", which is the
// case that separates a missing row from a broken database.
type countryRepo struct {
	profile AccountProfile
	err     error
}

func (c *countryRepo) GetAccountProfile(_ context.Context, _ uuid.UUID) (AccountProfile, error) {
	return c.profile, c.err
}

func (c *countryRepo) UpdateAccountProfile(_ context.Context, _ uuid.UUID, _ UpdateAccountProfileInput, _ bool) error {
	return nil
}

func (c *countryRepo) GetBillingProfile(_ context.Context, _ uuid.UUID) (BillingProfile, error) {
	return BillingProfile{}, nil
}

func (c *countryRepo) UpsertBillingProfile(_ context.Context, _ uuid.UUID, _ UpdateBillingProfileInput) error {
	return nil
}

var _ Repository = (*countryRepo)(nil)

// An account with no `account_profiles` row has no declared country, which is
// the same state the repository already produces for a NULL country_code. It
// must therefore resolve, not fail. Issue #1386: treating it as fatal answered
// 500 on the whole checkout surface for the 14 live accounts counted in #999.
func TestCountryCode_MissingProfileRowResolvesToNoCountry(t *testing.T) {
	svc := NewService(&countryRepo{err: ErrNotFound})

	country, err := svc.CountryCode(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("a missing profile row must not fail the read, got %v", err)
	}
	if country != "" {
		t.Errorf("expected no declared country, got %q", country)
	}
}

// The inverse, so the guard cannot widen into "any failure means no country".
// A database failure has to stay a failure: reading it as an unset country
// would silently pick a rail set from a read that never happened.
func TestCountryCode_RealFailureStillPropagates(t *testing.T) {
	boom := errors.New("connection reset by peer")
	svc := NewService(&countryRepo{err: boom})

	if _, err := svc.CountryCode(context.Background(), uuid.New()); !errors.Is(err, boom) {
		t.Fatalf("expected the underlying failure to propagate, got %v", err)
	}
}

func TestCountryCode_ReturnsTheDeclaredCountry(t *testing.T) {
	svc := NewService(&countryRepo{profile: AccountProfile{CountryCode: "BD"}})

	country, err := svc.CountryCode(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("CountryCode: %v", err)
	}
	if country != "BD" {
		t.Errorf("expected BD, got %q", country)
	}
}

// GetAccountProfile keeps its own contract. /api/v1/accounts/current/profile
// still has to answer 404 for a missing row, and the profile-setup gate still
// has to see the absence, so the tolerance added for the country must not leak
// into the full read.
func TestGetAccountProfile_StillReportsAMissingRow(t *testing.T) {
	svc := NewService(&countryRepo{err: ErrNotFound})

	if _, err := svc.GetAccountProfile(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound to survive, got %v", err)
	}
}
