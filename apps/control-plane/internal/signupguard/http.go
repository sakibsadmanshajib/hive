package signupguard

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// genericReject is the single customer-visible message used for every abuse
// control. It is intentionally vague so an attacker cannot distinguish a
// disposable-domain block from a rate limit or a CAPTCHA failure, and so no
// upstream/internal detail ever leaks. Operators get the real classification
// from the audit trail and the process log.
const genericReject = "We could not complete your sign-up. Please try again or use a different email address."

// defaultPrecheckTimeout is the per-request deadline applied to the full
// precheck chain (rate-limit Redis call + disposable lookup + Turnstile network
// call). Keeps a slow Turnstile upstream from stacking goroutines.
const defaultPrecheckTimeout = 8 * time.Second

// defaultMaxConcurrent is the global concurrent-request ceiling for the
// precheck handler. Requests beyond this limit are rejected with the generic
// message (same UX, no detail leak). Tunable via SIGNUP_PRECHECK_MAX_CONCURRENT.
const defaultMaxConcurrent = 100

// AuditFunc records a signup-guard decision for operators. The detail map must
// contain classification strings only, never the raw email, domain, or any
// provider/upstream value. Optional; nil disables auditing.
type AuditFunc func(ctx context.Context, action string, detail map[string]string)

// HandlerDeps wires the precheck handler.
type HandlerDeps struct {
	Blocklist   *Blocklist
	RateLimiter *RateLimiter
	Turnstile   *TurnstileVerifier
	AuditFunc   AuditFunc

	// TrustedProxyCIDRs is the list of network ranges whose direct peers are
	// trusted to supply accurate CF-Connecting-IP and X-Forwarded-For headers.
	// Default (nil/empty): trust no forwarded headers; always use RemoteAddr.
	// In production behind Cloudflare, set this to Cloudflare's published IP
	// ranges via TRUSTED_PROXY_CIDRS (comma-separated CIDR notation).
	TrustedProxyCIDRs []*net.IPNet

	// PrecheckTimeout overrides the per-request deadline (default 8s).
	// Zero uses the default.
	PrecheckTimeout time.Duration

	// MaxConcurrent overrides the global concurrent-request ceiling (default 100).
	// Zero uses the default.
	MaxConcurrent int
}

// Handler serves POST /api/v1/auth/sign-up/precheck. It is called by the
// web-console signup page before it invokes Supabase signUp; only a passing
// precheck should proceed to account creation.
type Handler struct {
	deps HandlerDeps
	sem  chan struct{}
}

// NewHandler constructs the precheck handler.
func NewHandler(deps HandlerDeps) *Handler {
	maxC := deps.MaxConcurrent
	if maxC <= 0 {
		maxC = defaultMaxConcurrent
	}
	return &Handler{
		deps: deps,
		sem:  make(chan struct{}, maxC),
	}
}

type precheckRequest struct {
	Email        string `json:"email"`
	CaptchaToken string `json:"captcha_token"`
}

// ServeHTTP runs the three abuse controls in cheap-to-expensive order:
// rate limit (Redis), disposable-domain (in-memory), CAPTCHA (network). Every
// rejection returns the same generic message; only the HTTP status varies so
// SDK/browser retry semantics still work (429 retryable, 4xx terminal).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Concurrency ceiling: non-blocking acquire. Saturated = same generic
	// rejection so an attacker cannot distinguish overload from a block.
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		writeError(w, http.StatusTooManyRequests, genericReject)
		return
	}

	// Per-request timeout wraps the full chain including the Turnstile network call.
	timeout := h.deps.PrecheckTimeout
	if timeout <= 0 {
		timeout = defaultPrecheckTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	ip := clientIPWithTrust(r, h.deps.TrustedProxyCIDRs)

	var body precheckRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	norm, err := NormalizeEmail(body.Email)
	if err != nil {
		// A malformed address is a client error, not an abuse signal.
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}

	// 1) Per-IP rate limit (fail closed per #51).
	if rlErr := h.deps.RateLimiter.Allow(ctx, ip); rlErr != nil {
		if errors.Is(rlErr, ErrRateLimiterUnavailable) {
			log.Printf("signupguard: rate limiter unavailable ip=%s: %v", ip, rlErr)
			h.audit(ctx, "SIGNUP_RATE_LIMITER_UNAVAILABLE", map[string]string{"stage": "rate_limit"})
		} else {
			h.audit(ctx, "SIGNUP_RATE_LIMITED", map[string]string{"stage": "rate_limit"})
		}
		// Both over-quota and fail-closed map to a retryable 429.
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusTooManyRequests, genericReject)
		return
	}

	// 2) Disposable-domain blocklist.
	if h.deps.Blocklist.IsDisposableDomain(domainOf(norm)) {
		h.audit(ctx, "SIGNUP_DISPOSABLE_BLOCKED", map[string]string{"stage": "disposable"})
		writeError(w, http.StatusForbidden, genericReject)
		return
	}

	// 3) CAPTCHA (no-op when TURNSTILE_SECRET_KEY is unset).
	if cErr := h.deps.Turnstile.Verify(ctx, body.CaptchaToken, ip); cErr != nil {
		// Classification only — never the Cloudflare error codes.
		stage := "captcha"
		if errors.Is(cErr, ErrCaptchaRequired) {
			log.Printf("signupguard: captcha token missing ip=%s", ip)
		} else {
			log.Printf("signupguard: captcha verification failed ip=%s", ip)
		}
		h.audit(ctx, "SIGNUP_CAPTCHA_FAILED", map[string]string{"stage": stage})
		writeError(w, http.StatusForbidden, genericReject)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) audit(ctx context.Context, action string, detail map[string]string) {
	if h.deps.AuditFunc == nil {
		return
	}
	h.deps.AuditFunc(ctx, action, detail)
}

// domainOf returns the domain of an already-normalized address.
func domainOf(normalized string) string {
	at := strings.LastIndexByte(normalized, '@')
	if at < 0 {
		return ""
	}
	return normalized[at+1:]
}

// remoteAddrHost extracts the host (without port) from r.RemoteAddr.
func remoteAddrHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// isInCIDRs reports whether ip is contained in any of the given networks.
func isInCIDRs(ip net.IP, cidrs []*net.IPNet) bool {
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIPWithTrust resolves the originating client IP honouring forwarded
// headers only when the direct peer (RemoteAddr) falls inside trustedCIDRs.
//
// When trusted:
//   - CF-Connecting-IP is returned as-is (Cloudflare sets exactly one value).
//   - X-Forwarded-For is walked right-to-left; the rightmost hop that is NOT
//     in the trusted set is the first untrusted (real client) hop.
//   - If every XFF hop is trusted, RemoteAddr host is returned as a safe fallback.
//
// When untrusted (or trustedCIDRs is empty): RemoteAddr host is always returned,
// ignoring any client-supplied headers.
func clientIPWithTrust(r *http.Request, trustedCIDRs []*net.IPNet) string {
	peerHost := remoteAddrHost(r)

	if len(trustedCIDRs) == 0 {
		return peerHost
	}

	peerIP := net.ParseIP(peerHost)
	if peerIP == nil || !isInCIDRs(peerIP, trustedCIDRs) {
		// Direct peer is not trusted: ignore all forwarded headers.
		return peerHost
	}

	// Trusted peer: honour CF-Connecting-IP first.
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}

	// Trusted peer: walk XFF right-to-left, find rightmost untrusted hop.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			hop := strings.TrimSpace(parts[i])
			if hop == "" {
				continue
			}
			hopIP := net.ParseIP(hop)
			if hopIP == nil {
				// Unparseable hop: treat as untrusted, return it.
				return hop
			}
			if !isInCIDRs(hopIP, trustedCIDRs) {
				return hop
			}
		}
		// All XFF hops were trusted: fall through to RemoteAddr.
	}

	return peerHost
}

// clientIP is the zero-trust variant (no forwarded headers honoured).
// Kept for backward compatibility with callers that have not been updated.
func clientIP(r *http.Request) string {
	return clientIPWithTrust(r, nil)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
