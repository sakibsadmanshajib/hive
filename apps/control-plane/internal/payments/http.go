package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
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
	InitiateCheckout(ctx context.Context, accountID uuid.UUID, rail Rail, credits int64, callbackBaseURL, returnBaseURL, idempotencyKey string) (*PaymentIntent, error)
	HandleProviderEvent(ctx context.Context, rail Rail, rawBody []byte, headers map[string]string) error
	GetCheckoutIntent(ctx context.Context, accountID, intentID uuid.UUID) (*CheckoutIntentView, error)
}

// AccountResolver resolves the current account ID from the request context.
// Follows the "accept interfaces" pattern so tests can inject stubs.
type AccountResolver interface {
	EnsureViewerContext(ctx context.Context) (uuid.UUID, error)
}

var ErrVerificationRequired = errors.New("payments: email verification required")

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
	case "/api/v1/accounts/current/checkout/intent":
		h.handleGetCheckoutIntent(w, r)
	case "/webhooks/stripe":
		h.handleWebhook(w, r, RailStripe)
	case "/webhooks/bkash/callback":
		h.handleWebhook(w, r, RailBkash)
	// Only the IPN endpoint remains. The former /success, /fail and /cancel
	// endpoints were browser return URLs pointed at this webhook handler, so a
	// paying customer's browser landed on `{"status":"ok"}` and, worse, a
	// browser request could drive settlement. Browser returns now go to the
	// console (see checkout_return.go); settlement is IPN only (issue #538).
	case "/webhooks/sslcommerz/ipn":
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
		if errors.Is(err, ErrVerificationRequired) {
			writePaymentJSON(w, http.StatusForbidden, map[string]string{
				"error": "email must be verified before accessing billing",
				"code":  "email_verification_required",
			})
			return
		}
		writePaymentJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	opts, err := h.svc.GetCheckoutOptions(r.Context(), accountID)
	if err != nil {
		// FX-17 review-pass (P0 regulatory): never include the raw error
		// message on a BD-eligible customer surface — internal errors such
		// as `payments: invalid effective rate "115.500000"` would leak the
		// FX rate value. Log internally; return an opaque static body.
		log.Printf("payments: GetCheckoutOptions error (account=%s): %v", accountID, err)
		writePaymentJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "checkout temporarily unavailable",
		})
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

// initiateResponse is the customer-surface JSON returned from
// POST /api/v1/accounts/current/checkout/initiate. Phase 17 FX/USD zero-leak
// (FX-17-01) requires this wire DTO carry NO USD/FX fields. The internal
// AmountUSD lives on payments.PaymentIntent (json:"-"); the Stripe USD
// payload is built server-side in stripe/rail.go from the Go struct, not
// from this wire DTO.
type initiateResponse struct {
	PaymentIntentID string  `json:"payment_intent_id"`
	RedirectURL     string  `json:"redirect_url"`
	Rail            Rail    `json:"rail"`
	Credits         int64   `json:"credits"`
	// AmountUSD intentionally omitted (Phase 17 FX-17-01).
	// Internal accounting USD is preserved on PaymentIntent (json:"-").
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
		if errors.Is(err, ErrVerificationRequired) {
			writePaymentJSON(w, http.StatusForbidden, map[string]string{
				"error": "email must be verified before accessing billing",
				"code":  "email_verification_required",
			})
			return
		}
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
	// A loopback console origin is only legitimate when the payer's browser is on
	// this machine, which is what the request host tells us.
	returnBaseURL := ResolveConsoleBaseURL(isLoopbackHost(r.Host))

	intent, err := h.svc.InitiateCheckout(r.Context(), accountID, req.Rail, req.Credits, callbackBaseURL, returnBaseURL, req.IdempotencyKey)
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
		// FX-17 review-pass (P0 regulatory): the legacy passthrough emitted
		// `payments: invalid effective rate "<value>"` and similar internal
		// strings on the customer wire. We now categorize errors and return
		// opaque, BDT-only, non-FX-bearing messages; details go to the log.
		log.Printf("payments: InitiateCheckout error (account=%s, rail=%s): %v", accountID, req.Rail, err)
		status, errMsg := classifyInitiateError(err)
		writePaymentJSON(w, status, map[string]string{"error": errMsg})
		return
	}

	// Phase 17 FX-17-01: do NOT copy intent.AmountUSD into the wire DTO.
	// AmountUSD remains on the internal PaymentIntent struct for ledger +
	// rail USD payload, but is never marshalled to the customer.
	resp := initiateResponse{
		PaymentIntentID: intent.ID.String(),
		RedirectURL:     intent.RedirectURL,
		Rail:            intent.Rail,
		Credits:         intent.Credits,
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
// handleGetCheckoutIntent — GET /api/v1/accounts/current/checkout/intent
// ---------------------------------------------------------------------------
//
// The read the browser return page relies on. Deliberately GET-only and
// side-effect free: the return surface reports what settlement already decided,
// it never decides anything itself (issue #538).
func (h *Handler) handleGetCheckoutIntent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writePaymentJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	accountID, err := h.accountsSvc.EnsureViewerContext(r.Context())
	if err != nil {
		if errors.Is(err, ErrVerificationRequired) {
			writePaymentJSON(w, http.StatusForbidden, map[string]string{
				"error": "email must be verified before accessing billing",
				"code":  "email_verification_required",
			})
			return
		}
		writePaymentJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	// The id arrives in a query string the customer controls, so it is parsed
	// strictly and then matched against the viewer's own account in the service.
	intentID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("payment_intent_id")))
	if err != nil {
		writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "payment_intent_id must be a UUID"})
		return
	}

	view, err := h.svc.GetCheckoutIntent(r.Context(), accountID, intentID)
	if err != nil {
		if errors.Is(err, ErrIntentNotFound) {
			writePaymentJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
			return
		}
		log.Printf("payments: GetCheckoutIntent error (account=%s, intent=%s): %v", accountID, intentID, err)
		writePaymentJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "payment status temporarily unavailable",
		})
		return
	}

	writePaymentJSON(w, http.StatusOK, view)
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

// resolveCallbackBaseURL derives the control-plane origin that providers post
// their server-to-server webhooks to. This is the *webhook* leg only; the
// customer's browser return is resolved independently from the console origin
// (see ResolveConsoleBaseURL) and never touches this value.
//
// A loopback CONTROL_PLANE_PUBLIC_URL is deliberately demoted below the request
// host. Compose injects `CONTROL_PLANE_PUBLIC_URL=http://localhost:8081` as a
// default, so a deployment reached over a tunnel handed every provider a
// callback URL that no provider on the internet can resolve, while the correct
// host-derived fallback below never got a chance to run. Preferring the real
// request host in that specific case makes the deployment work whatever the
// default says, and logs the mismatch instead of failing silently.
//
// This mirrors the same policy the console already applies to a loopback
// NEXT_PUBLIC_APP_URL in apps/web-console/lib/http/origin.ts.
func resolveCallbackBaseURL(r *http.Request) string {
	hostOrigin := requestOriginOf(r)

	if u := strings.TrimRight(strings.TrimSpace(os.Getenv("CONTROL_PLANE_PUBLIC_URL")), "/"); u != "" {
		if !isLoopbackBaseURL(u) || isLoopbackHost(r.Host) {
			return u
		}
		log.Printf(
			"payments: CONTROL_PLANE_PUBLIC_URL=%q is loopback but this request arrived on host %q; "+
				"using %q for provider callbacks. A provider cannot reach a loopback address — "+
				"set CONTROL_PLANE_PUBLIC_URL to the publicly reachable control-plane origin.",
			u, r.Host, hostOrigin,
		)
	}

	return hostOrigin
}

// requestOriginOf reconstructs the origin this request arrived on.
func requestOriginOf(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && isLoopbackHost(r.Host) {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

// isLoopbackHost reports whether a Host header names this machine.
func isLoopbackHost(host string) bool {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.ToLower(strings.Trim(name, "[]"))
	return name == "localhost" || name == "::1" || strings.HasPrefix(name, "127.")
}

// isLoopbackBaseURL reports whether a configured base URL points at loopback.
func isLoopbackBaseURL(base string) bool {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		// Unparseable is treated as unusable, which routes the caller to the
		// request-derived origin rather than to a value nothing can resolve.
		return true
	}
	return isLoopbackHost(parsed.Host)
}

func writePaymentJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// classifyInitiateError maps a service-layer error into an HTTP status +
// customer-safe message. FX-17 review-pass (P0): the prior implementation
// passed the raw `err.Error()` through to the customer when it started
// with "payments:", which leaked internal FX-rate values like
// `payments: invalid effective rate "115.500000"` onto a BD customer wire.
// We now categorize on sentinel errors / known prefixes and return opaque
// messages. The detailed error is logged separately at the call site.
func classifyInitiateError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "checkout failed"
	}
	// Sentinel-error categories — safe to surface their fixed strings.
	if errors.Is(err, ErrBillingProfileRequired) {
		return http.StatusBadRequest, "Complete billing profile required before first purchase"
	}
	if errors.Is(err, ErrFXUnavailable) {
		// Never name "FX" on a BD customer surface.
		return http.StatusServiceUnavailable, "payment service temporarily unavailable"
	}
	if errors.Is(err, ErrReturnURLNotConfigured) {
		// Deployment fault, not a customer fault: no console origin is configured,
		// so a payer would be stranded after paying. Refuse before taking money and
		// keep the variable names in the log, not on the customer wire.
		return http.StatusServiceUnavailable, "payment service temporarily unavailable"
	}
	// Validation errors carry the customer-provided value only (credit
	// count). The substrings below are safe — they do not echo FX rates,
	// USD amounts, or any internal accounting detail.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "credits must be positive"):
		return http.StatusBadRequest, "credits must be positive"
	case strings.Contains(msg, "credits must be a multiple of 1000"):
		return http.StatusBadRequest, "credits must be a multiple of 1000"
	case strings.Contains(msg, "rail") && strings.Contains(msg, "not available"):
		return http.StatusBadRequest, "selected payment rail is not available for this account"
	}
	// Default: opaque message. Any internal "payments: invalid effective
	// rate ..." or similar string MUST NOT reach this point as wire bytes.
	return http.StatusBadRequest, "checkout failed"
}
