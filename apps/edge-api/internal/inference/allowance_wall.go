package inference

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A 429 carries two different facts and the retry ladder used to hear only one.
//
// "You are going too fast" resets in seconds, and absorbing it is exactly what
// the 300ms / 800ms / 1800ms backoff in retry.go exists for. "Your allowance
// for the period is spent" resets on a calendar boundary, so every millisecond
// of that backoff waits for something that cannot happen until the window
// rolls, and it repeats on every request until it does.
//
// TWO SIGNALS, IN THIS ORDER, after an independent review found the first
// version blind on the member that matters most.
//
//  1. HEADERS, and this is the primary signal. RFC 9110 Retry-After and the
//     de facto X-RateLimit-Reset are standard, provider neutral, and carry the
//     one fact a body cannot: WHEN the window rolls. A reset beyond what the
//     ladder could ever wait for is a wall by arithmetic rather than by
//     vocabulary, and it covers vendors nobody has written a row for.
//
//     THE HEADERS DO REACH US, verified by reading the pinned image rather than
//     assumed, because a reviewer correctly pointed out that this response is
//     LiteLLM's and not OpenRouter's, and that every test here constructs its
//     own headers and so would pass against a proxy that strips them. In
//     v1.98.0, proxy/common_request_processing.py's LLM-exception path takes
//     `e.headers`, or `e.response.headers` via `get_response_headers`, filters
//     them against UNSAFE_PROXY_RESPONSE_HEADERS, and attaches the survivors to
//     the ProxyException it raises. That filter is HTTP_FRAMING_HEADERS plus
//     BROWSER_SECURITY_HEADERS (constants.py): content-length, transfer-encoding,
//     content-encoding, content-type, set-cookie, cookie, proxy-authenticate,
//     proxy-authorization, and the CORS and browser-security family. Neither
//     retry-after nor any x-ratelimit-* name appears in either set, so both
//     survive to the client unrenamed.
//
//     One consequence found while checking, which the prose table deliberately
//     does NOT share: the same path calls _apply_router_cooldown_retry_after,
//     which sets retry-after from LiteLLM's OWN router cooldown on a
//     RouterRateLimitError. The effective cooldown on this stack is 5s, which
//     exceeds the ladder budget, so a router cooldown now classifies as a wall
//     through the header path even though the prose table excludes its body
//     shape by design. That is the correct outcome under the rule below rather
//     than an inconsistency: no deployment is available for 5 seconds, the
//     ladder can wait 2.9, so sleeping first and failing anyway is strictly
//     worse than one immediate re-dispatch and a fast return.
//
//  2. PROSE, for providers that send no such header. Groq is the case this
//     genuinely covers.
//
// Why the order was wrong before. OpenRouter is the provider this stack
// actually uses, and per https://openrouter.ai/docs/api-reference/limits it
// returns an IDENTICAL body for its per-minute cap and its per-day cap:
//
//	{"error":{"code":429,"message":"Rate limit exceeded",
//	          "metadata":{"error_type":"rate_limit_exceeded"}}}
//
// Only the headers separate them. A prose row for OpenRouter is therefore
// impossible to write correctly, and the previous body-only classifier paid the
// full backoff on every request until the daily window rolled, which is
// precisely the failure this file was written to prevent.
//
// The same blind spot exists in the Python precedent this file borrows its
// vocabulary from: scripts/classify-upstream-refusal.py defines DAILY_BUDGET as
// Groq's TPD wording only. Inheriting the vocabulary inherited the gap; the
// header check is what closes it.

// timeNow is the clock, injectable for tests.
var timeNow = time.Now

// retryBudget is the total wall time the ladder can spend waiting. It is
// derived from retryDelays rather than written as a constant, so tuning the
// backoff cannot silently change what counts as a wall.
//
// This is the whole threshold argument: if a limit's own reset is further away
// than every remaining backoff combined, waiting cannot reach the reset, so the
// backoff is pure delay. No magic number, and no per-provider tuning.
//
// It is also SHARPER than the per-minute versus per-day framing this file is
// named for, and the two disagree in a case worth knowing about. An OpenRouter
// per-minute cap that happens to reset 40 seconds from now cannot be waited out
// by a 2.9 second ladder either, so it is classified as a wall and skips the
// backoff. That is the correct behaviour, and "allowance wall" is simply a
// slightly narrow name for it: the real question is never which calendar period
// the provider names, it is whether this ladder could ever outlast the reset.
func retryBudget() time.Duration {
	var total time.Duration
	for _, d := range retryDelays {
		total += d
	}
	return total
}

// allowanceWallSignal is one provider's wording for a spent periodic allowance.
// A vendor is a ROW here, never a branch in the classifier: each spells it
// differently and more are coming, so adding one must not mean touching
// dispatchWithRetry at all.
type allowanceWallSignal struct {
	Provider string
	Pattern  *regexp.Regexp
}

// allowanceWallSignals is the prose table, consulted only when the headers do
// not decide. Every pattern must name a CALENDAR period, not merely a limit:
// "rate limit reached for requests, limit 20 per min" is the transient case
// whose absorption must be preserved, and matching it here would delete the
// reason the backoff exists.
var allowanceWallSignals = []allowanceWallSignal{
	{
		// Groq. Observed live on 2026-08-23 and the reason
		// deploy/litellm/config.yaml sets RateLimitErrorRetries to 0 at the
		// gateway layer; this is the same judgement one layer out. Groq names
		// the period in the message, so prose works here.
		Provider: "groq",
		Pattern:  regexp.MustCompile(`(?i)tokens per day|\bTPD\b`),
	},
	{
		// Cloudflare Workers AI. Its neuron budget is account level rather than
		// per token, resets daily at 00:00 UTC, and surfaces as error code 4006,
		// which some clients wrap in an HTTP 429.
		//
		// The digits are ANCHORED. Unanchored, `"?4006"?` also matched 40060,
		// 40061, 400600 and "4006abc", each of which would silently lose its
		// backoff; two reviewers found this independently. \b after the digits
		// is what rejects them, and it still admits both the bare number and
		// the quoted-string form.
		Provider: "cloudflare",
		Pattern:  regexp.MustCompile(`(?i)"code"\s*:\s*"?4006\b"?|used up your daily free allocation|daily free allocation of[^"]{0,40}neurons`),
	},
	{
		// Vendor-neutral wordings, kept as their own row rather than folded
		// into a vendor's: a provider nobody has added yet that says "monthly
		// quota exceeded" is walled just as hard.
		Provider: "generic-periodic-allowance",
		Pattern:  regexp.MustCompile(`(?i)(daily|monthly|per[- ]day|per[- ]month)[^"]{0,40}(quota|allowance|allocation|budget|limit)[^"]{0,20}(exceeded|exhausted|reached|used up|spent)|(quota|allowance|allocation|budget)[^"]{0,30}(exceeded|exhausted)[^"]{0,30}(today|this month|for the day|for the month)`),
	},
}

// isAllowanceWall reports whether resp is a provider saying a periodic
// allowance is spent, rather than saying we are going too fast.
//
// Scoped to statuses the ladder would otherwise retry, so a success and an
// already-terminal status never pay for a header or body read.
func isAllowanceWall(resp *http.Response) bool {
	if resp == nil || !retryableStatuses[resp.StatusCode] {
		return false
	}
	if wall, decided := resetIsBeyondTheLadder(resp); decided {
		if wall {
			slog.Debug("upstream limit resets beyond the retry ladder; treating as an allowance wall",
				"signal", "headers", "status", resp.StatusCode)
		}
		return wall
	}
	return bodyNamesAllowanceWall(resp)
}

// resetIsBeyondTheLadder reads Retry-After and X-RateLimit-Reset and reports
// whether the limit's own reset is further away than the ladder could wait.
//
// The second return value is what makes this the PRIMARY signal: when a header
// is present it is authoritative in BOTH directions, so a provider telling us
// the window rolls in two seconds keeps its backoff and never reaches the prose
// table. Only a response carrying neither header falls through.
func resetIsBeyondTheLadder(resp *http.Response) (wall bool, decided bool) {
	now := timeNow()

	if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), now); ok {
		return d > retryBudget(), true
	}
	if d, ok := parseRateLimitReset(resp.Header.Get("X-RateLimit-Reset"), now); ok {
		return d > retryBudget(), true
	}
	return false, false
}

// parseRetryAfter handles both RFC 9110 forms: delta-seconds, and an HTTP-date.
func parseRetryAfter(raw string, now time.Time) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if at, err := http.ParseTime(raw); err == nil {
		d := at.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// parseRateLimitReset reads X-RateLimit-Reset as an absolute epoch timestamp.
//
// The unit is not standardised and providers disagree, so it is bounded on BOTH
// sides rather than assumed.
//
// Upper: at or above 1e11 the value cannot be a plausible epoch in seconds
// (that is the year 5138), so it is milliseconds. OpenRouter sends this form.
//
// Lower: below 1e9 (September 2001) it cannot be an epoch at all, and is almost
// certainly the IETF `RateLimit-Reset` draft's DELTA-SECONDS form. This
// repository emits that form itself, in authorizer.go's
// x-ratelimit-reset-requests, so two conventions genuinely coexist under nearly
// the same header name.
//
// The missing lower bound was a real defect, measured by a reviewer on this
// branch: a delta value of 3600 parsed as an epoch resolving to January 1970,
// produced a reset in the past, and returned decided=true with wall=false.
// Because a header is authoritative in both directions, that verdict then
// SUPPRESSED the prose table, so a genuine Groq daily wall was reported as a
// transient burst. Undecided is the safe answer for an ambiguous value: it
// falls through to the body rather than overriding it.
func parseRateLimitReset(raw string, now time.Time) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	if v < 1e9 {
		return 0, false
	}
	var at time.Time
	if v >= 1e11 {
		at = time.UnixMilli(v)
	} else {
		at = time.Unix(v, 0)
	}
	d := at.Sub(now)
	if d < 0 {
		d = 0
	}
	return d, true
}

// bodyNamesAllowanceWall runs the prose table against the provider's REFUSAL
// TEXT, not against the whole response body.
//
// The distinction is the fix for a real defect. Matching the full 8 KiB head
// meant anything the envelope echoed could decide the verdict, and it does not
// take an adversary to land: a body like
//
//	{"error":{"message":"upstream busy",
//	          "request":{"messages":[{"content":"my daily budget was exceeded"}]}}}
//
// classified as a hard wall on the echoed user text, deleting the pacing on a
// transient failure. refusalText pulls out only the provider's own message and
// code fields, so an echoed request payload is out of scope entirely.
func bodyNamesAllowanceWall(resp *http.Response) bool {
	head, ok := peekBody(resp)
	if !ok || head == "" {
		return false
	}
	text := refusalText(head)
	if text == "" {
		return false
	}
	for _, signal := range allowanceWallSignals {
		if signal.Pattern.MatchString(text) {
			// Names the matching row and nothing from the body, so a false
			// positive is traceable to a vendor without logging customer
			// content. This is also what makes the Provider field real rather
			// than documentation.
			slog.Debug("upstream body names a spent periodic allowance",
				"signal", "body", "provider", signal.Provider, "status", resp.StatusCode)
			return true
		}
	}
	return false
}

// maxRefusalFields bounds how many message and code values are collected, so a
// pathological envelope cannot make the join large.
const maxRefusalFields = 32

// refusalText extracts the provider's own refusal wording from a JSON error
// envelope: the message and code fields of `error` and of `errors[]`, which
// between them cover the OpenAI and OpenRouter shape and the Cloudflare shape.
//
// Returns "" when the head does not parse as JSON or carries no such envelope,
// and the caller then falls back to the raw head. That fallback is deliberate:
// a provider answering with plain text still deserves classification, and a
// non-JSON body has no echoed request structure to be confused by.
func refusalText(head string) string {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(head), &envelope); err != nil {
		if len(head) >= maxRetryPeekBytes {
			// Truncated by peekBody, so failing to parse says nothing about
			// whether the provider sent JSON. Falling back to the whole head
			// here would be exactly the unbounded match this function exists to
			// prevent, and a body large enough to be cut is the one MOST likely
			// to be large because it echoes the request. Measured on this
			// branch by a reviewer: the same echo classified false at 1 KiB and
			// true once padded past the cap.
			return ""
		}
		return head
	}

	var fields []string
	collect := func(raw json.RawMessage) {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return
		}
		for _, key := range []string{"message", "code", "type", "error_type"} {
			v, ok := obj[key]
			if !ok || len(fields) >= maxRefusalFields {
				continue
			}
			// The code field is a bare number as often as a string, and the
			// Cloudflare row matches on `"code": 4006`, so the raw JSON token
			// is re-emitted with its key rather than unquoted.
			fields = append(fields, `"`+key+`":`+string(v))
		}
		// metadata is where OpenRouter puts error_type, and where it puts the
		// upstream provider's VERBATIM refusal under `raw`. That is where a Groq
		// daily wall reached through the pool actually lands, so excluding it
		// meant the Groq row could never fire on the pooled path, which is the
		// only path it is reachable on. `raw` is provider generated rather than
		// caller supplied, so including it does not reopen the echo problem.
		if meta, ok := obj["metadata"]; ok && len(fields) < maxRefusalFields {
			var metaObj map[string]json.RawMessage
			if json.Unmarshal(meta, &metaObj) == nil {
				for _, key := range []string{"message", "error_type", "reason", "raw"} {
					if v, ok := metaObj[key]; ok && len(fields) < maxRefusalFields {
						fields = append(fields, `"`+key+`":`+string(v))
					}
				}
			}
		}
	}

	if raw, ok := envelope["error"]; ok {
		collect(raw)
	}
	if raw, ok := envelope["errors"]; ok {
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil {
			for _, item := range list {
				collect(item)
			}
		}
	}
	// A flat envelope, which is the shape a bare {"error":"..."} or a
	// top-level {"message":...} takes.
	for _, key := range []string{"message", "code"} {
		if v, ok := envelope[key]; ok && len(fields) < maxRefusalFields {
			fields = append(fields, `"`+key+`":`+string(v))
		}
	}
	if raw, ok := envelope["error"]; ok && len(fields) == 0 {
		// {"error":"plain string"}.
		var s string
		if json.Unmarshal(raw, &s) == nil {
			fields = append(fields, s)
		}
	}

	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, "\n")
}
