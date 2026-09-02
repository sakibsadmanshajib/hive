package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// endToEnd wiring for the exact request that answered 500 on the deployed
// console: the real Handler, the real payments Service and the real
// profiles.Service, with only the pgx repository replaced. Nothing between the
// HTTP boundary and the profile read is stubbed, so this reproduces issue #1386
// at the level the customer actually hit it.

type railsProfilesRepo struct{ err error }

func (r *railsProfilesRepo) GetAccountProfile(_ context.Context, _ uuid.UUID) (profiles.AccountProfile, error) {
	return profiles.AccountProfile{}, r.err
}

func (r *railsProfilesRepo) UpdateAccountProfile(_ context.Context, _ uuid.UUID, _ profiles.UpdateAccountProfileInput, _ bool) error {
	return nil
}

func (r *railsProfilesRepo) GetBillingProfile(_ context.Context, _ uuid.UUID) (profiles.BillingProfile, error) {
	return profiles.BillingProfile{}, nil
}

func (r *railsProfilesRepo) UpsertBillingProfile(_ context.Context, _ uuid.UUID, _ profiles.UpdateBillingProfileInput) error {
	return nil
}

type railsResolver struct{ accountID uuid.UUID }

func (r *railsResolver) EnsureViewerContext(_ context.Context) (uuid.UUID, error) {
	return r.accountID, nil
}

func TestGetRailsEndpoint_AccountWithNoProfileRowAnswers200(t *testing.T) {
	profilesSvc := profiles.NewService(&railsProfilesRepo{err: profiles.ErrNotFound})
	svc := NewService(
		newStubRepository(),
		&stubLedger{},
		profilesSvc,
		&stubFXProvider{},
		map[Rail]PaymentRail{RailStripe: newStubRail(RailStripe)},
	)
	h := NewHandler(svc, &railsResolver{accountID: uuid.New()})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/checkout/rails", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Logged so the response bytes can be lifted into a proof record rather
	// than hand-written from the struct definition.
	t.Logf("checkout rails payload: %s", rr.Body.String())

	// Decode with the same required-field rules the console applies, so a green
	// result here means the modal has a rail to render.
	var payload struct {
		Rails []struct {
			Rail       string `json:"rail"`
			Label      string `json:"label"`
			Currency   string `json:"currency"`
			Enabled    *bool  `json:"enabled"`
			MinCredits int64  `json:"min_credits"`
			MaxCredits int64  `json:"max_credits"`
		} `json:"rails"`
		PricePerBlockMinor *int64 `json:"price_per_block_minor"`
		CreditBlockSize    *int64 `json:"credit_block_size"`
		Currency           string `json:"currency"`
		CreditIncrement    *int64 `json:"credit_increment"`
		MinCredits         *int64 `json:"min_credits"`
		MaxCredits         *int64 `json:"max_credits"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(payload.Rails) != 1 {
		t.Fatalf("expected the non-BD rail set, got %d rails", len(payload.Rails))
	}
	rail := payload.Rails[0]
	if rail.Rail != string(RailStripe) || rail.Label == "" || rail.Currency == "" || rail.Enabled == nil || !*rail.Enabled {
		t.Errorf("the console decoder would drop this rail item: %+v", rail)
	}
	if payload.Currency != "USD" {
		t.Errorf("expected USD for an unresolved country, got %q", payload.Currency)
	}
	if payload.PricePerBlockMinor == nil || *payload.PricePerBlockMinor != 106 {
		t.Errorf("expected 106 minor units per block, got %v", payload.PricePerBlockMinor)
	}
	if payload.CreditBlockSize == nil || *payload.CreditBlockSize != CreditsPerUSD {
		t.Errorf("expected block size %d, got %v", CreditsPerUSD, payload.CreditBlockSize)
	}
	if payload.CreditIncrement == nil || *payload.CreditIncrement != CreditIncrement {
		t.Errorf("expected increment %d, got %v", CreditIncrement, payload.CreditIncrement)
	}
	if payload.MinCredits == nil || *payload.MinCredits != MinPurchaseCredits {
		t.Errorf("expected min %d, got %v", MinPurchaseCredits, payload.MinCredits)
	}
	if payload.MaxCredits == nil || *payload.MaxCredits != MaxPurchaseCreditsStripe {
		t.Errorf("expected max %d, got %v", MaxPurchaseCreditsStripe, payload.MaxCredits)
	}
}
