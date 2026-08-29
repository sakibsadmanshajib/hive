package inference

import (
	"errors"
	"log"
	"net/http"
	"net/url"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// refuseOnReservationFailure classifies a failed CreateReservation call. It
// returns true when the request must not be dispatched to a provider, having
// already written the customer-facing refusal, and false only when the failure
// is an enumerated infrastructure-transient fault the request is allowed to
// proceed through unreserved.
//
// Fail closed by default. A reservation failure means the request cannot be
// accounted for, so serving it anyway gives away inference for free: that is
// exactly what happened before this existed, because the three dispatch sites
// classified the failure by string-matching the error text against "429",
// "budget" and "insufficient". A control-plane credit rejection answers 409
// with "reservation exceeds available credits", which matches none of those
// three, so every insolvent account's traffic was logged as a non-fatal
// warning and then served, unmetered.
//
// Classification is on the HTTP status the control-plane returned, never on
// message text. The two failure directions point opposite ways -- serving free
// is revenue loss, refusing a solvent customer is an outage -- so the
// transient set below has to genuinely cover infrastructure faults, and
// anything outside it has to refuse.
func refuseOnReservationFailure(w http.ResponseWriter, endpoint, model string, err error) bool {
	if reservationFailureTransient(err) {
		log.Printf("inference: create reservation failed, proceeding unreserved (transient control-plane fault) endpoint=%s alias=%s: %v", endpoint, model, err)
		return false
	}

	var statusErr *StatusError
	if errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusConflict || statusErr.StatusCode == http.StatusTooManyRequests) {
		// 409 is the control-plane's credit-policy rejection
		// (accounting.PolicyError, see writeAccountingError); 429 is a rate
		// limit on the reservation call itself. Both are quota verdicts, and
		// both answer with the OpenAI-compatible insufficient_quota shape that
		// SDKs already understand: status 429, type and code
		// insufficient_quota, unchanged, because that is what SDKs branch on.
		//
		// The two say DIFFERENT things to a human, though, and used to share
		// one sentence borrowed from OpenAI: "You exceeded your current quota,
		// please check your plan and billing details." On the 409 branch that
		// was wrong twice over, since the customer has credit and Hive has no
		// plans, and it sent them to a billing page that showed a positive
		// balance (issue #1372). Provider-blind and free of any balance figure
		// on purpose: this is an error body, not a billing surface.
		code := "insufficient_quota"
		message := "Your available credit does not cover this request. Add credits, or send a shorter request, and try again."
		if statusErr.StatusCode == http.StatusTooManyRequests {
			message = "Too many requests right now. Please retry in a moment."
		}
		apierrors.WriteError(w, http.StatusTooManyRequests, "insufficient_quota", message, &code)
		return true
	}

	// Anything unrecognized (a 4xx that is not a quota verdict, a malformed
	// request, a decode failure) is fatal, never non-fatal: an error nobody
	// enumerated is precisely the case that must not silently reach a provider.
	log.Printf("inference: create reservation refused, unclassified failure endpoint=%s alias=%s: %v", endpoint, model, err)
	code := "reservation_unavailable"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error",
		"Credit reservation is temporarily unavailable. Please retry.", &code)
	return true
}

// reservationFailureTransient reports whether a reservation failure is an
// infrastructure fault the gateway may ride out unreserved, rather than a
// verdict about the account. The enumerated set is deliberately narrow:
//
//   - a control-plane 5xx: the accounting service itself failed, not the
//     customer's credit position;
//   - any transport-level failure, which net/http surfaces as *url.Error:
//     connection refused, DNS failure, TLS failure, and request timeout or
//     context deadline (the client's Timeout is accountingTimeout).
//
// Everything else, including a marshal or decode bug on our own side, is fatal.
func reservationFailureTransient(err error) bool {
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode >= 500
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}
