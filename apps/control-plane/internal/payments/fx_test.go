package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubFXRepo is a minimal in-memory repository for FX snapshot tests.
// It only implements the FX-related methods; all others are no-ops.
type stubFXRepo struct {
	snapshots map[uuid.UUID]FXSnapshot
}

func newStubFXRepo() *stubFXRepo {
	return &stubFXRepo{snapshots: make(map[uuid.UUID]FXSnapshot)}
}

func (r *stubFXRepo) InsertPaymentIntent(_ context.Context, _ PaymentIntent) error { return nil }
func (r *stubFXRepo) GetPaymentIntent(_ context.Context, _ uuid.UUID) (PaymentIntent, error) {
	return PaymentIntent{}, ErrIntentNotFound
}
func (r *stubFXRepo) GetPaymentIntentByProviderID(_ context.Context, _ string) (PaymentIntent, error) {
	return PaymentIntent{}, ErrIntentNotFound
}
func (r *stubFXRepo) CompareAndSetStatus(_ context.Context, _ uuid.UUID, _, _ IntentStatus) (bool, error) {
	return false, nil
}
func (r *stubFXRepo) UpdateProviderDetails(_ context.Context, _ uuid.UUID, _, _ string, _ *time.Time) error {
	return nil
}
func (r *stubFXRepo) SetConfirmingAt(_ context.Context, _ uuid.UUID, _ time.Time) error { return nil }
func (r *stubFXRepo) ListConfirmingIntents(_ context.Context, _ time.Time) ([]PaymentIntent, error) {
	return nil, nil
}
func (r *stubFXRepo) InsertPaymentEvent(_ context.Context, _ PaymentEvent) error { return nil }
func (r *stubFXRepo) InsertFXSnapshot(_ context.Context, snap FXSnapshot) error {
	r.snapshots[snap.ID] = snap
	return nil
}
func (r *stubFXRepo) GetFXSnapshot(_ context.Context, id uuid.UUID) (FXSnapshot, error) {
	s, ok := r.snapshots[id]
	if !ok {
		return FXSnapshot{}, ErrIntentNotFound
	}
	return s, nil
}

// memFXCache is an in-memory FXCache for tests (no real Redis needed).
type memFXCache struct {
	data map[string]string
}

func newMemFXCache() *memFXCache {
	return &memFXCache{data: make(map[string]string)}
}

func (m *memFXCache) Get(_ context.Context, key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", errors.New("cache miss")
	}
	return v, nil
}

func (m *memFXCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	m.data[key] = value
	return nil
}

// xeResponseJSON builds a minimal XE API JSON response.
func xeResponseJSON(midRate string) []byte {
	type xeTo struct {
		Quotecurrency string  `json:"quotecurrency"`
		Mid           float64 `json:"mid"`
	}
	type xeResp struct {
		To []xeTo `json:"to"`
	}
	resp := xeResp{To: []xeTo{{Quotecurrency: "BDT", Mid: 0}}}
	// Encode the mid rate as a raw number in the response.
	raw, _ := json.Marshal(struct {
		To []json.RawMessage `json:"to"`
	}{
		To: []json.RawMessage{[]byte(`{"quotecurrency":"BDT","mid":` + midRate + `}`)},
	})
	_ = resp
	return raw
}

func TestFetchUSDToBDT_AdminOverrideTakesPrecedence(t *testing.T) {
	svc := NewFXService(http.DefaultClient, "acc", "key", nil)
	svc.SetAdminOverride("115.00")

	rate, source, err := svc.FetchUSDToBDT(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate != "115.00" {
		t.Errorf("expected rate 115.00, got %s", rate)
	}
	if source != "admin_override" {
		t.Errorf("expected source admin_override, got %s", source)
	}
}

func TestFetchUSDToBDT_XEAPISuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(xeResponseJSON("110.25"))
	}))
	defer ts.Close()

	cache := newMemFXCache()
	svc := newFXServiceWithBaseURL(http.DefaultClient, "acc", "key", cache, ts.URL)

	rate, source, err := svc.FetchUSDToBDT(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate != "110.25" {
		t.Errorf("expected rate 110.25, got %s", rate)
	}
	if source != "xe" {
		t.Errorf("expected source xe, got %s", source)
	}
}

func TestFetchUSDToBDT_XEFailsFallsBackToCache(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	// Pre-populate in-memory cache.
	cache := newMemFXCache()
	_ = cache.Set(context.Background(), "fx:usd_bdt:mid_rate", "108.50", 0)

	svc := newFXServiceWithBaseURL(http.DefaultClient, "acc", "key", cache, ts.URL)
	rate, source, err := svc.FetchUSDToBDT(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate != "108.50" {
		t.Errorf("expected cached rate 108.50, got %s", rate)
	}
	if source != "cache" {
		t.Errorf("expected source cache, got %s", source)
	}
}

func TestFetchUSDToBDT_AllSourcesFailReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	// Empty cache — no cached value.
	cache := newMemFXCache()
	svc := newFXServiceWithBaseURL(http.DefaultClient, "acc", "key", cache, ts.URL)

	_, _, err := svc.FetchUSDToBDT(context.Background())
	if err != ErrFXUnavailable {
		t.Errorf("expected ErrFXUnavailable, got %v", err)
	}
}

func TestCreateSnapshot_ComputesEffectiveRateWith5PercentFee(t *testing.T) {
	// Use admin override so no external calls needed
	svc := NewFXService(http.DefaultClient, "acc", "key", nil)
	svc.SetAdminOverride("110.00")

	repo := newStubFXRepo()
	snap, err := svc.CreateSnapshot(context.Background(), repo, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.MidRate != "110.00" {
		t.Errorf("expected mid_rate 110.00, got %s", snap.MidRate)
	}
	// effectiveRate = 110.00 * 1.05 = 115.500000
	if snap.EffectiveRate != "115.500000" {
		t.Errorf("expected effective_rate 115.500000, got %s", snap.EffectiveRate)
	}
	if snap.SourceAPI != "admin_override" {
		t.Errorf("expected source_api admin_override, got %s", snap.SourceAPI)
	}
	if snap.FeeRate != "0.05" {
		t.Errorf("expected fee_rate 0.05, got %s", snap.FeeRate)
	}
}
