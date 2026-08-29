package payments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	stripego "github.com/stripe/stripe-go/v84"
	"github.com/stripe/stripe-go/v84/webhook"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	stripeRail "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/stripe"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// Issue #640, third acceptance point: the body cap must be an extra gate in
// front of signature verification, never a replacement for it or a way around
// it. These tests drive the REAL Stripe rail, so verification runs for real.
//
// The signing secret below is an obvious fake and the signatures are generated
// with stripe-go's own test helper. Nothing here weakens, bypasses or
// conditionally skips verification.
const testWebhookSecret = "whsec_testvalidwebhooksecret12345"

// ---------------------------------------------------------------------------
// Minimal Repository stub. Only the webhook settlement path is exercised.
// ---------------------------------------------------------------------------

type capRepo struct {
	mu         sync.Mutex
	intents    map[uuid.UUID]payments.PaymentIntent
	byProv     map[string]uuid.UUID
	deliveries []payments.WebhookDelivery
}

func newCapRepo() *capRepo {
	return &capRepo{
		intents: make(map[uuid.UUID]payments.PaymentIntent),
		byProv:  make(map[string]uuid.UUID),
	}
}

func (r *capRepo) InsertPaymentIntent(_ context.Context, intent payments.PaymentIntent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.intents[intent.ID] = intent
	r.byProv[intent.ProviderIntentID] = intent.ID
	return nil
}

func (r *capRepo) GetPaymentIntent(_ context.Context, id uuid.UUID) (payments.PaymentIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.intents[id]
	if !ok {
		return payments.PaymentIntent{}, payments.ErrIntentNotFound
	}
	return i, nil
}

func (r *capRepo) GetPaymentIntentByProviderID(_ context.Context, providerID string) (payments.PaymentIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byProv[providerID]
	if !ok {
		return payments.PaymentIntent{}, payments.ErrIntentNotFound
	}
	return r.intents[id], nil
}

func (r *capRepo) CompareAndSetStatus(_ context.Context, id uuid.UUID, from, to payments.IntentStatus) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.intents[id]
	if !ok {
		return false, payments.ErrIntentNotFound
	}
	if i.Status != from {
		return false, nil
	}
	i.Status = to
	r.intents[id] = i
	return true, nil
}

func (r *capRepo) UpdateProviderDetails(_ context.Context, _ uuid.UUID, _, _ string, _ *time.Time) error {
	return nil
}
func (r *capRepo) SetConfirmingAt(_ context.Context, _ uuid.UUID, _ time.Time) error { return nil }
func (r *capRepo) ListConfirmingIntents(_ context.Context, _ time.Time) ([]payments.PaymentIntent, error) {
	return nil, nil
}
func (r *capRepo) InsertPaymentEvent(_ context.Context, _ payments.PaymentEvent) error { return nil }

func (r *capRepo) InsertWebhookDelivery(_ context.Context, delivery payments.WebhookDelivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries = append(r.deliveries, delivery)
	return nil
}

func (r *capRepo) UpdateWebhookDelivery(_ context.Context, id uuid.UUID, status payments.DeliveryStatus, _ *uuid.UUID, _, errorDetail string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.deliveries {
		if r.deliveries[i].ID == id {
			r.deliveries[i].Status = status
			r.deliveries[i].ErrorDetail = errorDetail
			return nil
		}
	}
	return nil
}

func (r *capRepo) deliveryCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.deliveries)
}

func (r *capRepo) InsertFXSnapshot(_ context.Context, _ payments.FXSnapshot) error { return nil }
func (r *capRepo) GetFXSnapshot(_ context.Context, _ uuid.UUID) (payments.FXSnapshot, error) {
	return payments.FXSnapshot{}, payments.ErrIntentNotFound
}

// ---------------------------------------------------------------------------
// Remaining dependencies
// ---------------------------------------------------------------------------

type capLedger struct{}

func (capLedger) GrantCredits(_ context.Context, _ uuid.UUID, _ string, _ int64, _ map[string]any) (ledger.LedgerEntry, error) {
	return ledger.LedgerEntry{}, nil
}

type capProfiles struct{}

func (capProfiles) GetBillingProfile(_ context.Context, _ uuid.UUID) (profiles.BillingProfile, error) {
	return profiles.BillingProfile{BillingContactName: "Jane", CountryCode: "US"}, nil
}
func (capProfiles) CountryCode(_ context.Context, _ uuid.UUID) (string, error) {
	return "US", nil
}

type capFX struct{}

func (capFX) CreateSnapshot(_ context.Context, _ payments.Repository, _ uuid.UUID) (payments.FXSnapshot, error) {
	return payments.FXSnapshot{}, nil
}

type capResolver struct{}

func (capResolver) EnsureViewerContext(_ context.Context) (uuid.UUID, error) { return uuid.Nil, nil }

// ---------------------------------------------------------------------------
// Fixture driving the real Stripe rail
// ---------------------------------------------------------------------------

type signatureFixture struct {
	handler *payments.Handler
	repo    *capRepo
}

func newSignatureFixture(t *testing.T) *signatureFixture {
	t.Helper()

	repo := newCapRepo()
	_ = repo.InsertPaymentIntent(t.Context(), payments.PaymentIntent{
		ID:               uuid.New(),
		AccountID:        uuid.New(),
		Rail:             payments.RailStripe,
		Status:           payments.IntentStatusPendingRedirect,
		Credits:          payments.CreditsPerUSD,
		ProviderIntentID: "cs_test_640",
		Metadata:         map[string]any{},
	})

	rail := stripeRail.NewRail("sk_test_key", testWebhookSecret)
	svc := payments.NewService(repo, capLedger{}, capProfiles{}, capFX{},
		map[payments.Rail]payments.PaymentRail{payments.RailStripe: rail})

	return &signatureFixture{handler: payments.NewHandler(svc, capResolver{}), repo: repo}
}

// signedEvent builds a genuinely signed Stripe event using stripe-go's test
// helper. padTo inflates the payload with a real event field so the signature
// stays valid at any size.
//
// The Checkout Session is this rail's unit of payment; payment_intent.* events
// are deliberately not settled (see stripe/rail.go), so the event has to be a
// paid checkout.session.completed to reach the settlement path at all.
func signedEvent(t *testing.T, sessionID string, padTo int) ([]byte, string) {
	t.Helper()

	sess := &stripego.CheckoutSession{
		ID:            sessionID,
		PaymentStatus: stripego.CheckoutSessionPaymentStatusPaid,
	}
	rawObj, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal checkout session: %v", err)
	}

	event := struct {
		Object     string              `json:"object"`
		Type       stripego.EventType  `json:"type"`
		APIVersion string              `json:"api_version"`
		Data       *stripego.EventData `json:"data"`
		Padding    string              `json:"padding,omitempty"`
	}{
		Object:     "event",
		Type:       stripego.EventType("checkout.session.completed"),
		APIVersion: stripego.APIVersion,
		Data:       &stripego.EventData{Raw: json.RawMessage(rawObj)},
	}

	rawEvent, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if padTo > len(rawEvent) {
		event.Padding = strings.Repeat("X", padTo-len(rawEvent))
		if rawEvent, err = json.Marshal(event); err != nil {
			t.Fatalf("marshal padded event: %v", err)
		}
	}

	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   rawEvent,
		Secret:    testWebhookSecret,
		Timestamp: time.Now(),
	})
	return rawEvent, signed.Header
}

func (f *signatureFixture) post(body []byte, signature string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	if signature != "" {
		req.Header.Set("Stripe-Signature", signature)
	}
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

// TestWebhook_OversizedBodyRejectedEvenWithValidSignature proves the cap is not
// something an authenticated-looking caller can talk its way past. A correctly
// signed but oversized payload is still refused, and still writes no row.
func TestWebhook_OversizedBodyRejectedEvenWithValidSignature(t *testing.T) {
	f := newSignatureFixture(t)

	body, sig := signedEvent(t, "cs_test_640", 2<<20)
	rr := f.post(body, sig)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for an oversized body even with a valid signature, got %d", rr.Code)
	}
	if got := f.repo.deliveryCount(); got != 0 {
		t.Errorf("expected 0 delivery rows, got %d", got)
	}
}

// TestWebhook_InvalidSignatureStillRejected is the regression guard on the
// security property itself. If the cap had been added by loosening or
// short-circuiting verification, this would start passing bad signatures.
func TestWebhook_InvalidSignatureStillRejected(t *testing.T) {
	f := newSignatureFixture(t)

	body, _ := signedEvent(t, "cs_test_640", 0)

	for _, tc := range []struct {
		name string
		sig  string
	}{
		{"absent", ""},
		{"garbage", "t=1,v1=deadbeef"},
		{"wrong key", func() string {
			signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
				Payload:   body,
				Secret:    "whsec_testwrongsecret000000000000",
				Timestamp: time.Now(),
			})
			return signed.Header
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.post(body, tc.sig)
			if rr.Code/100 == 2 {
				t.Fatalf("an unverifiable payload was accepted with %d", rr.Code)
			}
		})
	}
}

// TestWebhook_ValidSignatureUnderCapStillSettles confirms the cap did not break
// the normal path: a real signed event under the limit still settles, and its
// delivery row is still written.
func TestWebhook_ValidSignatureUnderCapStillSettles(t *testing.T) {
	f := newSignatureFixture(t)

	body, sig := signedEvent(t, "cs_test_640", 0)
	if rr := f.post(body, sig); rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for a valid signed event, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := f.repo.deliveryCount(); got != 1 {
		t.Errorf("expected 1 delivery row, got %d", got)
	}
}

// TestWebhook_RealStripeEventIsFarBelowCap is the evidence that the chosen limit
// cannot drop a legitimate provider event. A real signed payment_intent event is
// a few hundred bytes; the cap is 1 MiB.
func TestWebhook_RealStripeEventIsFarBelowCap(t *testing.T) {
	body, _ := signedEvent(t, "cs_test_640", 0)
	t.Logf("real signed Stripe checkout.session.completed event: %d bytes (cap is %d bytes)", len(body), 1<<20)
	if len(body) > (1<<20)/10 {
		t.Errorf("a routine provider event is %d bytes, uncomfortably close to the cap", len(body))
	}
}
