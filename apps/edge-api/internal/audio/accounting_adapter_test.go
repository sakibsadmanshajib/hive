package audio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

func TestAccountingAdapterCreateReservationUsesStrictPolicy(t *testing.T) {
	t.Helper()

	var got inference.CreateReservationInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/accounting/reservations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode reservation request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(inference.ReservationResult{
			ID:               "reservation-1",
			AccountID:        "account-1",
			Status:           "active",
			EstimatedCredits: 1000,
		}); err != nil {
			t.Fatalf("encode reservation response: %v", err)
		}
	}))
	defer server.Close()

	adapter := NewAccountingAdapter(inference.NewAccountingClient(server.URL))

	reservationID, err := adapter.CreateReservation(context.Background(), ReservationInput{
		AccountID:        "account-1",
		APIKeyID:         "11111111-1111-1111-1111-111111111111",
		RequestID:        "req-1",
		Endpoint:         "/v1/audio/speech",
		ModelAlias:       "hive-auto",
		EstimatedCredits: 1000,
	})
	if err != nil {
		t.Fatalf("CreateReservation() error = %v", err)
	}
	if reservationID != "reservation-1" {
		t.Fatalf("CreateReservation() reservationID = %q, want %q", reservationID, "reservation-1")
	}
	if got.PolicyMode != "strict" {
		t.Fatalf("CreateReservation() policy_mode = %q, want %q", got.PolicyMode, "strict")
	}
	if got.ModelAlias != "hive-auto" {
		t.Fatalf("CreateReservation() model_alias = %q, want %q", got.ModelAlias, "hive-auto")
	}
	if got.APIKeyID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("CreateReservation() api_key_id = %q, want %q", got.APIKeyID, "11111111-1111-1111-1111-111111111111")
	}
	if got.EstimatedCredits != 1000 {
		t.Fatalf("CreateReservation() estimated_credits = %d, want %d", got.EstimatedCredits, 1000)
	}
}

// Control-plane answers 409 (accounting.PolicyError) when the credit policy
// refuses a hold, and 5xx or a timeout when it simply cannot answer. Only the
// former may surface as insufficient credits.
func TestAccountingAdapterClassifiesReservationFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantQuota bool
	}{
		{name: "policy refusal", status: http.StatusConflict, wantQuota: true},
		{name: "control plane error", status: http.StatusInternalServerError, wantQuota: false},
		{name: "internal auth rejected", status: http.StatusUnauthorized, wantQuota: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"reservation exceeds available credits"}`))
			}))
			defer server.Close()

			adapter := NewAccountingAdapter(inference.NewAccountingClient(server.URL))
			_, err := adapter.CreateReservation(context.Background(), ReservationInput{
				AccountID:        "account-1",
				APIKeyID:         "11111111-1111-1111-1111-111111111111",
				RequestID:        "req-1",
				Endpoint:         "/v1/audio/speech",
				ModelAlias:       "hive-tts",
				EstimatedCredits: 1000,
			})
			if err == nil {
				t.Fatal("CreateReservation() error = nil, want failure")
			}
			if got := errors.Is(err, ErrInsufficientCredits); got != tc.wantQuota {
				t.Fatalf("errors.Is(err, ErrInsufficientCredits) = %v, want %v (err = %v)", got, tc.wantQuota, err)
			}
		})
	}
}
