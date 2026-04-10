package payments

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
)

// ---------------------------------------------------------------------------
// Stub implementations
// ---------------------------------------------------------------------------

// stubRepository implements Repository with in-memory maps.
type stubRepository struct {
	mu      sync.Mutex
	intents map[uuid.UUID]PaymentIntent
	byProv  map[string]uuid.UUID // providerIntentID -> intentID
	events  []PaymentEvent
	snaps   map[uuid.UUID]FXSnapshot
}

func newStubRepository() *stubRepository {
	return &stubRepository{
		intents: make(map[uuid.UUID]PaymentIntent),
		byProv:  make(map[string]uuid.UUID),
		events:  nil,
		snaps:   make(map[uuid.UUID]FXSnapshot),
	}
}

func (r *stubRepository) InsertPaymentIntent(_ context.Context, intent PaymentIntent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.intents[intent.ID] = intent
	return nil
}

func (r *stubRepository) GetPaymentIntent(_ context.Context, id uuid.UUID) (PaymentIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.intents[id]
	if !ok {
		return PaymentIntent{}, ErrIntentNotFound
	}
	return i, nil
}

func (r *stubRepository) GetPaymentIntentByProviderID(_ context.Context, providerID string) (PaymentIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byProv[providerID]
	if !ok {
		return PaymentIntent{}, ErrIntentNotFound
	}
	return r.intents[id], nil
}

func (r *stubRepository) CompareAndSetStatus(_ context.Context, id uuid.UUID, from, to IntentStatus) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.intents[id]
	if !ok {
		return false, ErrIntentNotFound
	}
	if i.Status != from {
		return false, nil
	}
	i.Status = to
	r.intents[id] = i
	return true, nil
}

func (r *stubRepository) UpdateProviderDetails(_ context.Context, id uuid.UUID, providerIntentID, redirectURL string, expiresAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.intents[id]
	if !ok {
		return ErrIntentNotFound
	}
	i.ProviderIntentID = providerIntentID
	i.RedirectURL = redirectURL
	i.ExpiresAt = expiresAt
	r.intents[id] = i
	r.byProv[providerIntentID] = id
	return nil
}

func (r *stubRepository) SetConfirmingAt(_ context.Context, id uuid.UUID, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.intents[id]
	if !ok {
		return ErrIntentNotFound
	}
	i.ConfirmingAt = &at
	r.intents[id] = i
	return nil
}

func (r *stubRepository) ListConfirmingIntents(_ context.Context, olderThan time.Time) ([]PaymentIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []PaymentIntent
	for _, i := range r.intents {
		if i.Status == IntentStatusConfirming && i.ConfirmingAt != nil && !i.ConfirmingAt.After(olderThan) {
			result = append(result, i)
		}
	}
	return result, nil
}

func (r *stubRepository) InsertPaymentEvent(_ context.Context, event PaymentEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *stubRepository) InsertFXSnapshot(_ context.Context, snap FXSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snaps[snap.ID] = snap
	return nil
}

func (r *stubRepository) GetFXSnapshot(_ context.Context, id uuid.UUID) (FXSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.snaps[id]
	if !ok {
		return FXSnapshot{}, ErrIntentNotFound
	}
	return s, nil
}

// stubRail implements PaymentRail returning fixed values.
type stubRail struct {
	rail      Rail
	initResult InitiateResult
	initErr    error
	eventResult RailEvent
	eventErr    error
}

func newStubRail(rail Rail) *stubRail {
	return &stubRail{
		rail: rail,
		initResult: InitiateResult{
			ProviderIntentID: "prov_" + string(rail),
			RedirectURL:      "https://example.com/pay/" + string(rail),
			ExpiresAt:        time.Now().Add(30 * time.Minute),
		},
	}
}

func (s *stubRail) RailName() Rail { return s.rail }

func (s *stubRail) Initiate(_ context.Context, _ InitiateInput) (InitiateResult, error) {
	return s.initResult, s.initErr
}

func (s *stubRail) ProcessEvent(_ context.Context, _ []byte, _ map[string]string) (RailEvent, error) {
	return s.eventResult, s.eventErr
}

// stubLedger implements LedgerGranter.
type stubLedger struct {
	mu       sync.Mutex
	calls    []ledgerCall
	returnErr error
}

type ledgerCall struct {
	accountID      uuid.UUID
	idempotencyKey string
	credits        int64
}

func (l *stubLedger) GrantCredits(_ context.Context, accountID uuid.UUID, idempotencyKey string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Simulate idempotency: if same key already seen, return without appending.
	for _, c := range l.calls {
		if c.idempotencyKey == idempotencyKey {
			return ledger.LedgerEntry{}, l.returnErr
		}
	}
	l.calls = append(l.calls, ledgerCall{accountID: accountID, idempotencyKey: idempotencyKey, credits: credits})
	return ledger.LedgerEntry{}, l.returnErr
}

func (l *stubLedger) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

// stubProfiles implements ProfileReader.
type stubProfiles struct {
	accountProfile  profiles.AccountProfile
	billingProfile  profiles.BillingProfile
	billingErr      error
}

func (p *stubProfiles) GetBillingProfile(_ context.Context, _ uuid.UUID) (profiles.BillingProfile, error) {
	return p.billingProfile, p.billingErr
}

func (p *stubProfiles) GetAccountProfile(_ context.Context, _ uuid.UUID) (profiles.AccountProfile, error) {
	return p.accountProfile, nil
}

// stubFXProvider implements FXProvider with a fixed snapshot result.
type stubFXProvider struct {
	snap FXSnapshot
	err  error
}

func (f *stubFXProvider) CreateSnapshot(_ context.Context, repo Repository, accountID uuid.UUID) (FXSnapshot, error) {
	if f.err != nil {
		return FXSnapshot{}, f.err
	}
	snap := f.snap
	snap.ID = uuid.New()
	snap.AccountID = accountID
	_ = repo.InsertFXSnapshot(context.Background(), snap)
	return snap, nil
}

// ---------------------------------------------------------------------------
// Helper: build a Service with the given stubs
// ---------------------------------------------------------------------------

func buildService(
	repo Repository,
	led LedgerGranter,
	prof ProfileReader,
	fx FXProvider,
	rails map[Rail]PaymentRail,
) *Service {
	return NewService(repo, led, prof, fx, rails)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestInitiateCheckout_HappyPath_Stripe(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US", AccountType: "personal"},
		billingProfile: profiles.BillingProfile{
			BillingContactName: "John Doe",
			CountryCode:        "US",
		},
	}
	fxProv := &stubFXProvider{}
	stripeRail := newStubRail(RailStripe)
	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{RailStripe: stripeRail})

	intent, err := svc.InitiateCheckout(context.Background(), uuid.New(), RailStripe, 100_000, "https://app.example.com", "idem-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 100,000 credits / (100,000/100) = 100 cents = $1.00
	if intent.AmountUSD != 100 {
		t.Errorf("expected AmountUSD=100 cents, got %d", intent.AmountUSD)
	}
	if intent.Status != IntentStatusPendingRedirect {
		t.Errorf("expected status pending_redirect, got %s", intent.Status)
	}
	if intent.RedirectURL == "" {
		t.Error("expected redirect URL to be set")
	}
	if intent.ProviderIntentID == "" {
		t.Error("expected provider intent ID to be set")
	}
}

func TestInitiateCheckout_BDRail_CreatesFXSnapshot(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "BD", AccountType: "personal"},
		billingProfile: profiles.BillingProfile{
			BillingContactName: "Rahim Uddin",
			CountryCode:        "BD",
			LegalEntityType:    "individual",
		},
	}
	fxProv := &stubFXProvider{
		snap: FXSnapshot{
			BaseCurrency:  "USD",
			QuoteCurrency: "BDT",
			MidRate:       "110.00",
			FeeRate:       "0.05",
			EffectiveRate: "115.500000",
			SourceAPI:     "admin_override",
			FetchedAt:     time.Now(),
			CreatedAt:     time.Now(),
		},
	}
	bkashRail := newStubRail(RailBkash)
	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{RailBkash: bkashRail})

	intent, err := svc.InitiateCheckout(context.Background(), uuid.New(), RailBkash, 100_000, "https://app.example.com", "idem-bd-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if intent.FXSnapshotID == nil {
		t.Error("expected FXSnapshotID to be set for BD rail")
	}
	if intent.LocalCurrency != "BDT" {
		t.Errorf("expected LocalCurrency=BDT, got %s", intent.LocalCurrency)
	}
	// amountLocal = (100 cents / 100) * 115.500000 (as float approx)
	// = 1.00 USD * 115.50 rate * 100 paisa/BDT = 11550 paisa
	if intent.AmountLocal <= 0 {
		t.Errorf("expected positive AmountLocal for BD rail, got %d", intent.AmountLocal)
	}
}

func TestInitiateCheckout_RejectsMissingBillingProfile(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US"},
		billingErr:     profiles.ErrNotFound,
	}
	fxProv := &stubFXProvider{}
	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{RailStripe: newStubRail(RailStripe)})

	_, err := svc.InitiateCheckout(context.Background(), uuid.New(), RailStripe, 100_000, "https://app.example.com", "idem-002")
	if !errors.Is(err, ErrBillingProfileRequired) {
		t.Errorf("expected ErrBillingProfileRequired, got %v", err)
	}
}

func TestInitiateCheckout_RejectsEmptyBillingContactName(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US"},
		billingProfile: profiles.BillingProfile{
			BillingContactName: "", // empty = incomplete
			CountryCode:        "US",
		},
	}
	fxProv := &stubFXProvider{}
	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{RailStripe: newStubRail(RailStripe)})

	_, err := svc.InitiateCheckout(context.Background(), uuid.New(), RailStripe, 100_000, "https://app.example.com", "idem-003")
	if !errors.Is(err, ErrBillingProfileRequired) {
		t.Errorf("expected ErrBillingProfileRequired, got %v", err)
	}
}

func TestInitiateCheckout_RejectsInvalidCredits(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US"},
		billingProfile: profiles.BillingProfile{BillingContactName: "John", CountryCode: "US"},
	}
	fxProv := &stubFXProvider{}
	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{RailStripe: newStubRail(RailStripe)})

	_, err := svc.InitiateCheckout(context.Background(), uuid.New(), RailStripe, 500, "https://app.example.com", "idem-004")
	if err == nil {
		t.Error("expected error for non-multiple-of-1000 credits, got nil")
	}
}

func TestInitiateCheckout_RejectsUnavailableRail(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US"},
		billingProfile: profiles.BillingProfile{BillingContactName: "John", CountryCode: "US"},
	}
	fxProv := &stubFXProvider{}
	// US account trying to use bkash (BD only)
	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{
		RailStripe: newStubRail(RailStripe),
		RailBkash:  newStubRail(RailBkash),
	})

	_, err := svc.InitiateCheckout(context.Background(), uuid.New(), RailBkash, 100_000, "https://app.example.com", "idem-005")
	if err == nil {
		t.Error("expected error for unavailable rail (bkash for US account), got nil")
	}
}

func TestHandleProviderEvent_StripeSuccess_PostsGrant(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US"},
		billingProfile: profiles.BillingProfile{BillingContactName: "John", CountryCode: "US"},
	}
	fxProv := &stubFXProvider{}

	intentID := uuid.New()
	accountID := uuid.New()
	providerIntentID := "prov_stripe_001"

	// Pre-insert an intent in pending_redirect status.
	intent := PaymentIntent{
		ID:               intentID,
		AccountID:        accountID,
		Rail:             RailStripe,
		Status:           IntentStatusPendingRedirect,
		Credits:          100_000,
		AmountUSD:        100,
		IdempotencyKey:   "idem-stripe-001",
		ProviderIntentID: providerIntentID,
		Metadata:         map[string]any{},
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	_ = repo.InsertPaymentIntent(context.Background(), intent)
	repo.byProv[providerIntentID] = intentID

	stripeRail := newStubRail(RailStripe)
	stripeRail.eventResult = RailEvent{
		ProviderIntentID: providerIntentID,
		EventType:        "payment.succeeded",
		RawPayload:       []byte(`{}`),
	}

	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{RailStripe: stripeRail})
	err := svc.HandleProviderEvent(context.Background(), RailStripe, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updatedIntent, _ := repo.GetPaymentIntent(context.Background(), intentID)
	if updatedIntent.Status != IntentStatusCompleted {
		t.Errorf("expected status completed, got %s", updatedIntent.Status)
	}

	expectedKey := fmt.Sprintf("payment:purchase:%s", intentID)
	if led.callCount() != 1 {
		t.Errorf("expected 1 ledger grant call, got %d", led.callCount())
	}
	if led.calls[0].idempotencyKey != expectedKey {
		t.Errorf("expected idempotency key %s, got %s", expectedKey, led.calls[0].idempotencyKey)
	}
}

func TestHandleProviderEvent_BkashSuccess_TransitionsToConfirming(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "BD"},
		billingProfile: profiles.BillingProfile{BillingContactName: "Rahim", CountryCode: "BD"},
	}
	fxProv := &stubFXProvider{}

	intentID := uuid.New()
	providerIntentID := "prov_bkash_001"

	intent := PaymentIntent{
		ID:               intentID,
		AccountID:        uuid.New(),
		Rail:             RailBkash,
		Status:           IntentStatusPendingRedirect,
		Credits:          50_000,
		AmountUSD:        50,
		IdempotencyKey:   "idem-bkash-001",
		ProviderIntentID: providerIntentID,
		Metadata:         map[string]any{},
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	_ = repo.InsertPaymentIntent(context.Background(), intent)
	repo.byProv[providerIntentID] = intentID

	bkashRail := newStubRail(RailBkash)
	bkashRail.eventResult = RailEvent{
		ProviderIntentID: providerIntentID,
		EventType:        "payment.succeeded",
		RawPayload:       []byte(`{}`),
	}

	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{RailBkash: bkashRail})
	err := svc.HandleProviderEvent(context.Background(), RailBkash, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updatedIntent, _ := repo.GetPaymentIntent(context.Background(), intentID)
	if updatedIntent.Status != IntentStatusConfirming {
		t.Errorf("expected status confirming, got %s", updatedIntent.Status)
	}
	if updatedIntent.ConfirmingAt == nil {
		t.Error("expected confirming_at to be set")
	}
	// No ledger grant for BD rails — only confirming
	if led.callCount() != 0 {
		t.Errorf("expected 0 ledger grant calls for BD confirming, got %d", led.callCount())
	}
}

func TestHandleProviderEvent_Failed_TransitionsToFailed(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{}
	fxProv := &stubFXProvider{}

	intentID := uuid.New()
	providerIntentID := "prov_fail_001"

	intent := PaymentIntent{
		ID:               intentID,
		AccountID:        uuid.New(),
		Rail:             RailStripe,
		Status:           IntentStatusPendingRedirect,
		Credits:          10_000,
		AmountUSD:        10,
		IdempotencyKey:   "idem-fail-001",
		ProviderIntentID: providerIntentID,
		Metadata:         map[string]any{},
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	_ = repo.InsertPaymentIntent(context.Background(), intent)
	repo.byProv[providerIntentID] = intentID

	stripeRail := newStubRail(RailStripe)
	stripeRail.eventResult = RailEvent{
		ProviderIntentID: providerIntentID,
		EventType:        "payment.failed",
		RawPayload:       []byte(`{}`),
	}

	svc := buildService(repo, led, prof, fxProv, map[Rail]PaymentRail{RailStripe: stripeRail})
	err := svc.HandleProviderEvent(context.Background(), RailStripe, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updatedIntent, _ := repo.GetPaymentIntent(context.Background(), intentID)
	if updatedIntent.Status != IntentStatusFailed {
		t.Errorf("expected status failed, got %s", updatedIntent.Status)
	}
	if led.callCount() != 0 {
		t.Error("expected no ledger grants on payment.failed")
	}
}

func TestConfirmPendingBDPayments_PostsGrantAfterDelay(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{}
	fxProv := &stubFXProvider{}

	intentID := uuid.New()
	oldConfirmingAt := time.Now().Add(-4 * time.Minute)

	intent := PaymentIntent{
		ID:             intentID,
		AccountID:      uuid.New(),
		Rail:           RailBkash,
		Status:         IntentStatusConfirming,
		Credits:        10_000,
		AmountUSD:      10,
		IdempotencyKey: "idem-confirm-001",
		ConfirmingAt:   &oldConfirmingAt,
		Metadata:       map[string]any{},
		CreatedAt:      time.Now().Add(-10 * time.Minute),
		UpdatedAt:      time.Now().Add(-4 * time.Minute),
	}
	_ = repo.InsertPaymentIntent(context.Background(), intent)

	svc := buildService(repo, led, prof, fxProv, nil)
	count, err := svc.ConfirmPendingBDPayments(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 confirmed intent, got %d", count)
	}

	updatedIntent, _ := repo.GetPaymentIntent(context.Background(), intentID)
	if updatedIntent.Status != IntentStatusCompleted {
		t.Errorf("expected status completed, got %s", updatedIntent.Status)
	}
	if led.callCount() != 1 {
		t.Errorf("expected 1 ledger grant, got %d", led.callCount())
	}
}

func TestPostPurchaseGrant_IdempotentOnDuplicate(t *testing.T) {
	repo := newStubRepository()
	led := &stubLedger{}
	prof := &stubProfiles{}
	fxProv := &stubFXProvider{}

	svc := buildService(repo, led, prof, fxProv, nil)

	intent := PaymentIntent{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Credits:   100_000,
		Metadata:  map[string]any{},
	}

	// Call twice — should grant only once.
	err := svc.PostPurchaseGrant(context.Background(), intent)
	if err != nil {
		t.Fatalf("first grant: %v", err)
	}
	err = svc.PostPurchaseGrant(context.Background(), intent)
	if err != nil {
		t.Fatalf("second grant: %v", err)
	}

	if led.callCount() != 1 {
		t.Errorf("expected idempotent: 1 ledger call, got %d", led.callCount())
	}
}
