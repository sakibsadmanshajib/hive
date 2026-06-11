package signupguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultTurnstileURL is Cloudflare's server-side siteverify endpoint.
const defaultTurnstileURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// ErrCaptchaRequired is returned when CAPTCHA is enabled but the caller did not
// present a token.
var ErrCaptchaRequired = errors.New("signupguard: captcha token required")

// ErrCaptchaFailed is returned when Cloudflare rejects the token or the
// verification call itself fails. The error string is deliberately generic so
// no upstream error code reaches the customer surface.
var ErrCaptchaFailed = errors.New("signupguard: captcha verification failed")

// turnstileResponse models the Cloudflare siteverify JSON body. Only Success
// and ErrorCodes are consumed; ErrorCodes is logged by the caller, never
// forwarded to the customer.
type turnstileResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
	Hostname   string   `json:"hostname"`
}

// TurnstileVerifier performs server-side Cloudflare Turnstile verification.
// When constructed with an empty secret it is disabled and Verify is a no-op
// that accepts every request (feature flag off via unset TURNSTILE_SECRET_KEY).
type TurnstileVerifier struct {
	secret    string
	client    *http.Client
	verifyURL string
}

// NewTurnstileVerifier constructs a verifier. An empty secret disables the
// feature. A nil client falls back to a 5s-timeout default client.
func NewTurnstileVerifier(secret string, client *http.Client) *TurnstileVerifier {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &TurnstileVerifier{
		secret:    strings.TrimSpace(secret),
		client:    client,
		verifyURL: defaultTurnstileURL,
	}
}

// Enabled reports whether a secret is configured. When false, Verify accepts
// every request without a network call.
func (v *TurnstileVerifier) Enabled() bool {
	return v != nil && v.secret != ""
}

// Verify validates the Turnstile token against Cloudflare. When the verifier is
// disabled it returns nil. When enabled it fails closed: a missing token, a
// network/HTTP error, or an unsuccessful result all return an error so the
// caller rejects the signup.
func (v *TurnstileVerifier) Verify(ctx context.Context, token, remoteIP string) error {
	if !v.Enabled() {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return ErrCaptchaRequired
	}

	form := url.Values{}
	form.Set("secret", v.secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.verifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w: build request: %v", ErrCaptchaFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		// Fail closed on transport errors (timeouts, DNS, refused).
		return fmt.Errorf("%w: transport: %v", ErrCaptchaFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: unexpected status %d", ErrCaptchaFailed, resp.StatusCode)
	}

	var body turnstileResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrCaptchaFailed, err)
	}
	if !body.Success {
		// Return the bare sentinel: the upstream Cloudflare error codes must
		// never reach the customer surface, so they are not embedded in the
		// returned error. The codes are diagnostic only and are not logged by
		// this library (the HTTP layer logs a fixed classification instead).
		return ErrCaptchaFailed
	}
	return nil
}
