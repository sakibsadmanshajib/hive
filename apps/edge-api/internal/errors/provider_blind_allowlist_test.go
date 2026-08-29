package errors

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteProviderBlindUpstreamErrorCollapsesEverythingNotCustomerActionable
// is the width test for the allowlist that replaced the old forward-unless-
// matched default.
//
// Every case below is an upstream talking about the account WE hold with it,
// and every one of them reached a customer word for word on the revision that
// used a money-vocabulary denylist (measured, PR #1303 review). They are kept
// here as a table precisely because the failure mode being guarded is "the
// list was one phrasing short", so the test has to be cheap to extend and has
// to fail loudly when the default flips back to forwarding.
//
// Statuses are deliberately 400, 402 and 500 and never 401 or 403: the auth
// rule already collapses every 401 and 403 regardless of body, so a case on
// those statuses would pass with this guard removed entirely and would pin
// nothing. Disabling providerBlindCustomerActionable (making it return true)
// must turn every subtest below red.
func TestWriteProviderBlindUpstreamErrorCollapsesEverythingNotCustomerActionable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		raw    string
		// leaks is the vocabulary that must not survive, per case, so a
		// failure names the actual phrase that got through.
		leaks []string
	}{
		{
			name:   "deepseek out of balance on 402",
			status: http.StatusPaymentRequired,
			raw:    `{"error":{"message":"Insufficient Balance"}}`,
			leaks:  []string{"insufficient", "balance"},
		},
		{
			name:   "monthly spend limit on 400",
			status: http.StatusBadRequest,
			raw:    `{"error":{"message":"You have exceeded your monthly spend limit. Please contact sales."}}`,
			leaks:  []string{"spend limit", "contact sales", "exceeded"},
		},
		{
			name:   "past due with payment information",
			status: http.StatusBadRequest,
			raw:    `{"error":{"message":"Your account is past due. Update your payment information to continue."}}`,
			leaks:  []string{"past due", "payment information"},
		},
		{
			name:   "free tier limit reached names our tier",
			status: http.StatusBadRequest,
			raw:    `{"error":{"message":"Free tier limit reached for this organization."}}`,
			leaks:  []string{"free tier", "organization"},
		},
		{
			name:   "prepaid funds exhausted",
			status: http.StatusPaymentRequired,
			raw:    `{"error":{"message":"Your organization has run out of prepaid funds."}}`,
			leaks:  []string{"prepaid", "funds", "organization"},
		},
		{
			name:   "the reported sentence on a non-429 status",
			status: http.StatusBadRequest,
			raw:    `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota"}}`,
			leaks:  []string{"quota", "billing", "plan"},
		},
		{
			name:   "payment required with a top-up link",
			status: http.StatusPaymentRequired,
			raw:    `{"error":{"message":"Insufficient credits. Add more using https://openrouter.ai/settings/credits","code":402}}`,
			leaks:  []string{"credits", "https://", "settings/credits"},
		},
		{
			name:   "upstream internals on a 500",
			status: http.StatusInternalServerError,
			raw:    `{"error":{"message":"Upstream node pool degraded, shard 7 of 12 offline, see status page"}}`,
			leaks:  []string{"shard", "node pool", "status page"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			logs := captureProviderBlindLogs(t, func() {
				WriteProviderBlindUpstreamError(w, "hive-auto", tc.status, tc.raw)
			})

			resp := decodeOpenAIError(t, w)
			lower := strings.ToLower(resp.Error.Message)
			for _, forbidden := range tc.leaks {
				if strings.Contains(lower, strings.ToLower(forbidden)) {
					t.Fatalf("upstream text reached the customer: %q in %q", forbidden, resp.Error.Message)
				}
			}
			assertNoProviderLeak(t, resp.Error.Message)
			if !strings.Contains(resp.Error.Message, "hive-auto") {
				t.Fatalf("message should name the alias the caller asked for, got %q", resp.Error.Message)
			}
			// The operator still gets the raw text; only the customer does not.
			if !strings.Contains(logs, "provider_blind_upstream_error") {
				t.Fatalf("raw upstream text must stay in the operator log, got %q", logs)
			}
		})
	}
}

// TestWriteProviderBlindUpstreamErrorKeepsCustomerActionableText is the other
// side of the allowlist, and the reason it is an allowlist rather than a
// blanket collapse: an upstream error the caller can actually fix must still
// say what to fix. Without these, "collapse everything" would pass the test
// above trivially.
func TestWriteProviderBlindUpstreamErrorKeepsCustomerActionableText(t *testing.T) {
	cases := []struct {
		name   string
		status int
		raw    string
		expect string
	}{
		{
			name:   "prompt longer than the context window",
			status: http.StatusBadRequest,
			raw:    `{"error":{"message":"This model's maximum context length is 8192 tokens, however you requested 9001 tokens."}}`,
			expect: "maximum context length",
		},
		{
			name:   "invalid parameter value",
			status: http.StatusBadRequest,
			raw:    `{"error":{"message":"Invalid value for 'temperature': must be one of the supported range 0 to 2."}}`,
			expect: "must be one of",
		},
		{
			name:   "content refused by the upstream filter",
			status: http.StatusBadRequest,
			// Phrased without the word "rejected" on purpose: the
			// pre-existing auth rule collapses any message containing it,
			// which would make this case pass with the allowlist removed.
			raw:    `{"error":{"message":"Your prompt was blocked by the content filter."}}`,
			expect: "content filter",
		},
		{
			name:   "unprocessable request shape",
			status: http.StatusUnprocessableEntity,
			raw:    `{"error":{"message":"Unsupported parameter: 'logprobs' is not supported with this model."}}`,
			expect: "unsupported parameter",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteProviderBlindUpstreamError(w, "hive-auto", tc.status, tc.raw)
			resp := decodeOpenAIError(t, w)
			if !strings.Contains(strings.ToLower(resp.Error.Message), tc.expect) {
				t.Fatalf("customer-actionable detail was collapsed away: want %q in %q", tc.expect, resp.Error.Message)
			}
			assertNoProviderLeak(t, resp.Error.Message)
		})
	}
}

// TestWriteProviderBlindUpstreamErrorKeepsRateLimitVerdictOn429 pins the
// interaction that a money-vocabulary rule placed ahead of the rate-limit rule
// got wrong: Google and Vertex phrase an ordinary per-minute rate limit in
// quota vocabulary ("Quota exceeded for quota metric ... requests per
// minute"), and Azure says "Tokens per minute quota exceeded". Both are 429s,
// both are wait-and-retry verdicts, and the response code says
// rate_limit_error, so the sentence must agree with the code rather than tell
// the customer to go pick another model.
//
// Google Gemini is seeded as a live provider by migration 20260824_02, so this
// is reachable rather than theoretical.
func TestWriteProviderBlindUpstreamErrorKeepsRateLimitVerdictOn429(t *testing.T) {
	for _, raw := range []string{
		`{"error":{"message":"Quota exceeded for quota metric 'Generate Content API requests per minute'"}}`,
		`{"error":{"message":"Tokens per minute quota exceeded. Please retry after 12 seconds."}}`,
	} {
		t.Run(raw[:40], func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteProviderBlindUpstreamError(w, "hive-auto", http.StatusTooManyRequests, raw)

			resp := decodeOpenAIError(t, w)
			if resp.Error.Type != "rate_limit_error" {
				t.Fatalf("type = %q, want rate_limit_error", resp.Error.Type)
			}
			if resp.Error.Message != "hive-auto is temporarily rate limited." {
				t.Fatalf("message = %q, want the rate-limit verdict to agree with the code", resp.Error.Message)
			}
			assertNoProviderLeak(t, resp.Error.Message)
		})
	}
}

// TestWriteProviderBlindUpstreamErrorVetoesMixedMessages closes the bypass an
// allowlist has by construction: a token licenses forwarding the WHOLE
// message, so one upstream sentence carrying both an actionable clause and
// account detail would carry the account detail out with it.
//
// Found by the Antigravity review stream on this pull request, not by a
// probe against a live provider, so the fixtures are constructed rather than
// measured. They are still the shape a chatty upstream produces when it
// explains two things at once.
func TestWriteProviderBlindUpstreamErrorVetoesMixedMessages(t *testing.T) {
	for _, raw := range []string{
		`{"error":{"message":"Organization org-12345 is over its quota, and this prompt also exceeds the maximum context length of 8192 tokens."}}`,
		`{"error":{"message":"Invalid value for 'stream'. Also, your account balance is too low to continue."}}`,
		`{"error":{"message":"Free tier accounts must be one of the supported regions."}}`,
	} {
		t.Run(raw[:45], func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteProviderBlindUpstreamError(w, "hive-auto", http.StatusBadRequest, raw)
			resp := decodeOpenAIError(t, w)
			lower := strings.ToLower(resp.Error.Message)
			for _, forbidden := range []string{"org-12345", "quota", "balance", "free tier", "account"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("account detail rode out on an actionable token: %q in %q", forbidden, resp.Error.Message)
				}
			}
			// The collapsed fallback for a request-shaped status, which changed
			// with the fix for issue 1348: a 400 now says the REQUEST was
			// invalid instead of the neutral "request failed", because the
			// old sentence read as a fault on our side of a call that means
			// the opposite. What this test exists to pin, the leak assertions
			// above, is unchanged.
			if resp.Error.Message != "Invalid request for hive-auto. Check the request parameters." {
				t.Fatalf("message = %q, want the collapsed request-shaped fallback", resp.Error.Message)
			}
		})
	}
}

// TestWriteProviderBlindUpstreamErrorRateLimitRuleIsPinnedOffThe429Fallback
// exists because the 429 test above cannot pin the rule it names: with the
// rate-limit guard deleted, a 429 falls through to fallbackProviderBlindMessage,
// which returns the identical sentence for that status, so the test stays
// green over a removed guard (Antigravity review finding on this pull
// request, and the same "cannot go red" failure this repository keeps hitting).
//
// A non-429 status carrying rate-limit text is the case only the guard can
// answer: the fallback for a 500 is "request failed."
func TestWriteProviderBlindUpstreamErrorRateLimitRuleIsPinnedOffThe429Fallback(t *testing.T) {
	w := httptest.NewRecorder()
	WriteProviderBlindUpstreamError(w, "hive-auto", http.StatusInternalServerError,
		`{"error":{"message":"Backend rate limit reached, retry shortly."}}`)

	resp := decodeOpenAIError(t, w)
	if resp.Error.Message != "hive-auto is temporarily rate limited." {
		t.Fatalf("message = %q, want the rate-limit verdict on a non-429 status", resp.Error.Message)
	}
}
