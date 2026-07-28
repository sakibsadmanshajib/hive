package payments

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Browser return versus server-to-server webhook (issue #538)
// ---------------------------------------------------------------------------
//
// A payment rail talks to us over two entirely different channels and they must
// never share an endpoint:
//
//   - The webhook (or IPN) is a server-to-server POST from the provider. It is
//     signature or hash verified, it is the only thing allowed to settle a
//     payment, and it answers with machine JSON. It lives on the control-plane
//     origin (InitiateInput.CallbackBaseURL).
//   - The browser return is where the paying customer's own browser is sent
//     after the hosted payment page finishes. It must render a human page on
//     the console origin (InitiateInput.ReturnBaseURL) and it must never be
//     able to move money.
//
// Before this split, SSLCommerz sent paying customers to the control-plane IPN
// handler, so a customer saw `{"status":"ok"}` as raw JSON, and Stripe sent them
// to `/checkout/stripe`, a route that existed in no service at all.

const (
	// CheckoutReturnPath is the single console page every rail's browser return
	// resolves to. It renders the authoritative outcome of a payment.
	CheckoutReturnPath = "/console/billing/checkout/return"

	// SSLCommerzReturnPath is the console route SSLCommerz is pointed at.
	// SSLCommerz returns the customer with a form POST rather than a plain
	// navigation, which a Next.js page cannot serve, so a small route handler
	// absorbs the POST and redirects the browser to CheckoutReturnPath.
	SSLCommerzReturnPath = "/api/payments/return/sslcommerz"

	// ReturnHintCancelled marks a return that came from a provider's "cancel"
	// or "back" URL.
	//
	// TRUST BOUNDARY: a hint is a copy selector, never a state. It travels in a
	// query string the customer can edit, so the return page is only allowed to
	// use it to soften wording while the authoritative intent state is still
	// pending. It can never produce a success, a failure, or a credit.
	ReturnHintCancelled = "cancelled"

	// consoleBaseURLEnv names the console origin that browsers are returned to.
	consoleBaseURLEnv = "WEB_CONSOLE_PUBLIC_URL"
)

// consoleBaseURLEnvs is the preference order for the console origin.
//
// CONSOLE_APP_URL is the deployed console origin (`.env.example` sets it to the
// real host, and compose feeds it to the console's NEXT_PUBLIC_APP_URL build
// arg). NEXT_PUBLIC_APP_URL is last because `.env.example` ships it as
// `http://localhost:3000` for local development, so it is the value most likely
// to be a loopback placeholder rather than a real origin.
var consoleBaseURLEnvs = []string{
	consoleBaseURLEnv,
	"CONSOLE_APP_URL",
	"NEXT_PUBLIC_APP_URL",
}

// ReturnState is the customer-facing outcome rendered on the return page. It is
// always derived from the stored payment intent, never from a request parameter.
type ReturnState string

const (
	ReturnStateSuccess   ReturnState = "success"
	ReturnStatePending   ReturnState = "pending"
	ReturnStateFailed    ReturnState = "failed"
	ReturnStateCancelled ReturnState = "cancelled"
)

// ReturnStateFor collapses the intent state machine into the four states the
// return page renders. Anything that is not yet terminal is pending, which is
// the common real case: the browser usually gets back before the provider's
// webhook has landed.
func ReturnStateFor(status IntentStatus) ReturnState {
	switch status {
	case IntentStatusCompleted:
		return ReturnStateSuccess
	case IntentStatusFailed, IntentStatusExpired:
		return ReturnStateFailed
	case IntentStatusCancelled:
		return ReturnStateCancelled
	default:
		return ReturnStatePending
	}
}

// CheckoutIntentView is the customer-safe projection of a payment intent used
// by the return page.
//
// BD regulatory rule: this is a customer-visible surface, so it carries no USD
// amount, no FX rate, and no currency-exchange language. Credits are the only
// quantity a returning customer needs, and credits are currency free.
type CheckoutIntentView struct {
	PaymentIntentID string       `json:"payment_intent_id"`
	Rail            Rail         `json:"rail"`
	Status          IntentStatus `json:"status"`
	State           ReturnState  `json:"state"`
	Credits         int64        `json:"credits"`
}

// NewCheckoutIntentView projects a stored intent onto the customer-safe view.
func NewCheckoutIntentView(intent PaymentIntent) CheckoutIntentView {
	return CheckoutIntentView{
		PaymentIntentID: intent.ID.String(),
		Rail:            intent.Rail,
		Status:          intent.Status,
		State:           ReturnStateFor(intent.Status),
		Credits:         intent.Credits,
	}
}

// ErrReturnURLNotConfigured is returned when no console origin is configured at
// all. It is a deployment fault, not a customer fault, so callers surface it as a
// temporary service failure rather than a bad request.
var ErrReturnURLNotConfigured = errors.New("payments: console return base URL is not configured")

// ValidateReturnBaseURL rejects anything that is not a usable absolute http or
// https origin. A rail that cannot build a real return URL must fail the
// checkout rather than send a paying customer somewhere undefined.
func ValidateReturnBaseURL(base string) error {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return fmt.Errorf("%w (set one of %s)", ErrReturnURLNotConfigured, strings.Join(consoleBaseURLEnvs, ", "))
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("payments: console return base URL is not a URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("payments: console return base URL must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("payments: console return base URL has no host")
	}
	return nil
}

// ResolveConsoleBaseURL returns the console origin browsers are returned to,
// or the empty string when no usable origin is configured.
//
// There is deliberately no hardcoded default. A base URL with a plausible
// looking built-in default is exactly how this system already produced an
// outage: compose injected a loopback CONTROL_PLANE_PUBLIC_URL, so every
// provider callback dialled an address no provider can reach, and the
// misconfiguration was invisible until a customer hit it. An empty value here
// fails the checkout loudly at ValidateReturnBaseURL, naming the variables to
// set, which is strictly better on a money path than silently redirecting a
// payer somewhere that does not answer.
//
// requestIsLoopback reports whether the checkout request itself arrived on
// loopback, which is what makes a loopback console origin legitimate. A loopback
// candidate is otherwise demoted below any real origin and, if it is the only
// candidate, refused outright. This is the same policy resolveCallbackBaseURL
// applies to the webhook leg, and it exists because `.env.example` ships
// NEXT_PUBLIC_APP_URL=http://localhost:3000: without the demotion a deployed box
// would bake `http://localhost:3000` into a live SSLCommerz success_url and the
// payer would be sent to their own machine. The code refuses the value rather
// than relying on an operator remembering to override it.
func ResolveConsoleBaseURL(requestIsLoopback bool) string {
	var loopbackCandidate, loopbackKey string

	for _, key := range consoleBaseURLEnvs {
		v := strings.TrimRight(strings.TrimSpace(os.Getenv(key)), "/")
		if v == "" {
			continue
		}
		if !isLoopbackBaseURL(v) {
			return v
		}
		if loopbackCandidate == "" {
			loopbackCandidate, loopbackKey = v, key
		}
	}

	// Local development: the payer's browser is on this machine, so a loopback
	// console origin is the correct answer.
	if requestIsLoopback {
		return loopbackCandidate
	}

	if loopbackCandidate != "" {
		log.Printf(
			"payments: refusing to return a paying customer to %s=%q because it is a loopback address "+
				"and this checkout did not arrive on loopback. Set %s to the publicly reachable console origin.",
			loopbackKey, loopbackCandidate, consoleBaseURLEnv,
		)
	}
	return ""
}

// BrowserReturnURL builds the URL a rail sends the customer's browser to.
//
// hint may be empty; when set it is only ever a copy selector (see
// ReturnHintCancelled). The authoritative outcome comes from the payment intent
// record that the return page reads back through the account-scoped API.
func BrowserReturnURL(returnBaseURL, path string, intentID uuid.UUID, rail Rail, hint string) string {
	q := url.Values{}
	q.Set("rail", string(rail))
	q.Set("intent", intentID.String())
	if hint != "" {
		q.Set("hint", hint)
	}
	return strings.TrimRight(returnBaseURL, "/") + path + "?" + q.Encode()
}
