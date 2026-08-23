package payments

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// Issue #640: handleWebhook is unauthenticated by design and read the body with
// no size limit. Since #638 the delivery row carrying the full raw_body is
// written BEFORE signature verification, so any unauthenticated caller could
// write an arbitrarily large row on every request, and providers retry on every
// non 2xx including 400, so a rejected payload wrote a fresh row per redelivery.
//
// The record-before-verify ordering is deliberate and stays. The fix is the cap.

// bodyCapFixture wires the real Service and Handler over the in-memory stub
// repository so delivery rows can actually be counted.
type bodyCapFixture struct {
	handler *Handler
	repo    *stubRepository
	rail    *stubRail
}

func newBodyCapFixture(t *testing.T) *bodyCapFixture {
	t.Helper()

	repo := newStubRepository()
	stubbedRail := newStubRail(RailStripe)

	intentID := uuid.New()
	providerIntentID := "prov_stripe_640"
	intent := PaymentIntent{
		ID:               intentID,
		AccountID:        uuid.New(),
		Rail:             RailStripe,
		Status:           IntentStatusPendingRedirect,
		Credits:          CreditsPerUSD,
		AmountUSD:        100,
		IdempotencyKey:   "idem-640",
		ProviderIntentID: providerIntentID,
		Metadata:         map[string]any{},
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	_ = repo.InsertPaymentIntent(t.Context(), intent)
	repo.byProv[providerIntentID] = intentID

	stubbedRail.eventResult = RailEvent{
		ProviderIntentID: providerIntentID,
		EventType:        "payment.succeeded",
		RawPayload:       []byte(`{}`),
	}

	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US"},
		billingProfile: profiles.BillingProfile{BillingContactName: "Jane", CountryCode: "US"},
	}
	svc := NewService(repo, &stubLedger{}, prof, &stubFXProvider{}, map[Rail]PaymentRail{RailStripe: stubbedRail})

	return &bodyCapFixture{
		handler: NewHandler(svc, &stubBodyCapResolver{}),
		repo:    repo,
		rail:    stubbedRail,
	}
}

// stubBodyCapResolver satisfies AccountResolver. The webhook route is
// unauthenticated, so this is never consulted on the paths under test.
type stubBodyCapResolver struct{}

func (s *stubBodyCapResolver) EnsureViewerContext(_ context.Context) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (f *bodyCapFixture) post(body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

// TestWebhook_OversizedBody_RejectedWithoutPersistingDelivery is the core of
// #640. The cap has to sit ahead of the delivery insert, otherwise capping the
// read accomplishes nothing: the oversized row would already be written.
func TestWebhook_OversizedBody_RejectedWithoutPersistingDelivery(t *testing.T) {
	f := newBodyCapFixture(t)

	oversized := []byte(`{"padding":"` + strings.Repeat("A", maxWebhookBodyBytes+1024) + `"}`)
	rr := f.post(oversized)

	if rr.Code == http.StatusOK {
		t.Fatalf("an oversized unauthenticated body was accepted (%d)", rr.Code)
	}
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", rr.Code)
	}

	if got := len(f.repo.deliveryList()); got != 0 {
		t.Errorf("expected 0 delivery rows for an oversized body, got %d: an unauthenticated caller can still write arbitrarily large rows", got)
	}
}

// TestWebhook_OversizedBody_RepeatedDeliveriesPersistNothing covers the
// compounding case the issue calls out: providers retry on every non 2xx,
// including 400, so an uncapped reject wrote a fresh row per redelivery.
func TestWebhook_OversizedBody_RepeatedDeliveriesPersistNothing(t *testing.T) {
	f := newBodyCapFixture(t)

	oversized := []byte(`{"padding":"` + strings.Repeat("B", maxWebhookBodyBytes+1024) + `"}`)
	for i := 0; i < 5; i++ {
		if code := f.post(oversized).Code; code == http.StatusOK {
			t.Fatalf("redelivery %d accepted an oversized body", i+1)
		}
	}

	if got := len(f.repo.deliveryList()); got != 0 {
		t.Errorf("expected 0 delivery rows after 5 oversized redeliveries, got %d", got)
	}
}

// TestWebhook_NormalPayload_SucceedsAndPersistsDelivery is the guard against
// fixing #640 by breaking settlement. A cap set below a legitimate provider
// payload would be far worse than the problem it solves.
func TestWebhook_NormalPayload_SucceedsAndPersistsDelivery(t *testing.T) {
	f := newBodyCapFixture(t)

	rr := f.post([]byte(`{"type":"payment_intent.succeeded","data":{"object":{"id":"pi_abc"}}}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for a normal payload, got %d body=%s", rr.Code, rr.Body.String())
	}

	records := f.repo.deliveryList()
	if len(records) != 1 {
		t.Fatalf("expected 1 delivery row, got %d", len(records))
	}
	if records[0].Status != DeliveryStatusProcessed {
		t.Errorf("expected the delivery marked processed, got %s", records[0].Status)
	}
}

// TestWebhook_PayloadAtCap_StillAccepted pins the boundary so a later tightening
// of the limit cannot silently start dropping legitimate provider events.
func TestWebhook_PayloadAtCap_StillAccepted(t *testing.T) {
	f := newBodyCapFixture(t)

	// Exactly at the limit. MaxBytesReader rejects only what exceeds it.
	prefix := `{"padding":"`
	suffix := `"}`
	padding := maxWebhookBodyBytes - len(prefix) - len(suffix)
	atCap := []byte(prefix + strings.Repeat("C", padding) + suffix)
	if len(atCap) != maxWebhookBodyBytes {
		t.Fatalf("test built a %d byte body, expected %d", len(atCap), maxWebhookBodyBytes)
	}

	if rr := f.post(atCap); rr.Code != http.StatusOK {
		t.Fatalf("a payload exactly at the cap was rejected with %d", rr.Code)
	}
	if got := len(f.repo.deliveryList()); got != 1 {
		t.Errorf("expected 1 delivery row, got %d", got)
	}
}

// TestWebhook_CapIsAtLeastOneMiB documents the floor. Provider events are a few
// kilobytes in practice; the margin is what keeps an unusually large but
// legitimate event from being dropped.
func TestWebhook_CapIsAtLeastOneMiB(t *testing.T) {
	if maxWebhookBodyBytes < 1<<20 {
		t.Fatalf("cap %d is below 1 MiB and risks rejecting legitimate provider events", maxWebhookBodyBytes)
	}
}
