package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

// PaymentService is the interface the Handler uses to interact with payments.
type PaymentService interface {
	GetCheckoutOptions(ctx context.Context, accountID uuid.UUID) (*CheckoutOptions, error)
	InitiateCheckout(ctx context.Context, accountID uuid.UUID, rail Rail, credits int64, callbackBaseURL, idempotencyKey string) (*PaymentIntent, error)
	HandleProviderEvent(ctx context.Context, rail Rail, rawBody []byte, headers map[string]string) error
}

// AccountResolver resolves the current account ID from the request context.
// Follows the "accept interfaces" pattern so tests can inject stubs.
type AccountResolver interface {
	EnsureViewerContext(ctx context.Context) (uuid.UUID, error)
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

// Handler handles payments HTTP routes: checkout initiation, rail listing, and provider webhooks.
type Handler struct {
	svc         PaymentService
	accountsSvc AccountResolver
}

// NewHandler constructs a payments Handler.
func NewHandler(svc PaymentService, accountsSvc AccountResolver) *Handler {
	return &Handler{svc: svc, accountsSvc: accountsSvc}
}

// ServeHTTP dispatches to the appropriate sub-handler based on path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/accounts/current/checkout/rails":
		h.handleGetRails(w, r)
	case "/api/v1/accounts/current/checkout/initiate":
		h.handleInitiateCheckout(w, r)
	case "/webhooks/stripe":
		h.handleWebhook(w, r, RailStripe)
	case "/webhooks/bkash/callback":
		h.handleWebhook(w, r, RailBkash)
	case "/webhooks/sslcommerz/ipn",
		"/webhooks/sslcommerz/success",
		"/webhooks/sslcommerz/fail",
		"/webhooks/sslcommerz/cancel":
		h.handleWebhook(w, r, RailSSLCommerz)
	default:
		writePaymentJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// ---------------------------------------------------------------------------
// handleGetRails — GET /api/v1/accounts/current/checkout/rails (authenticated)
// ---------------------------------------------------------------------------

func (h *Handler) handleGetRails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writePaymentJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	accountID, err := h.accountsSvc.EnsureViewerContext(r.Context())
	if err != nil {
		writePaymentJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	opts, err := h.svc.GetCheckoutOptions(r.Context(), accountID)
	if err != nil {
		writePaymentJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to get checkout options: %v", err)})
		return
	}

	writePaymentJSON(w, http.StatusOK, opts)
}

// ---------------------------------------------------------------------------
// handleInitiateCheckout — POST /api/v1/accounts/current/checkout/initiate (authenticated)
// ---------------------------------------------------------------------------

type initiateRequest struct {
	Rail           Rail   `json:"rail"`
	Credits        int64  `json:"credits"`
	IdempotencyKey string `json:"idempotency_key"`
}

type initiateResponse struct {
	PaymentIntentID string  `json:"payment_intent_id"`
	RedirectURL     string  `json:"redirect_url"`
	Rail            Rail    `json:"rail"`
	Credits         int64   `json:"credits"`
	AmountUSD       int64   `json:"amount_usd"`
	AmountLocal     int64   `json:"amount_local"`
	LocalCurrency   string  `json:"local_currency"`
	TaxTreatment    string  `json:"tax_treatment"`
	ExpiresAt       *string `json:"expires_at,omitempty"`
}

func (h *Handler) handleInitiateCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writePaymentJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	accountID, err := h.accountsSvc.EnsureViewerContext(r.Context())
	if err != nil {
		writePaymentJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req initiateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	// Validate required fields.
	if req.Rail == "" {
		writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "rail is required"})
		return
	}
	if req.Credits <= 0 {
		writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "credits must be positive"})
		return
	}
	if req.Credits%1000 != 0 {
		writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("credits must be a multiple of 1000, got %d", req.Credits)})
		return
	}
	if req.IdempotencyKey == "" {
		writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "idempotency_key is required"})
		return
	}

	callbackBaseURL := resolveCallbackBaseURL(r)

	intent, err := h.svc.InitiateCheckout(r.Context(), accountID, req.Rail, req.Credits, callbackBaseURL, req.IdempotencyKey)
	if err != nil {
		if errors.Is(err, ErrBillingProfileRequired) {
			writePaymentJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{
					"message": "Complete billing profile required before first purchase",
					"type":    "invalid_request_error",
				},
			})
			return
		}
		// Check for ValidationError (credits multiple/positive validation from service layer)
		var errMsg string
		if strings.Contains(err.Error(), "payments:") {
			errMsg = err.Error()
		} else {
			errMsg = fmt.Sprintf("checkout failed: %v", err)
		}
		writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	resp := initiateResponse{
		PaymentIntentID: intent.ID.String(),
		RedirectURL:     intent.RedirectURL,
		Rail:            intent.Rail,
		Credits:         intent.Credits,
		AmountUSD:       intent.AmountUSD,
		AmountLocal:     intent.AmountLocal,
		LocalCurrency:   intent.LocalCurrency,
		TaxTreatment:    intent.TaxTreatment,
	}
	if intent.ExpiresAt != nil {
		s := intent.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
		resp.ExpiresAt = &s
	}

	writePaymentJSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------------
// handleWebhook — POST /webhooks/{provider} (unauthenticated, signature-verified)
// ---------------------------------------------------------------------------

func (h *Handler) handleWebhook(w http.ResponseWriter, r *http.Request, rail Rail) {
	if r.Method != http.MethodPost {
		writePaymentJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// MUST read raw body first, before any JSON parsing.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("payments webhook: read body error: %v", err)
		// Still return 200 — log and continue.
		writePaymentJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Collect headers as lowercase key map.
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}

	if err := h.svc.HandleProviderEvent(r.Context(), rail, rawBody, headers); err != nil {
		log.Printf("payments webhook: handle event error (rail=%s): %v", rail, err)
		// Always return 200 — payment providers retry on non-200, causing duplicate processing.
	}

	writePaymentJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolveCallbackBaseURL derives the callback base URL from the configured env var
// or falls back to the request Host.
func resolveCallbackBaseURL(r *http.Request) string {
	if u := os.Getenv("CONTROL_PLANE_PUBLIC_URL"); u != "" {
		return u
	}
	scheme := "https"
	if r.TLS == nil && (r.Host == "localhost" || strings.HasPrefix(r.Host, "localhost:") || strings.HasPrefix(r.Host, "127.")) {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

func writePaymentJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
