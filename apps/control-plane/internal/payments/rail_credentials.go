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
// The BD rails carry XE_ACCOUNT_ID and XE_API_KEY as well. A BD checkout prices
// itself from an FX snapshot before it reaches the rail at all, and that
// snapshot is taken by NewFXService, which is built from those two variables.
// With the four bKash credentials present and XE absent the rail would register,
// advertise itself as enabled, and then fail inside CreateSnapshot on every
// attempt. It fails closed, so no money moves, but an operator would get no
// warning naming the variable that is actually unset, which is the diagnostic
// gap this file exists to close.
//
// The base URL variables (BKASH_BASE_URL, SSLCOMMERZ_BASE_URL) are deliberately
// absent from these sets: both default to the provider sandbox host at startup,
// so neither is ever unset by the time a rail is built.
var RailCredentialEnvs = map[Rail][]string{
	RailStripe:     {"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"},
	RailBkash:      {"BKASH_APP_KEY", "BKASH_APP_SECRET", "BKASH_USERNAME", "BKASH_PASSWORD", "XE_ACCOUNT_ID", "XE_API_KEY"},
	RailSSLCommerz: {"SSLCOMMERZ_STORE_ID", "SSLCOMMERZ_STORE_PASSWD", "XE_ACCOUNT_ID", "XE_API_KEY"},
}

// MissingRailCredentials returns, in declaration order, the variables of a
// rail's credential set that lookup answers as empty. Whitespace is not a
// credential: a variable set to spaces in a `.env` file would otherwise
// register a rail that cannot authenticate against its provider.
//
// lookup is normally os.Getenv. It is a parameter so the decision is testable
// without mutating the process environment, and it answers a plain string
// rather than the (value, ok) pair of os.LookupEnv precisely so presence can
// never become the test. The deployed box injects all twelve payment variables
// as present-and-empty through compose ${VAR:-} defaults, so on that box a
// presence check would read three fully configured rails where there are none.
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

// RailBuilder pairs a rail with the constructor that builds it. Build runs only
// after the rail's whole credential set is confirmed present, and may still
// refuse: each rail constructor rejects an empty credential itself, so the
// registration decision is never the only thing between a half-built rail and a
// payer (issue #1449).
type RailBuilder struct {
	Rail  Rail
	Build func() (PaymentRail, error)
}

// RailRefusal records one rail left unregistered, in the form the boot log
// prints: the credential variables that were unset, or the constructor's own
// refusal when the set looked complete.
type RailRefusal struct {
	Rail    Rail
	Missing []string
	Err     error
}

// RegisterRails builds the rails this deployment holds a whole credential set
// for, and returns the refusals for the rest in builder order.
//
// This is the decision the reported defect turned on, so it lives here rather
// than inline in main(): a rail was registered from a single environment
// variable, on a deployment where every payment variable is always present, and
// the half-configured rail that resulted could redirect a payer and take their
// money while being unable to settle it.
func RegisterRails(lookup func(string) string, builders []RailBuilder) (map[Rail]PaymentRail, []RailRefusal) {
	rails := make(map[Rail]PaymentRail, len(builders))
	refusals := make([]RailRefusal, 0, len(builders))
	for _, b := range builders {
		if missing := MissingRailCredentials(b.Rail, lookup); len(missing) > 0 {
			refusals = append(refusals, RailRefusal{Rail: b.Rail, Missing: missing})
			continue
		}
		rail, err := b.Build()
		if err != nil {
			refusals = append(refusals, RailRefusal{Rail: b.Rail, Err: err})
			continue
		}
		rails[b.Rail] = rail
	}
	return rails, refusals
}
