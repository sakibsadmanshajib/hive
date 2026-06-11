package signupguard

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
)

// genericReject is the single customer-visible message used for every abuse
// control. It is intentionally vague so an attacker cannot distinguish a
// disposable-domain block from a rate limit or a CAPTCHA failure, and so no
// upstream/internal detail ever leaks. Operators get the real classification
// from the audit trail and the process log.
const genericReject = "We could not complete your sign-up. Please try again or use a different email address."

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
}

// Handler serves POST /api/v1/auth/sign-up/precheck. It is called by the
// web-console signup page before it invokes Supabase signUp; only a passing
// precheck should proceed to account creation.
type Handler struct{ deps HandlerDeps }

// NewHandler constructs the precheck handler.
func NewHandler(deps HandlerDeps) *Handler { return &Handler{deps: deps} }

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

	ip := clientIP(r)

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
	if rlErr := h.deps.RateLimiter.Allow(r.Context(), ip); rlErr != nil {
		if errors.Is(rlErr, ErrRateLimiterUnavailable) {
			log.Printf("signupguard: rate limiter unavailable ip=%s: %v", ip, rlErr)
			h.audit(r.Context(), "SIGNUP_RATE_LIMITER_UNAVAILABLE", map[string]string{"stage": "rate_limit"})
		} else {
			h.audit(r.Context(), "SIGNUP_RATE_LIMITED", map[string]string{"stage": "rate_limit"})
		}
		// Both over-quota and fail-closed map to a retryable 429.
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusTooManyRequests, genericReject)
		return
	}

	// 2) Disposable-domain blocklist.
	if h.deps.Blocklist.IsDisposableDomain(domainOf(norm)) {
		h.audit(r.Context(), "SIGNUP_DISPOSABLE_BLOCKED", map[string]string{"stage": "disposable"})
		writeError(w, http.StatusForbidden, genericReject)
		return
	}

	// 3) CAPTCHA (no-op when TURNSTILE_SECRET_KEY is unset).
	if cErr := h.deps.Turnstile.Verify(r.Context(), body.CaptchaToken, ip); cErr != nil {
		// Classification only — never the Cloudflare error codes.
		stage := "captcha"
		if errors.Is(cErr, ErrCaptchaRequired) {
			log.Printf("signupguard: captcha token missing ip=%s", ip)
		} else {
			log.Printf("signupguard: captcha verification failed ip=%s", ip)
		}
		h.audit(r.Context(), "SIGNUP_CAPTCHA_FAILED", map[string]string{"stage": stage})
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

// clientIP resolves the originating client IP, preferring Cloudflare's
// CF-Connecting-IP, then the first hop of X-Forwarded-For, then RemoteAddr.
func clientIP(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
