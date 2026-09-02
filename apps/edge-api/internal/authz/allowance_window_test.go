package authz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/packages/ratewindows"
)

// newLiveLimiter builds a Limiter over an in-process Redis, so these tests
// exercise the real Lua scripts rather than a stub that agrees with whatever
// the caller expected. Issue #1725: the pre-existing limiter tests all replace
// runLongWindow with a closure, which means no test in this package had ever
// executed window_score.lua at all.
func newLiveLimiter(t *testing.T, now func() time.Time) *Limiter {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	limiter := &Limiter{
		redis:             client,
		rpmTPMScript:      redis.NewScript(rpmTPMLua),
		windowScoreScript: redis.NewScript(windowScoreLua),
		now:               now,
	}
	limiter.runSlidingWindow = limiter.defaultRunSlidingWindow
	limiter.runLongWindow = limiter.defaultRunLongWindow
	return limiter
}

func windowSnapshot(sessionLimit, weeklyLimit int64, anchor time.Time) AuthSnapshot {
	anchorStr := anchor.Format(time.RFC3339Nano)
	return AuthSnapshot{
		KeyID:     "key-1725",
		AccountID: "acct-1725",
		Status:    "active",
		AccountRatePolicy: &RatePolicy{
			RollingFiveHourLimit:  sessionLimit,
			WeeklyLimit:           weeklyLimit,
			FreeTokenWeightTenths: 1,
			WeeklyAnchorAt:        &anchorStr,
		},
		KeyRatePolicy: &RatePolicy{FreeTokenWeightTenths: 1},
	}
}

// TestConfiguredSessionLimitActuallyRefuses is the test issue #1725 exists for.
// It asserts a REFUSAL once a configured session allowance is spent, not that a
// counter moved: a counter test passes just as happily when nothing is enforced.
func TestConfiguredSessionLimitActuallyRefuses(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	limiter := newLiveLimiter(t, func() time.Time { return now })
	snapshot := windowSnapshot(1000, 0, now.Add(-48*time.Hour))

	// Spend the whole session allowance.
	for i := 0; i < 10; i++ {
		result, err := limiter.Check(context.Background(), snapshot, "hive-default", 100, 0, 0)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("check %d refused early: %+v", i, result)
		}
	}

	result, err := limiter.Check(context.Background(), snapshot, "hive-default", 100, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if result.Allowed {
		t.Fatal("the eleventh request was admitted after the session allowance was spent; nothing is enforced")
	}
	if result.Reason != "session_limit_exceeded" {
		t.Fatalf("reason %q, want session_limit_exceeded (a fraud refusal and an allowance refusal must be distinguishable)", result.Reason)
	}
	if result.Window != ratewindows.Session {
		t.Fatalf("window %q, want %q", result.Window, ratewindows.Session)
	}
	if result.ResetAt.IsZero() {
		t.Fatal("refusal carries no reset timestamp; a limit with no visible reset is indistinguishable from an outage")
	}
	if !result.ResetAt.After(now) {
		t.Fatalf("reset %s is not in the future of %s", result.ResetAt, now)
	}
	// The session window slides over five hours, so a full drain of usage
	// recorded now cannot be more than five hours out.
	if result.ResetAt.After(now.Add(5*time.Hour + time.Minute)) {
		t.Fatalf("reset %s is beyond the five hour session window from %s", result.ResetAt, now)
	}
	if !result.Session.Configured || result.Session.Limit != 1000 {
		t.Fatalf("session state %+v does not report the configured limit", result.Session)
	}
	if result.Weekly.Configured {
		t.Fatalf("weekly reported as configured while unset: %+v", result.Weekly)
	}
}

// TestWeeklyWindowIsAnchoredNotRolling pins the owner's ruling (D-069): the
// weekly allowance restores in full at the account's anchor.
func TestWeeklyWindowIsAnchoredNotRolling(t *testing.T) {
	anchor := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
	current := anchor.Add(6 * 24 * time.Hour) // late in the anchor's week
	clock := current
	limiter := newLiveLimiter(t, func() time.Time { return clock })
	snapshot := windowSnapshot(0, 500, anchor)

	for i := 0; i < 5; i++ {
		result, err := limiter.Check(context.Background(), snapshot, "hive-default", 100, 0, 0)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("check %d refused early: %+v", i, result)
		}
	}
	refused, err := limiter.Check(context.Background(), snapshot, "hive-default", 100, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if refused.Allowed {
		t.Fatal("weekly allowance was not enforced")
	}
	if refused.Reason != "weekly_limit_exceeded" {
		t.Fatalf("reason %q, want weekly_limit_exceeded", refused.Reason)
	}
	wantReset := anchor.Add(7 * 24 * time.Hour)
	if !refused.ResetAt.Equal(wantReset) {
		t.Fatalf("weekly reset %s, want the anchor's week end %s (a rolling week would report a day out)", refused.ResetAt, wantReset)
	}

	// One second past the anchor's week end the whole allowance is back. A
	// rolling seven day window would still be holding six days of the spend.
	clock = wantReset.Add(time.Second)
	restored, err := limiter.Check(context.Background(), snapshot, "hive-default", 500, 0, 0)
	if err != nil {
		t.Fatalf("check after anchor: %v", err)
	}
	if !restored.Allowed {
		t.Fatalf("the allowance did not restore in full at the anchor: %+v", restored)
	}
}

// TestUnsetWindowIsReportedUnlimitedNotZero: an unconfigured window must be
// visibly unlimited rather than silently absent.
func TestUnsetWindowIsReportedUnlimitedNotZero(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	limiter := newLiveLimiter(t, func() time.Time { return now })
	snapshot := windowSnapshot(0, 0, now)

	result, err := limiter.Check(context.Background(), snapshot, "hive-default", 1_000_000_000, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("an unconfigured window refused a request: %+v", result)
	}
	if result.Session.Configured || result.Weekly.Configured {
		t.Fatalf("unset windows reported as configured: %+v %+v", result.Session, result.Weekly)
	}
	if result.Session.Limit != 0 || result.Weekly.Limit != 0 {
		t.Fatalf("unset windows carry a limit: %+v %+v", result.Session, result.Weekly)
	}
}

// TestSuccessCarriesWindowHeaders: headers must ship on a 200, not only a 429.
func TestSuccessCarriesWindowHeaders(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	limiter := newLiveLimiter(t, func() time.Time { return now })
	snapshot := windowSnapshot(1000, 5000, now.Add(-24*time.Hour))

	result, err := limiter.Check(context.Background(), snapshot, "hive-default", 250, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("refused unexpectedly: %+v", result)
	}
	headers := RateLimitHeaders(result)
	for _, want := range []string{
		"x-ratelimit-session-used-percent",
		"x-ratelimit-session-remaining-percent",
		"x-ratelimit-session-reset",
		"x-ratelimit-session-reset-at",
		"x-ratelimit-weekly-used-percent",
		"x-ratelimit-weekly-remaining-percent",
		"x-ratelimit-weekly-reset",
		"x-ratelimit-weekly-reset-at",
		"ratelimit-limit",
		"ratelimit-remaining",
		"ratelimit-reset",
		"ratelimit-policy",
	} {
		if headers[want] == "" {
			t.Fatalf("success headers are missing %q: %v", want, headers)
		}
	}
	if headers["x-ratelimit-session-used-percent"] != "25" {
		t.Fatalf("session used %q percent, want 25", headers["x-ratelimit-session-used-percent"])
	}
	if headers["x-ratelimit-session-remaining-percent"] != "75" {
		t.Fatalf("session remaining %q percent, want 75", headers["x-ratelimit-session-remaining-percent"])
	}

	// The raw allowance must never appear: it is a credit score, and credits
	// convert to dollars by a published constant, so shipping it would
	// disclose the confidential internal value of a plan (D-068, D-070).
	for name, value := range headers {
		if value == "1000" || value == "5000" || value == "750" || value == "4750" {
			t.Fatalf("header %q leaks the raw allowance %q; windows ship as percentages only", name, value)
		}
	}
}

// TestRefusalNamesWindowAndReset covers the customer-facing half: the message a
// caller reads must say which window ran out and when it comes back.
func TestRefusalNamesWindowAndReset(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(90 * time.Minute)
	result := LimitResult{
		Reason:  "session_limit_exceeded",
		Window:  ratewindows.Session,
		ResetAt: resetAt,
		Session: WindowState{Configured: true, Limit: 1000, Used: 1000, ResetAt: resetAt},
	}

	msg := rateLimitMessage(result)
	if !strings.Contains(strings.ToLower(msg), "session") {
		t.Fatalf("refusal does not name its window: %q", msg)
	}
	if !strings.Contains(msg, resetAt.UTC().Format(time.RFC3339)) {
		t.Fatalf("refusal does not name its reset time: %q", msg)
	}
	if strings.Contains(msg, "$") || strings.Contains(strings.ToLower(msg), "usd") {
		t.Fatalf("refusal leaks a currency figure (D-070): %q", msg)
	}

	// And the wire response has to survive the trip: a 429 with the reset in
	// the body, not a 401 because the code stopped being rate_limit_exceeded.
	rec := httptest.NewRecorder()
	oerr := rateLimitError(result)
	apierrors.WriteAuthFailure(rec, oerr, RateLimitHeaders(result))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			ResetAt string `json:"reset_at"`
			Window  string `json:"limit_window"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "session_limit_exceeded" {
		t.Fatalf("body code %q, want session_limit_exceeded", body.Error.Code)
	}
	if body.Error.ResetAt != resetAt.UTC().Format(time.RFC3339) {
		t.Fatalf("body reset_at %q, want %q", body.Error.ResetAt, resetAt.UTC().Format(time.RFC3339))
	}
	if body.Error.Window != ratewindows.Session {
		t.Fatalf("body limit_window %q, want %q", body.Error.Window, ratewindows.Session)
	}
}
