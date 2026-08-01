package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// Issue #628: a payment intent must never claim `completed` unless the credit
// grant that entitles the customer actually succeeded, and a failed grant must
// leave a durable, retryable record instead of a silent loss.

// seedSettlementIntent inserts an intent in pending_redirect with a provider id
// resolvable by GetPaymentIntentByProviderID, and returns it.
func seedSettlementIntent(t *testing.T, repo *stubRepository, rail Rail, providerIntentID string) PaymentIntent {
	t.Helper()
	intent := PaymentIntent{
		ID:               uuid.New(),
		AccountID:        uuid.New(),
		Rail:             rail,
		Status:           IntentStatusPendingRedirect,
		Credits:          100_000,
		AmountUSD:        100,
		IdempotencyKey:   "idem-" + providerIntentID,
		ProviderIntentID: providerIntentID,
		Metadata:         map[string]any{},
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.InsertPaymentIntent(context.Background(), intent); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	repo.byProv[providerIntentID] = intent.ID
	return intent
}

func succeededRail(rail Rail, providerIntentID string) *stubRail {
	r := newStubRail(rail)
	r.eventResult = RailEvent{
		ProviderIntentID: providerIntentID,
		EventType:        "payment.succeeded",
		RawPayload:       []byte(`{"ok":true}`),
	}
	return r
}

// (a) A grant failure must NOT leave the intent marked completed. A completed
// intent asserts the customer was served; if the grant failed they were not.
func TestHandleProviderEvent_GrantFailureLeavesIntentNotCompleted(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{returnErr: errors.New("ledger unavailable")}
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "US"}}
	rail := succeededRail(RailStripe, "prov_stripe_grantfail")
	intent := seedSettlementIntent(t, repo, RailStripe, "prov_stripe_grantfail")

	svc := buildService(repo, led, prof, &stubFXProvider{}, map[Rail]PaymentRail{RailStripe: rail})
	err := svc.HandleProviderEvent(context.Background(), RailStripe, []byte(`{"ok":true}`), nil)
	if err == nil {
		t.Fatal("expected an error when the credit grant fails, got nil")
	}

	after, getErr := repo.GetPaymentIntent(context.Background(), intent.ID)
	if getErr != nil {
		t.Fatalf("get intent: %v", getErr)
	}
	if after.Status == IntentStatusCompleted {
		t.Fatalf("intent claims completed with no credit grant: money collected, entitlement not delivered (status=%s)", after.Status)
	}
}

// (a) Same invariant on the BD confirmation loop, which is the second call site
// of the transition-then-grant ordering.
func TestConfirmPendingBDPayments_GrantFailureLeavesIntentNotCompleted(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{returnErr: errors.New("ledger unavailable")}
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "BD"}}

	confirmingAt := time.Now().UTC().Add(-4 * time.Minute)
	intent := seedSettlementIntent(t, repo, RailBkash, "prov_bkash_grantfail")
	if _, err := repo.CompareAndSetStatus(context.Background(), intent.ID, IntentStatusPendingRedirect, IntentStatusConfirming); err != nil {
		t.Fatalf("seed confirming: %v", err)
	}
	if err := repo.SetConfirmingAt(context.Background(), intent.ID, confirmingAt); err != nil {
		t.Fatalf("seed confirming_at: %v", err)
	}

	svc := buildService(repo, led, prof, &stubFXProvider{}, map[Rail]PaymentRail{RailBkash: newStubRail(RailBkash)})
	if _, err := svc.ConfirmPendingBDPayments(context.Background()); err == nil {
		t.Fatal("expected an error when the credit grant fails, got nil")
	}

	after, getErr := repo.GetPaymentIntent(context.Background(), intent.ID)
	if getErr != nil {
		t.Fatalf("get intent: %v", getErr)
	}
	if after.Status == IntentStatusCompleted {
		t.Fatalf("intent claims completed with no credit grant (status=%s)", after.Status)
	}
}

// (c) Returning a retryable status to the provider guarantees redeliveries, so a
// duplicate delivery of the same event must grant exactly once and still report
// success.
func TestHandleProviderEvent_DuplicateDeliveryGrantsExactlyOnce(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{accountProfile: profiles.AccountProfile{CountryCode: "US"}}
	rail := succeededRail(RailStripe, "prov_stripe_dup")
	intent := seedSettlementIntent(t, repo, RailStripe, "prov_stripe_dup")

	svc := buildService(repo, led, prof, &stubFXProvider{}, map[Rail]PaymentRail{RailStripe: rail})
	body := []byte(`{"ok":true}`)
	for attempt := 1; attempt <= 3; attempt++ {
		if err := svc.HandleProviderEvent(context.Background(), RailStripe, body, nil); err != nil {
			t.Fatalf("redelivery %d must succeed, got %v", attempt, err)
		}
	}

	if led.callCount() != 1 {
		t.Fatalf("expected exactly 1 credit grant across 3 redeliveries, got %d", led.callCount())
	}
	if got := led.calls[0].credits; got != intent.Credits {
		t.Fatalf("expected %d credits granted, got %d", intent.Credits, got)
	}

	after, _ := repo.GetPaymentIntent(context.Background(), intent.ID)
	if after.Status != IntentStatusCompleted {
		t.Fatalf("expected completed after a successful grant, got %s", after.Status)
	}
}

// (e) A failure before the intent is even loaded must still leave a durable
// record of the delivery. Without it the loss is invisible, not merely
// unrecovered: nothing shows the event ever arrived.
func TestHandleProviderEvent_ParseFailureStillRecordsDelivery(t *testing.T) {
	repo := newStubRepository()
	rail := newStubRail(RailStripe)
	rail.eventErr = errors.New("signature verification failed")

	svc := buildService(repo, &stubLedger{}, &stubProfiles{}, &stubFXProvider{}, map[Rail]PaymentRail{RailStripe: rail})
	body := []byte(`not even json`)
	err := svc.HandleProviderEvent(context.Background(), RailStripe, body, nil)
	if err == nil {
		t.Fatal("expected an error when the payload cannot be parsed, got nil")
	}
	if !errors.Is(err, ErrEventRejected) {
		t.Fatalf("an unparseable payload must be reported as rejected (no retry), got %v", err)
	}

	deliveries := repo.deliveryList()
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 durable delivery record, got %d", len(deliveries))
	}
	got := deliveries[0]
	if got.Status != DeliveryStatusFailed {
		t.Fatalf("expected the delivery marked %s, got %s", DeliveryStatusFailed, got.Status)
	}
	if got.RawBody != string(body) {
		t.Fatalf("expected the raw body preserved verbatim, got %q", got.RawBody)
	}
	if got.ErrorDetail == "" {
		t.Fatal("expected the failure reason recorded on the delivery")
	}
}

// (e) An intent that cannot be resolved is retryable, not rejected: a webhook can
// legitimately overtake the intent's own provider-id write.
func TestHandleProviderEvent_UnknownIntentRecordsRetryableDelivery(t *testing.T) {
	repo := newStubRepository()
	rail := succeededRail(RailStripe, "prov_stripe_never_seen")

	svc := buildService(repo, &stubLedger{}, &stubProfiles{}, &stubFXProvider{}, map[Rail]PaymentRail{RailStripe: rail})
	err := svc.HandleProviderEvent(context.Background(), RailStripe, []byte(`{"ok":true}`), nil)
	if err == nil {
		t.Fatal("expected an error for an unresolvable intent, got nil")
	}
	if errors.Is(err, ErrEventRejected) {
		t.Fatal("an unresolvable intent must stay retryable, not be rejected")
	}

	deliveries := repo.deliveryList()
	if len(deliveries) != 1 || deliveries[0].Status != DeliveryStatusFailed {
		t.Fatalf("expected 1 failed delivery record, got %+v", deliveries)
	}
}

// A settled delivery leaves the dead-letter view.
func TestHandleProviderEvent_SuccessMarksDeliveryProcessed(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	rail := succeededRail(RailStripe, "prov_stripe_processed")
	intent := seedSettlementIntent(t, repo, RailStripe, "prov_stripe_processed")

	svc := buildService(repo, led, &stubProfiles{}, &stubFXProvider{}, map[Rail]PaymentRail{RailStripe: rail})
	if err := svc.HandleProviderEvent(context.Background(), RailStripe, []byte(`{"ok":true}`), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deliveries := repo.deliveryList()
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 delivery record, got %d", len(deliveries))
	}
	got := deliveries[0]
	if got.Status != DeliveryStatusProcessed {
		t.Fatalf("expected %s, got %s", DeliveryStatusProcessed, got.Status)
	}
	if got.PaymentIntentID == nil || *got.PaymentIntentID != intent.ID {
		t.Fatalf("expected the delivery linked to intent %s, got %v", intent.ID, got.PaymentIntentID)
	}
	if got.EventType != "payment.succeeded" {
		t.Fatalf("expected the event type recorded, got %q", got.EventType)
	}
}
