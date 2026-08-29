package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// adapterRepo answers the one read the adapter's chain performs.
type adapterRepo struct {
	profile profiles.AccountProfile
	err     error
}

func (a *adapterRepo) GetAccountProfile(_ context.Context, _ uuid.UUID) (profiles.AccountProfile, error) {
	return a.profile, a.err
}

func (a *adapterRepo) UpdateAccountProfile(_ context.Context, _ uuid.UUID, _ profiles.UpdateAccountProfileInput, _ bool) error {
	return nil
}

func (a *adapterRepo) GetBillingProfile(_ context.Context, _ uuid.UUID) (profiles.BillingProfile, error) {
	return profiles.BillingProfile{}, nil
}

func (a *adapterRepo) UpsertBillingProfile(_ context.Context, _ uuid.UUID, _ profiles.UpdateBillingProfileInput) error {
	return nil
}

// The demo payment stub reaches the account country through this adapter, so it
// broke on a missing account_profiles row exactly the way the real payment
// service did (issue #1386). HIVE_PAYMENTS_STUB is what the demo box runs, so
// leaving the adapter reading the profile directly would have left "Buy
// credits" dead there even with the production path fixed.
func TestStubCountryAdapter_MissingProfileRowResolvesToNoCountry(t *testing.T) {
	adapter := &stubCountryAdapter{svc: profiles.NewService(&adapterRepo{err: profiles.ErrNotFound})}

	country, err := adapter.CountryCode(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("a missing profile row must not fail the stub checkout, got %v", err)
	}
	if country != "" {
		t.Errorf("expected no declared country, got %q", country)
	}
}

func TestStubCountryAdapter_ReturnsTheDeclaredCountry(t *testing.T) {
	adapter := &stubCountryAdapter{
		svc: profiles.NewService(&adapterRepo{profile: profiles.AccountProfile{CountryCode: "BD"}}),
	}

	country, err := adapter.CountryCode(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("CountryCode: %v", err)
	}
	if country != "BD" {
		t.Errorf("expected BD, got %q", country)
	}
}
