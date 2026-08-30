package inference

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A 429 has two meanings and the ladder used to treat them as one.
//
// "You are going too fast" resets in seconds and is worth absorbing: that is
// what the 300ms / 800ms / 1800ms backoff exists for. "Your allowance is spent"
// resets on a calendar boundary, so every millisecond of that backoff waits for
// something that cannot happen before the window rolls.
//
// The header tests come first because headers are the PRIMARY signal. That
// ordering is the fix for the defect review found: OpenRouter, the provider
// this stack actually uses, returns an identical body for its per-minute and
// per-day caps, so no amount of prose can separate them.

func respWithHeaders(status int, body string, headers map[string]string) *http.Response {
	resp := mkResp(status, body)
	resp.Header = http.Header{}
	for k, v := range headers {
		resp.Header.Set(k, v)
	}
	return resp
}

// openRouterBody is the exact envelope OpenRouter returns for BOTH its
// per-minute cap and its per-day cap, per
// https://openrouter.ai/docs/api-reference/limits. It is the same string in
// both header cases below, which is the whole point.
const openRouterBody = `{"error":{"code":429,"message":"Rate limit exceeded","metadata":{"error_type":"rate_limit_exceeded"}}}`

func TestOpenRouterPerDayAndPerMinuteAreSeparatedByHeadersAlone(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	prev := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = prev })

	// Daily cap: OpenRouter sends X-RateLimit-Reset as a Unix timestamp in
	// MILLISECONDS, here the next 00:00 UTC boundary.
	midnight := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	daily := respWithHeaders(429, openRouterBody, map[string]string{
		"X-RateLimit-Reset": strconv.FormatInt(midnight.UnixMilli(), 10),
	})
	if !isAllowanceWall(daily) {
		t.Error("OpenRouter per-day cap was not classified as a wall; its body is identical to the per-minute case, so only the reset header can decide")
	}

	// Per-minute cap with a reset the ladder can actually outlast. Identical
	// body, so only the header separates this from the case above.
	//
	// The threshold is the ladder's own budget, not the calendar period, and
	// 1.5s sits inside the default 2.9s. A per-minute cap whose reset is 40
	// seconds away is correctly classified as a wall too, because the ladder
	// cannot outlast that either. See the axis test below.
	burst := respWithHeaders(429, openRouterBody, map[string]string{
		"X-RateLimit-Reset": strconv.FormatInt(now.Add(1500*time.Millisecond).UnixMilli(), 10),
	})
	if isAllowanceWall(burst) {
		t.Error("a reset 1.5s away was classified as a wall; that is inside the ladder budget and is exactly what the backoff exists to absorb")
	}
}

// The axis is NOT the calendar period the provider names, it is whether the
// ladder can outlast the reset.
//
// This is sharper than the per-minute versus per-day framing this file started
// from, and it is worth pinning because the two genuinely disagree: an
// OpenRouter per-minute cap that resets in 40 seconds cannot be waited out by a
// 2.9 second ladder either, so sleeping through it is the same pure delay a
// daily wall would be. Classifying it as a wall is correct, and the name
// "allowance wall" is the part that is slightly wrong, not the behaviour.
func TestTheAxisIsWhetherTheLadderCanOutlastTheReset(t *testing.T) {
	now := time.Now()
	prev := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = prev })

	budget := retryBudget()
	for _, tc := range []struct {
		name  string
		after time.Duration
		want  bool
	}{
		{"just inside the budget", budget - 100*time.Millisecond, false},
		{"just beyond the budget", budget + 100*time.Millisecond, true},
		{"a per-minute cap resetting in 40s is still unwaitable", 40 * time.Second, true},
		{"a daily cap", 11 * time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := respWithHeaders(429, openRouterBody, map[string]string{
				"X-RateLimit-Reset": strconv.FormatInt(now.Add(tc.after).UnixMilli(), 10),
			})
			if got := isAllowanceWall(resp); got != tc.want {
				t.Errorf("reset %v away classified as wall=%v, want %v (ladder budget is %v)", tc.after, got, tc.want, budget)
			}
		})
	}
}

func TestRetryAfterDecidesInBothDirections(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	prev := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = prev })

	cases := []struct {
		name       string
		retryAfter string
		want       bool
	}{
		// The budget is the sum of retryDelays, 2.9s by default, so these
		// straddle it rather than a hand-picked constant.
		{"two seconds is a burst", "2", false},
		{"one hour is a wall", "3600", true},
		{"zero is a burst", "0", false},
		{"http-date far ahead is a wall", now.Add(6 * time.Hour).UTC().Format(http.TimeFormat), true},
		{"http-date one second ahead is a burst", now.Add(1 * time.Second).UTC().Format(http.TimeFormat), false},
		{"http-date in the past is a burst", now.Add(-time.Hour).UTC().Format(http.TimeFormat), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := respWithHeaders(429, openRouterBody, map[string]string{"Retry-After": tc.retryAfter})
			if got := isAllowanceWall(resp); got != tc.want {
				t.Errorf("Retry-After %q classified as wall=%v, want %v", tc.retryAfter, got, tc.want)
			}
		})
	}
}

// A header is authoritative in BOTH directions. A provider saying the window
// rolls in two seconds must keep its backoff even if the body happens to carry
// wording the prose table would match.
func TestHeadersOverrideTheProseTable(t *testing.T) {
	now := time.Now()
	prev := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = prev })

	resp := respWithHeaders(429,
		`{"error":{"message":"tokens per day (TPD) limit reached"}}`,
		map[string]string{"Retry-After": "1"})

	if isAllowanceWall(resp) {
		t.Error("a one-second Retry-After was overridden by prose; the header carries the one fact the body cannot, which is when the window rolls")
	}
}

// A malformed header must fall through rather than decide.
//
// REWRITTEN after a reviewer answered the "which test passes either way" question
// with this one. The previous version asserted a wall on a body the prose table
// matches anyway, so it could not tell "the malformed header fell through" apart
// from "the header was never consulted", and it stayed green under a mutation
// that stubbed resetIsBeyondTheLadder to always return (false, false).
//
// It now pairs the malformed header with a WELL FORMED one that decides the
// opposite way, in both directions, so treating the malformed value as decisive
// fails the test.
func TestUnparseableHeaderIsSkippedNotTreatedAsDecisive(t *testing.T) {
	now := time.Now()
	prev := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = prev })

	transientBody := `{"error":{"message":"Rate limit reached for requests. Limit: 20 / min."}}`
	wallBody := `{"error":{"message":"tokens per day (TPD) limit reached"}}`

	// A malformed Retry-After must not stop the well-formed X-RateLimit-Reset
	// from deciding "wall", on a body the prose table would NOT match.
	resp := respWithHeaders(429, transientBody, map[string]string{
		"Retry-After":       "soon",
		"X-RateLimit-Reset": strconv.FormatInt(now.Add(6*time.Hour).UnixMilli(), 10),
	})
	if !isAllowanceWall(resp) {
		t.Error("a malformed Retry-After suppressed a well-formed X-RateLimit-Reset; a header it cannot parse must be skipped, not treated as decisive")
	}

	// And the opposite direction: with a well-formed reset inside the budget,
	// the verdict must be "not a wall" even though the BODY says otherwise.
	// This is the half the previous version could not see.
	resp = respWithHeaders(429, wallBody, map[string]string{
		"Retry-After":       "soon",
		"X-RateLimit-Reset": strconv.FormatInt(now.Add(1*time.Second).UnixMilli(), 10),
	})
	if isAllowanceWall(resp) {
		t.Error("a well-formed reset inside the ladder budget was overridden by prose, so the malformed header path is not falling through correctly")
	}

	// Only when BOTH headers are unusable does the prose table decide.
	resp = respWithHeaders(429, wallBody, map[string]string{
		"Retry-After": "soon", "X-RateLimit-Reset": "not-a-number",
	})
	if !isAllowanceWall(resp) {
		t.Error("with no usable header at all, the prose table must decide")
	}
}

// Finding 2, measured by the reviewer on this branch. X-RateLimit-Reset has an
// upper plausibility bound but had no lower one, so a delta-seconds value (the
// IETF RateLimit-Reset form, which this repo emits itself in authorizer.go)
// parsed as an epoch in 1970, produced a past reset, and returned decided=true
// with wall=false. Because a header is authoritative in both directions, that
// then SUPPRESSED the prose table, turning a genuine Groq daily wall into a
// reported transient burst.
func TestDeltaSecondsResetDoesNotSuppressTheProseTable(t *testing.T) {
	now := time.Now()
	prev := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = prev })

	groqWall := `{"error":{"message":"Limit reset at 00:00 UTC. tokens per day (TPD)"}}`

	for _, delta := range []string{"3600", "60", "1", "999999999"} {
		t.Run("delta="+delta, func(t *testing.T) {
			resp := respWithHeaders(429, groqWall, map[string]string{"X-RateLimit-Reset": delta})
			if !isAllowanceWall(resp) {
				t.Errorf("X-RateLimit-Reset %q was read as an epoch in the past and suppressed the prose table; a value too small to be an epoch must be undecided so the body still decides", delta)
			}
		})
	}

	// The control: a real epoch is still read as one, in both units.
	resp := respWithHeaders(429, groqWall, map[string]string{
		"X-RateLimit-Reset": strconv.FormatInt(now.Add(6*time.Hour).Unix(), 10),
	})
	if !isAllowanceWall(resp) {
		t.Error("an epoch-seconds reset six hours out was not read as a wall")
	}
	resp = respWithHeaders(429, `{"error":{"message":"Rate limit reached for requests. Limit: 20 / min."}}`, map[string]string{
		"X-RateLimit-Reset": strconv.FormatInt(now.Add(1*time.Second).Unix(), 10),
	})
	if isAllowanceWall(resp) {
		t.Error("an epoch-seconds reset one second out was read as a wall")
	}
}

// Finding 1. peekBody truncates at maxRetryPeekBytes, so an envelope larger than
// that arrives cut mid token and cannot parse as JSON. Falling back to the whole
// head there is exactly the unbounded match refusalText exists to prevent, and a
// body large enough to be cut is the one MOST likely to be large because it
// echoes the request.
//
// The reviewer measured the same echo classifying false at a small size and true
// once padded past the cap, which is why the existing echo test could not see it.
func TestAnOversizeEchoingBodyDoesNotDeclareAWall(t *testing.T) {
	echo := `{"error":{"message":"upstream busy","request":{"messages":[{"content":"my daily budget was exceeded yesterday`
	padding := strings.Repeat("x", maxRetryPeekBytes)
	body := echo + padding + `"}]}}}`

	if len(body) <= maxRetryPeekBytes {
		t.Fatalf("test body is %d bytes, which does not exceed the %d byte peek cap, so this test cannot exercise truncation", len(body), maxRetryPeekBytes)
	}

	resp := mkResp(429, body)
	resp.Header = http.Header{}
	if isAllowanceWall(resp) {
		t.Error("an oversize body classified as a wall on echoed request content; a head truncated by peekBody must not fall back to whole-head matching")
	}

	// The control: the SAME echo under the cap is also not a wall, so this is
	// about the truncation path rather than about the wording.
	small := mkResp(429, echo+`"}]}}}`)
	small.Header = http.Header{}
	if isAllowanceWall(small) {
		t.Error("the same echo under the peek cap classified as a wall")
	}

	// And a genuine oversize wall is still unreachable through the body, which
	// is the accepted cost of refusing to match a truncated head. Recorded as a
	// behaviour rather than left to be rediscovered: providers that wall us send
	// small envelopes, and the header path covers the rest.
	bigWall := `{"error":{"message":"tokens per day (TPD)` + padding + `"}}`
	resp = mkResp(429, bigWall)
	resp.Header = http.Header{}
	if isAllowanceWall(resp) {
		t.Error("an oversize body was matched despite truncation; the guard is not firing")
	}
}

// Finding 4. OpenRouter relays the upstream provider's verbatim refusal in
// metadata.raw, which is where a Groq daily wall reached through the pool
// actually lands. Excluding it meant the Groq row could never fire on the pooled
// path, which is the only path it is reachable on.
func TestRelayedUpstreamRefusalInMetadataRawIsRead(t *testing.T) {
	body := `{"error":{"code":429,"message":"Provider returned error","metadata":{"raw":"{\"error\":{\"message\":\"Rate limit reached. Limit reset at 00:00 UTC. tokens per day (TPD)\"}}","provider_name":"Groq"}}}`

	resp := mkResp(429, body)
	resp.Header = http.Header{}
	if !isAllowanceWall(resp) {
		t.Error("a Groq daily wall relayed through OpenRouter's metadata.raw was not classified; that is the only shape the Groq row can ever see on the pooled path")
	}

	// metadata.raw is provider generated, so reading it does not reopen the echo
	// problem: caller content still lives under error.request and stays out.
	echo := `{"error":{"message":"Provider returned error","metadata":{"provider_name":"Groq"},"request":{"messages":[{"content":"tokens per day"}]}}}`
	resp = mkResp(429, echo)
	resp.Header = http.Header{}
	if isAllowanceWall(resp) {
		t.Error("caller content under error.request decided the verdict")
	}
}

// A LiteLLM router cooldown reaches us as retry-after, set by
// _apply_router_cooldown_retry_after in the pinned v1.98.0 image, and the
// effective cooldown on this stack is 5s. The prose table deliberately excludes
// the cooldown BODY shape, so this pins the deliberate asymmetry: through the
// header path it does classify as a wall, because the ladder cannot wait 5s and
// sleeping 2.9s before failing anyway is strictly worse than one immediate
// re-dispatch and a fast return.
func TestRouterCooldownRetryAfterIsTreatedAsUnwaitable(t *testing.T) {
	cooldownBody := `{"error":{"message":"No deployments available for selected model, Try again in 5 seconds. Passed model=route-free-pool cooldown_list=['7916fd0b']"}}`

	// Body alone: not a wall, as the prose table intends.
	resp := mkResp(429, cooldownBody)
	resp.Header = http.Header{}
	if isAllowanceWall(resp) {
		t.Error("the router cooldown body matched the prose table, which it must not")
	}

	// With LiteLLM's own retry-after, the arithmetic decides and it is a wall.
	resp = respWithHeaders(429, cooldownBody, map[string]string{"Retry-After": "5"})
	if !isAllowanceWall(resp) {
		t.Errorf("a 5 second router cooldown was not treated as unwaitable against a %v ladder budget", retryBudget())
	}
}

func TestAllowanceWallIsRecognisedPerProvider(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			// Groq, observed live on 2026-08-23. Groq names the period in the
			// message, which is the case the prose table genuinely covers.
			name: "groq tokens per day",
			body: `{"error":{"message":"Rate limit reached for model gpt-oss-20b: Limit 500000, Used 500000. Limit reset at 00:00 UTC. tokens per day (TPD)","type":"tokens","code":"rate_limit_exceeded"}}`,
			want: true,
		},
		{
			name: "cloudflare neuron code 4006",
			body: `{"errors":[{"code":4006,"message":"you have used up your daily free allocation of 10,000 neurons"}]}`,
			want: true,
		},
		{
			name: "cloudflare wording without the code",
			body: `{"error":{"message":"You have used up your daily free allocation of neurons. It resets at 00:00 UTC."}}`,
			want: true,
		},
		{
			name: "monthly allowance",
			body: `{"error":{"message":"Monthly quota exceeded for this account"}}`,
			want: true,
		},
		{
			// The case that must NOT match, or the ladder loses the absorption
			// it exists for.
			name: "ordinary per-minute burst",
			body: `{"error":{"message":"Rate limit reached for requests. Limit: 20 / min. Please try again in 3s.","code":"rate_limit_exceeded"}}`,
			want: false,
		},
		{
			// LiteLLM's own router cooldown is not a provider refusal at all.
			// classify-upstream-refusal.py learned this on run 33243396287,
			// where it read a cooldown line as an upstream rate limit and
			// misreported a storage bug.
			name: "litellm router cooldown",
			body: `{"error":{"message":"No deployments available for selected model, Try again in 5 seconds. Passed model=route-free-pool cooldown_list=['7916fd0b']"}}`,
			want: false,
		},
		{
			name: "empty body",
			body: ``,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := mkResp(429, tc.body)
			resp.Header = http.Header{}
			got := isAllowanceWall(resp)
			if got != tc.want {
				t.Errorf("isAllowanceWall = %v, want %v", got, tc.want)
			}

			// The body must survive classification intact, exactly as the
			// sibling classifiers in retry.go guarantee: this runs on the path
			// that hands the upstream error to the customer, and a
			// half-consumed body silently truncates what they see.
			rest, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body after classification: %v", err)
			}
			if string(rest) != tc.body {
				t.Errorf("body after classification = %q, want it spliced back whole as %q", string(rest), tc.body)
			}
		})
	}
}

// The negative test that would have caught the unanchored pattern. Both
// reviewers found it independently by running the regex rather than reading it,
// which is the lesson this test encodes.
func TestNeighbouringErrorCodesDoNotTripTheCloudflareRow(t *testing.T) {
	for _, body := range []string{
		`{"errors":[{"code":40060,"message":"transient"}]}`,
		`{"errors":[{"code":40061,"message":"unrelated"}]}`,
		`{"errors":[{"code":400600,"message":"bad gateway"}]}`,
		`{"errors":[{"code":"4006abc","message":"nonsense"}]}`,
		`{"errors":[{"code":4007,"message":"a different failure"}]}`,
		`{"errors":[{"code":1006,"message":"not this one either"}]}`,
	} {
		resp := mkResp(429, body)
		resp.Header = http.Header{}
		if isAllowanceWall(resp) {
			t.Errorf("%s classified as a spent daily allowance; only code 4006 exactly is Cloudflare's neuron budget, and a false positive here silently deletes the backoff", body)
		}
	}

	// The control: the real code still matches, so the anchoring did not just
	// disable the row.
	resp := mkResp(429, `{"errors":[{"code":4006,"message":"neurons"}]}`)
	resp.Header = http.Header{}
	if !isAllowanceWall(resp) {
		t.Error("anchoring the pattern also stopped it matching the real code 4006")
	}
}

// The classifier must read the provider's refusal, not whatever the envelope
// echoes back. Content does not have to be adversarial to land here: a caller
// discussing their budget in a prompt is enough.
func TestEchoedRequestContentDoesNotDeclareAWall(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"upstream busy","request":{"messages":[{"content":"my daily budget was exceeded yesterday"}]}}}`,
		`{"error":{"message":"connection reset","input":{"prompt":"explain why the monthly quota exceeded error happens"}}}`,
		`{"error":{"message":"Rate limit reached for requests. Limit: 20 / min.","echo":{"text":"tokens per day"}}}`,
	} {
		resp := mkResp(429, body)
		resp.Header = http.Header{}
		if isAllowanceWall(resp) {
			t.Errorf("echoed request content decided the verdict for %s; the classifier must read the provider's own message and code, not the whole envelope", body)
		}
	}
}

// Not every status is worth paying a body read for.
func TestAllowanceWallOnlyInspectsRetryableStatuses(t *testing.T) {
	for _, status := range []int{200, 201, 400, 401, 403, 404} {
		resp := mkResp(status, `{"error":{"message":"you have used up your daily free allocation"}}`)
		resp.Header = http.Header{}
		if isAllowanceWall(resp) {
			t.Errorf("status %d classified as an allowance wall; only a status the ladder would otherwise retry is worth the read", status)
		}
	}
	// A 5xx carrying the wording does count: a provider that answers 503 on a
	// spent allowance walls us just as hard as one that answers 429.
	resp := mkResp(503, `{"error":{"message":"monthly quota exceeded"}}`)
	resp.Header = http.Header{}
	if !isAllowanceWall(resp) {
		t.Error("a retryable 5xx naming a spent allowance was not classified")
	}
}

func TestAllowanceWallIsNilSafe(t *testing.T) {
	if isAllowanceWall(nil) {
		t.Error("a nil response classified as an allowance wall")
	}
}

// retryBudget is derived from retryDelays so that tuning the backoff cannot
// silently move what counts as a wall.
func TestRetryBudgetTracksTheLadder(t *testing.T) {
	orig := retryDelays
	retryDelays = []time.Duration{0, 5 * time.Second, 7 * time.Second}
	t.Cleanup(func() { retryDelays = orig })

	if got := retryBudget(); got != 12*time.Second {
		t.Fatalf("retryBudget = %v, want 12s; it must be the sum of retryDelays, not a constant", got)
	}
}

// ─── behaviour ──────────────────────────────────────────────────────────────

// A hard wall must fail over to another pool member IMMEDIATELY rather than
// sleeping the backoff first, and must then fail fast rather than firing the
// whole ladder back to back.
func TestDispatchWithRetryDoesNotBackOffOnAnAllowanceWall(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 300 * time.Millisecond, 800 * time.Millisecond, 1800 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		resp := mkResp(429, `{"errors":[{"code":4006,"message":"you have used up your daily free allocation of 10,000 neurons"}]}`)
		resp.Header = http.Header{}
		return resp, nil
	}

	start := time.Now()
	resp, err := dispatchWithRetry(context.Background(), "route-free-pool", []byte("{}"), fn)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 429 {
		t.Fatalf("status = %d, want the upstream 429 returned verbatim", resp.StatusCode)
	}
	// One failover attempt, then stop. Running the full ladder at zero delay
	// would be the worst possible shape during a pool-wide exhaustion.
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2: one attempt, one immediate failover, then fail fast", got)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("ladder took %v against a spent allowance; the backoff is waiting for a window that resets on a calendar boundary", elapsed)
	}
}

// The control. A transient limit must keep its backoff, or this change would
// have removed the absorption the ladder exists for.
func TestDispatchWithRetryStillBacksOffOnATransientRateLimit(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 120 * time.Millisecond, 120 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		resp := mkResp(429, `{"error":{"message":"Rate limit reached for requests. Limit: 20 / min."}}`)
		resp.Header = http.Header{}
		return resp, nil
	}

	start := time.Now()
	if _, err := dispatchWithRetry(context.Background(), "route-free-pool", []byte("{}"), fn); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	elapsed := time.Since(start)

	if elapsed < 200*time.Millisecond {
		t.Errorf("ladder took only %v on an ordinary per-minute limit; the backoff that absorbs a real burst was lost", elapsed)
	}
	if got := atomic.LoadInt32(&calls); got != int32(len(retryDelays)) {
		t.Errorf("calls = %d, want %d", got, len(retryDelays))
	}
}

// A wall on one member followed by a healthy member is the whole point: the
// immediate re-dispatch is what gives LiteLLM the chance to pick another one.
func TestDispatchWithRetryFailsOverImmediatelyFromAWalledMember(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 900 * time.Millisecond, 900 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		resp := mkResp(200, `{"ok":true}`)
		if atomic.AddInt32(&calls, 1) == 1 {
			resp = mkResp(429, `{"error":{"message":"tokens per day (TPD) limit reached"}}`)
		}
		resp.Header = http.Header{}
		return resp, nil
	}

	start := time.Now()
	resp, err := dispatchWithRetry(context.Background(), "route-free-pool", []byte("{}"), fn)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 from the surviving member", resp.StatusCode)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("failover took %v, want immediate", elapsed)
	}
}

// The signal table is the extension point: a provider's hard-wall wording is a
// row, not a branch.
//
// Strengthened after review flagged the previous version as near-vacuous. It
// used to assert only that rows had non-empty names and non-nil patterns, which
// is close to unfailable. Each row must now actually match its own real-world
// sample and must NOT match the ordinary per-minute case, so a row added with a
// pattern too loose or too tight fails here rather than in production.
func TestEverySignalRowMatchesItsOwnSampleAndNotATransientLimit(t *testing.T) {
	samples := map[string]string{
		"groq":                       `"message":"Limit reset at 00:00 UTC. tokens per day (TPD)"`,
		"cloudflare":                 `"code":4006`,
		"generic-periodic-allowance": `"message":"Monthly quota exceeded for this account"`,
	}
	const transient = `"message":"Rate limit reached for requests. Limit: 20 / min. Please try again in 3s."`

	if len(allowanceWallSignals) == 0 {
		t.Fatal("the signal table is empty, so every assertion below is vacuous")
	}

	seen := map[string]bool{}
	for _, sig := range allowanceWallSignals {
		if strings.TrimSpace(sig.Provider) == "" {
			t.Error("a signal row carries no provider name, so a false positive cannot be traced to a vendor")
			continue
		}
		if sig.Pattern == nil {
			t.Errorf("signal row %q carries a nil pattern", sig.Provider)
			continue
		}
		if seen[sig.Provider] {
			t.Errorf("duplicate signal row for %q", sig.Provider)
		}
		seen[sig.Provider] = true

		sample, ok := samples[sig.Provider]
		if !ok {
			t.Errorf("signal row %q has no sample in this test; every row must come with the wording it was written for, or nothing proves it can fire", sig.Provider)
			continue
		}
		if !sig.Pattern.MatchString(sample) {
			t.Errorf("row %q does not match its own sample %q", sig.Provider, sample)
		}
		if sig.Pattern.MatchString(transient) {
			t.Errorf("row %q matches an ordinary per-minute limit, which would delete the backoff this ladder exists for", sig.Provider)
		}
	}

	for provider := range samples {
		if !seen[provider] {
			t.Errorf("sample for %q has no row; the table and this test have drifted apart", provider)
		}
	}
}
