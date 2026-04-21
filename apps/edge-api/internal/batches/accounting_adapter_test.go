package batches

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hivegpt/hive/apps/edge-api/internal/inference"
)

func TestAccountingAdapterCreateReservationUsesStrictPolicyAndModelAlias(t *testing.T) {
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

	input := ReservationInput{
		AccountID:        "account-1",
		APIKeyID:         "11111111-1111-1111-1111-111111111111",
		RequestID:        "batch-file-input",
		Endpoint:         "/v1/chat/completions",
		EstimatedCredits: 1000,
	}
	modelAliasField := reflect.ValueOf(&input).Elem().FieldByName("ModelAlias")
	if !modelAliasField.IsValid() {
		t.Fatalf("reservation input is missing ModelAlias field")
	}
	modelAliasField.SetString("hive-fast")

	reservationID, err := adapter.CreateReservation(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateReservation() error = %v", err)
	}
	if reservationID != "reservation-1" {
		t.Fatalf("CreateReservation() reservationID = %q, want %q", reservationID, "reservation-1")
	}
	if got.ModelAlias != "hive-fast" {
		t.Fatalf("CreateReservation() model_alias = %q, want %q", got.ModelAlias, "hive-fast")
	}
	if got.PolicyMode != "strict" {
		t.Fatalf("CreateReservation() policy_mode = %q, want %q", got.PolicyMode, "strict")
	}
}
