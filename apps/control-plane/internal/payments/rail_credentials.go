package payments

import (
	"errors"
	"strings"
)

// ErrRailNotConfigured is returned when a checkout names a rail this deployment
// holds no credentials for, so no rail implementation was registered at startup.
//
// It is a deployment fault, not a customer fault: the box can neither take the
// payment nor settle it, so callers surface it as a service failure rather than
// a bad request, and refuse before any intent, FX snapshot or redirect exists
// (issue #1449).
var ErrRailNotConfigured = errors.New("payments: payment rail is not configured on this deployment")

// RailCredentialEnvs names the environment variables a deployment must set
// before a rail can both take a payment and settle one. Every variable listed
// is load bearing on that round trip, which is why a rail is registered only
// when the whole set is present.
//
// A partially configured rail is worse than an absent one. Stripe with a secret
// key and no webhook secret redirects the payer, takes the money, and then
// fails signature verification on every settlement event, so the payment
// succeeds at the provider and the credit never arrives. bKash needs its
// username and password to mint a grant token at all, and SSLCommerz signs its
// validation call with the store password.
//
// The base URL variables (BKASH_BASE_URL, SSLCOMMERZ_BASE_URL) are deliberately
// absent from these sets: both default to the provider sandbox host at startup,
// so neither is ever unset by the time a rail is built.
var RailCredentialEnvs = map[Rail][]string{
	RailStripe:     {"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"},
	RailBkash:      {"BKASH_APP_KEY", "BKASH_APP_SECRET", "BKASH_USERNAME", "BKASH_PASSWORD"},
	RailSSLCommerz: {"SSLCOMMERZ_STORE_ID", "SSLCOMMERZ_STORE_PASSWD"},
}

// MissingRailCredentials returns, in declaration order, the variables of a
// rail's credential set that lookup answers as empty. Whitespace is not a
// credential: a variable set to spaces in a `.env` file would otherwise
// register a rail that cannot authenticate against its provider.
//
// lookup is normally os.Getenv. It is a parameter so the decision is testable
// without mutating the process environment.
func MissingRailCredentials(rail Rail, lookup func(string) string) []string {
	required := RailCredentialEnvs[rail]
	missing := make([]string, 0, len(required))
	for _, name := range required {
		if strings.TrimSpace(lookup(name)) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
