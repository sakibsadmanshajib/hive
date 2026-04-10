package bkash_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
	bkashRail "github.com/hivegpt/hive/apps/control-plane/internal/payments/bkash"
)

// bkashServer sets up an httptest.Server that handles the three bKash endpoints.
// It returns the server, and records requests for assertion.
type bkashServer struct {
	server            *httptest.Server
	grantRequests     []*http.Request
	createRequests    []*http.Request
	createBodies      []map[string]string
	executeRequests   []*http.Request
	executeBodies     []map[string]string

	// configurable responses
	grantIDToken       string
	createPaymentID    string
	createBkashURL     string
	executeStatus      string
}

func newBkashServer(t *testing.T) *bkashServer {
	t.Helper()
	bs := &bkashServer{
		grantIDToken:    "test_id_token_123",
		createPaymentID: "bkash_pay_abc123",
		createBkashURL:  "https://sandbox.bkash.com/pay/bkash_pay_abc123",
		executeStatus:   "Completed",
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/tokenized/checkout/token/grant", func(w http.ResponseWriter, r *http.Request) {
		bs.grantRequests = append(bs.grantRequests, r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id_token": bs.grantIDToken,
		})
	})

	mux.HandleFunc("/tokenized/checkout/create", func(w http.ResponseWriter, r *http.Request) {
		bs.createRequests = append(bs.createRequests, r)
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]string
		_ = json.Unmarshal(body, &parsed)
		bs.createBodies = append(bs.createBodies, parsed)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"paymentID": bs.createPaymentID,
			"bkashURL":  bs.createBkashURL,
		})
	})

	mux.HandleFunc("/tokenized/checkout/execute", func(w http.ResponseWriter, r *http.Request) {
		bs.executeRequests = append(bs.executeRequests, r)
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]string
		_ = json.Unmarshal(body, &parsed)
		bs.executeBodies = append(bs.executeBodies, parsed)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"transactionStatus": bs.executeStatus,
		})
	})

	bs.server = httptest.NewServer(mux)
	t.Cleanup(bs.server.Close)
	return bs
}

func (bs *bkashServer) newRail() *bkashRail.Rail {
	return bkashRail.NewRail(
		bs.server.Client(),
		bs.server.URL,
		"test_app_key",
		"test_app_secret",
		"test_username",
		"test_password",
	)
}

func defaultInput() payments.InitiateInput {
	return payments.InitiateInput{
		PaymentIntentID: uuid.MustParse("123e4567-e89b-12d3-a456-426614174000"),
		AccountID:       uuid.MustParse("223e4567-e89b-12d3-a456-426614174001"),
		Credits:         10000,
		AmountUSD:       100,
		AmountLocal:     150000, // 1500.00 BDT in paisa
		Currency:        "BDT",
		CallbackBaseURL: "https://example.com",
		CustomerName:    "Test User",
		CustomerEmail:   "test@example.com",
	}
}

func TestBkashRailName(t *testing.T) {
	bs := newBkashServer(t)
	rail := bs.newRail()
	if rail.RailName() != payments.RailBkash {
		t.Errorf("expected %q got %q", payments.RailBkash, rail.RailName())
	}
}

func TestBkashInitiate_GrantsTokenAndCreatesPayment(t *testing.T) {
	bs := newBkashServer(t)
	rail := bs.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := rail.Initiate(ctx, defaultInput())
	if err != nil {
		t.Fatalf("Initiate returned error: %v", err)
	}

	if len(bs.grantRequests) != 1 {
		t.Errorf("expected 1 grant request, got %d", len(bs.grantRequests))
	}
	if len(bs.createRequests) != 1 {
		t.Errorf("expected 1 create request, got %d", len(bs.createRequests))
	}
	if result.RedirectURL != bs.createBkashURL {
		t.Errorf("expected redirect URL %q, got %q", bs.createBkashURL, result.RedirectURL)
	}
}

func TestBkashInitiate_UsesPaymentIntentIDAsInvoiceNumber(t *testing.T) {
	bs := newBkashServer(t)
	rail := bs.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := defaultInput()
	_, err := rail.Initiate(ctx, input)
	if err != nil {
		t.Fatalf("Initiate returned error: %v", err)
	}

	if len(bs.createBodies) == 0 {
		t.Fatal("no create request body captured")
	}
	body := bs.createBodies[0]
	if body["merchantInvoiceNumber"] != input.PaymentIntentID.String() {
		t.Errorf("expected merchantInvoiceNumber %q, got %q",
			input.PaymentIntentID.String(), body["merchantInvoiceNumber"])
	}
}

func TestBkashInitiate_ReturnsPaymentIDAsProviderIntentID(t *testing.T) {
	bs := newBkashServer(t)
	bs.createPaymentID = "bkash_pay_unique_xyz"
	rail := bs.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := rail.Initiate(ctx, defaultInput())
	if err != nil {
		t.Fatalf("Initiate returned error: %v", err)
	}

	// ProviderIntentID must be bKash's paymentID, NOT merchantInvoiceNumber (Hive UUID)
	if result.ProviderIntentID != "bkash_pay_unique_xyz" {
		t.Errorf("expected ProviderIntentID %q (bKash paymentID), got %q",
			"bkash_pay_unique_xyz", result.ProviderIntentID)
	}
	if result.ProviderIntentID == defaultInput().PaymentIntentID.String() {
		t.Error("ProviderIntentID must not equal merchantInvoiceNumber (Hive UUID)")
	}
}

func TestBkashInitiate_FormatsAmountAsBDTDecimal(t *testing.T) {
	bs := newBkashServer(t)
	rail := bs.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := defaultInput()
	input.AmountLocal = 150000 // 1500.00 BDT in paisa

	_, err := rail.Initiate(ctx, input)
	if err != nil {
		t.Fatalf("Initiate returned error: %v", err)
	}

	if len(bs.createBodies) == 0 {
		t.Fatal("no create request body captured")
	}
	if bs.createBodies[0]["amount"] != "1500.00" {
		t.Errorf("expected amount %q, got %q", "1500.00", bs.createBodies[0]["amount"])
	}
}

func TestBkashProcessEvent_ExecutesServerSide(t *testing.T) {
	bs := newBkashServer(t)
	rail := bs.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	callbackBody, _ := json.Marshal(map[string]string{
		"paymentID": "bkash_pay_abc123",
		"status":    "success",
	})

	_, err := rail.ProcessEvent(ctx, callbackBody, nil)
	if err != nil {
		t.Fatalf("ProcessEvent returned error: %v", err)
	}

	// Must have called execute endpoint server-side
	if len(bs.executeRequests) != 1 {
		t.Errorf("expected 1 execute request, got %d (server-side verification required)", len(bs.executeRequests))
	}
}

func TestBkashProcessEvent_CompletedMapsToSucceeded(t *testing.T) {
	bs := newBkashServer(t)
	bs.executeStatus = "Completed"
	rail := bs.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	callbackBody, _ := json.Marshal(map[string]string{
		"paymentID": "bkash_pay_abc123",
		"status":    "success",
	})

	event, err := rail.ProcessEvent(ctx, callbackBody, nil)
	if err != nil {
		t.Fatalf("ProcessEvent returned error: %v", err)
	}

	if event.EventType != "payment.succeeded" {
		t.Errorf("expected payment.succeeded, got %q", event.EventType)
	}
}

func TestBkashProcessEvent_ReturnsPaymentIDAsProviderIntentID(t *testing.T) {
	bs := newBkashServer(t)
	rail := bs.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const paymentID = "bkash_pay_abc123"
	callbackBody, _ := json.Marshal(map[string]string{
		"paymentID": paymentID,
		"status":    "success",
	})

	event, err := rail.ProcessEvent(ctx, callbackBody, nil)
	if err != nil {
		t.Fatalf("ProcessEvent returned error: %v", err)
	}

	// ProviderIntentID must be bKash paymentID, NOT merchantInvoiceNumber (Hive UUID)
	if event.ProviderIntentID != paymentID {
		t.Errorf("expected ProviderIntentID %q (bKash paymentID), got %q", paymentID, event.ProviderIntentID)
	}
	// Must not be a UUID (which would be the merchantInvoiceNumber/Hive ID)
	if strings.Contains(event.ProviderIntentID, "-") && len(event.ProviderIntentID) == 36 {
		t.Error("ProviderIntentID looks like a UUID — it should be the bKash paymentID, not merchantInvoiceNumber")
	}
}

func TestBkashProcessEvent_FailedMapsToFailed(t *testing.T) {
	bs := newBkashServer(t)
	bs.executeStatus = "Initiated" // not "Completed"
	rail := bs.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	callbackBody, _ := json.Marshal(map[string]string{
		"paymentID": "bkash_pay_abc123",
		"status":    "failure",
	})

	event, err := rail.ProcessEvent(ctx, callbackBody, nil)
	if err != nil {
		t.Fatalf("ProcessEvent returned error: %v", err)
	}

	if event.EventType != "payment.failed" {
		t.Errorf("expected payment.failed, got %q", event.EventType)
	}
}
