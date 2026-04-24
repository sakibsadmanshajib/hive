package sslcommerz_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
	sslRail "github.com/hivegpt/hive/apps/control-plane/internal/payments/sslcommerz"
)

// sslServer sets up an httptest.Server that handles SSLCommerz initiation and validation endpoints.
type sslServer struct {
	server           *httptest.Server
	initiateRequests []*http.Request
	initiateBodies   []url.Values
	validateRequests []*http.Request
	validateQueries  []url.Values

	// configurable responses
	initiateStatus      string
	initiateSessionkey  string
	initiateGatewayURL  string
	validateStatus      string
}

func newSSLServer(t *testing.T) *sslServer {
	t.Helper()
	ss := &sslServer{
		initiateStatus:     "SUCCESS",
		initiateSessionkey: "ssl_session_xyz789",
		initiateGatewayURL: "https://sandbox.sslcommerz.com/EasyCheckOut/testcdeasyssl_session_xyz789",
		validateStatus:     "VALID",
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/gwprocess/v4/api.php", func(w http.ResponseWriter, r *http.Request) {
		ss.initiateRequests = append(ss.initiateRequests, r)
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		ss.initiateBodies = append(ss.initiateBodies, vals)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":         ss.initiateStatus,
			"GatewayPageURL": ss.initiateGatewayURL,
			"sessionkey":     ss.initiateSessionkey,
		})
	})

	mux.HandleFunc("/validator/api/validationserverAPI.php", func(w http.ResponseWriter, r *http.Request) {
		ss.validateRequests = append(ss.validateRequests, r)
		ss.validateQueries = append(ss.validateQueries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": ss.validateStatus,
		})
	})

	ss.server = httptest.NewServer(mux)
	t.Cleanup(ss.server.Close)
	return ss
}

func (ss *sslServer) newRail() *sslRail.Rail {
	return sslRail.NewRail(
		ss.server.Client(),
		ss.server.URL,
		"test_store_id",
		"test_store_passwd",
	)
}

func defaultSSLInput() payments.InitiateInput {
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

func TestSSLCommerzRailName(t *testing.T) {
	ss := newSSLServer(t)
	rail := ss.newRail()
	if rail.RailName() != payments.RailSSLCommerz {
		t.Errorf("expected %q got %q", payments.RailSSLCommerz, rail.RailName())
	}
}

func TestSSLCommerzInitiate_SendsFormEncodedRequest(t *testing.T) {
	ss := newSSLServer(t)
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := rail.Initiate(ctx, defaultSSLInput())
	if err != nil {
		t.Fatalf("Initiate returned error: %v", err)
	}

	if len(ss.initiateRequests) != 1 {
		t.Fatalf("expected 1 initiate request, got %d", len(ss.initiateRequests))
	}
	req := ss.initiateRequests[0]
	if !strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		t.Errorf("expected form-encoded Content-Type, got %q", req.Header.Get("Content-Type"))
	}
	if result.RedirectURL != ss.initiateGatewayURL {
		t.Errorf("expected redirect URL %q, got %q", ss.initiateGatewayURL, result.RedirectURL)
	}

	body := ss.initiateBodies[0]
	// Check required fields present
	for _, field := range []string{"store_id", "store_passwd", "total_amount", "currency",
		"tran_id", "success_url", "fail_url", "cancel_url", "ipn_url",
		"cus_name", "cus_email", "product_profile"} {
		if body.Get(field) == "" {
			t.Errorf("expected form field %q to be set", field)
		}
	}
	if body.Get("product_profile") != "digital-goods" {
		t.Errorf("expected product_profile %q, got %q", "digital-goods", body.Get("product_profile"))
	}
}

func TestSSLCommerzInitiate_UsesPaymentIntentIDAsTranID(t *testing.T) {
	ss := newSSLServer(t)
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := defaultSSLInput()
	_, err := rail.Initiate(ctx, input)
	if err != nil {
		t.Fatalf("Initiate returned error: %v", err)
	}

	if len(ss.initiateBodies) == 0 {
		t.Fatal("no initiate request body captured")
	}
	if ss.initiateBodies[0].Get("tran_id") != input.PaymentIntentID.String() {
		t.Errorf("expected tran_id %q, got %q",
			input.PaymentIntentID.String(), ss.initiateBodies[0].Get("tran_id"))
	}
}

func TestSSLCommerzInitiate_ReturnsSessionkeyAsProviderIntentID(t *testing.T) {
	ss := newSSLServer(t)
	ss.initiateSessionkey = "ssl_session_unique_abc"
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := rail.Initiate(ctx, defaultSSLInput())
	if err != nil {
		t.Fatalf("Initiate returned error: %v", err)
	}

	// ProviderIntentID must be SSLCommerz sessionkey, NOT tran_id (Hive UUID)
	if result.ProviderIntentID != "ssl_session_unique_abc" {
		t.Errorf("expected ProviderIntentID %q (sessionkey), got %q",
			"ssl_session_unique_abc", result.ProviderIntentID)
	}
	if result.ProviderIntentID == defaultSSLInput().PaymentIntentID.String() {
		t.Error("ProviderIntentID must not equal tran_id (Hive UUID)")
	}
}

func TestSSLCommerzInitiate_FormatsAmountAsBDTDecimal(t *testing.T) {
	ss := newSSLServer(t)
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := defaultSSLInput()
	input.AmountLocal = 150000 // 1500.00 BDT in paisa

	_, err := rail.Initiate(ctx, input)
	if err != nil {
		t.Fatalf("Initiate returned error: %v", err)
	}

	if len(ss.initiateBodies) == 0 {
		t.Fatal("no initiate body captured")
	}
	if ss.initiateBodies[0].Get("total_amount") != "1500.00" {
		t.Errorf("expected total_amount %q, got %q", "1500.00", ss.initiateBodies[0].Get("total_amount"))
	}
}

func TestSSLCommerzProcessEvent_ValidatesViaServerAPI(t *testing.T) {
	ss := newSSLServer(t)
	ss.validateStatus = "VALID"
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// IPN form body without hash fields (hash verification skipped when verify_key is absent)
	ipnBody := url.Values{}
	ipnBody.Set("val_id", "val_test_123")
	ipnBody.Set("sessionkey", "ssl_session_xyz789")
	ipnBody.Set("tran_id", "123e4567-e89b-12d3-a456-426614174000")
	ipnBody.Set("amount", "1500.00")
	ipnBody.Set("currency", "BDT")
	ipnBody.Set("status", "VALID")

	event, err := rail.ProcessEvent(ctx, []byte(ipnBody.Encode()), nil)
	if err != nil {
		t.Fatalf("ProcessEvent returned error: %v", err)
	}

	// Must have called validation API
	if len(ss.validateRequests) != 1 {
		t.Errorf("expected 1 validation API request, got %d", len(ss.validateRequests))
	}
	if event.EventType != "payment.succeeded" {
		t.Errorf("expected payment.succeeded, got %q", event.EventType)
	}
}

func TestSSLCommerzProcessEvent_ReturnsSessionkeyAsProviderIntentID(t *testing.T) {
	ss := newSSLServer(t)
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const sessionkey = "ssl_session_xyz789"
	ipnBody := url.Values{}
	ipnBody.Set("val_id", "val_test_456")
	ipnBody.Set("sessionkey", sessionkey)
	ipnBody.Set("tran_id", "123e4567-e89b-12d3-a456-426614174000")
	ipnBody.Set("amount", "1500.00")
	ipnBody.Set("currency", "BDT")
	ipnBody.Set("status", "VALID")

	event, err := rail.ProcessEvent(ctx, []byte(ipnBody.Encode()), nil)
	if err != nil {
		t.Fatalf("ProcessEvent returned error: %v", err)
	}

	// ProviderIntentID must be SSLCommerz sessionkey, NOT tran_id (Hive UUID)
	if event.ProviderIntentID != sessionkey {
		t.Errorf("expected ProviderIntentID %q (sessionkey), got %q", sessionkey, event.ProviderIntentID)
	}
}

func TestSSLCommerzProcessEvent_FailedValidation(t *testing.T) {
	ss := newSSLServer(t)
	ss.validateStatus = "FAILED"
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ipnBody := url.Values{}
	ipnBody.Set("val_id", "val_fail_789")
	ipnBody.Set("sessionkey", "ssl_session_xyz789")
	ipnBody.Set("tran_id", "123e4567-e89b-12d3-a456-426614174000")
	ipnBody.Set("amount", "1500.00")
	ipnBody.Set("currency", "BDT")
	ipnBody.Set("status", "FAILED")

	event, err := rail.ProcessEvent(ctx, []byte(ipnBody.Encode()), nil)
	if err != nil {
		t.Fatalf("ProcessEvent returned error: %v", err)
	}

	if event.EventType != "payment.failed" {
		t.Errorf("expected payment.failed, got %q", event.EventType)
	}
}
