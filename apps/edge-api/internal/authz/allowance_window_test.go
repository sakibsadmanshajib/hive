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
	limiter.commitLongWindows = limiter.defaultCommitLongWindows
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

// TestRefusalDistinguishesOversizedFromExhausted guards a defect found live
// during the issue #1725 proof capture: a first request against a completely
// unused window refused with "you have used all of your session allowance",
// because the request alone was larger than the allowance. A customer told
// they had spent everything, with a bar reading zero percent beside it, goes
// looking for usage that does not exist.
func TestRefusalDistinguishesOversizedFromExhausted(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour)

	exhausted := rateLimitMessage(LimitResult{
		Reason:  "session_limit_exceeded",
		Window:  ratewindows.Session,
		ResetAt: reset,
		Session: WindowState{Configured: true, Limit: 1000, Used: 1000, Remaining: 0, ResetAt: reset},
	})
	if !strings.Contains(exhausted, "used all of your session allowance") {
		t.Fatalf("an exhausted window does not say so: %q", exhausted)
	}

	oversized := rateLimitMessage(LimitResult{
		Reason:  "session_limit_exceeded",
		Window:  ratewindows.Session,
		ResetAt: reset,
		Session: WindowState{Configured: true, Limit: 1000, Used: 100, Remaining: 900, ResetAt: reset},
	})
	if strings.Contains(oversized, "used all") {
		t.Fatalf("a window with 90 percent left claims it is spent: %q", oversized)
	}
	if !strings.Contains(oversized, "90%") {
		t.Fatalf("the oversized-request refusal does not say how much is left: %q", oversized)
	}
}

// TestLongWindowRefusalDoesNotForgeAPerMinuteHeader guards the second defect
// the same capture surfaced: a session refusal filled x-ratelimit-reset-requests,
// which names the requests-per-minute window, with a reset four hours out.
func TestLongWindowRefusalDoesNotForgeAPerMinuteHeader(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	headers := RateLimitHeaders(LimitResult{
		Reason:            "session_limit_exceeded",
		Window:            ratewindows.Session,
		ResetAt:           now.Add(4 * time.Hour),
		RetryAfterSeconds: 14400,
		Session:           WindowState{Configured: true, Limit: 1000, Used: 1000, ResetAt: now.Add(4 * time.Hour), ResetSeconds: 14400},
	})
	if headers["x-ratelimit-limit-requests"] != "" || headers["x-ratelimit-reset-requests"] != "" {
		t.Fatalf("a long-window refusal forged per-minute request headers: %v", headers)
	}
	if headers["retry-after"] != "14400" {
		t.Fatalf("retry-after %q, want 14400", headers["retry-after"])
	}

	// And with a per-minute limit ALSO in play, its reset stays its own. The
	// long window's four hour wait belongs in retry-after and nowhere else.
	both := RateLimitHeaders(LimitResult{
		Reason:              "session_limit_exceeded",
		Window:              ratewindows.Session,
		ResetAt:             now.Add(4 * time.Hour),
		RetryAfterSeconds:   14400,
		RequestLimit:        60,
		RequestRemaining:    59,
		RequestResetSeconds: 30,
		Session:             WindowState{Configured: true, Limit: 1000, Used: 1000, ResetAt: now.Add(4 * time.Hour), ResetSeconds: 14400},
	})
	if both["x-ratelimit-reset-requests"] != "30" {
		t.Fatalf("per-minute reset %q, want 30: a long window overwrote the requests-per-minute reset", both["x-ratelimit-reset-requests"])
	}
	if both["retry-after"] != "14400" {
		t.Fatalf("retry-after %q, want the long window's 14400", both["retry-after"])
	}
}

// windowSnapshotWithKey configures BOTH scopes. Every other test in this file
// leaves KeyRatePolicy unconfigured, which is why the account-to-key merge had
// no coverage at all.
func windowSnapshotWithKey(accountSession, accountWeekly, keySession, keyWeekly int64, anchor time.Time) AuthSnapshot {
	snapshot := windowSnapshot(accountSession, accountWeekly, anchor)
	snapshot.KeyRatePolicy = &RatePolicy{
		RollingFiveHourLimit:  keySession,
		WeeklyLimit:           keyWeekly,
		FreeTokenWeightTenths: 1,
	}
	return snapshot
}

// TestRefusedRequestSpendsNoWindow is the one that matters for a subscriber's
// bill of allowance: a request that receives a 429 must not have spent the
// windows that admitted before the one that refused.
//
// Before this, window_score.lua INCRBYed on its own admit path, and the loop
// ran session before weekly, so a weekly refusal had already charged the
// session window. A client obeying the retry-after it was handed drained the
// session allowance on every attempt, and the component doing the charging was
// the component doing the refusing.
func TestRefusedRequestSpendsNoWindow(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	anchor := now.Add(-24 * time.Hour)
	limiter := newLiveLimiter(t, func() time.Time { return now })

	refused, err := limiter.Check(context.Background(), windowSnapshot(1000, 200, anchor), "hive-default", 300, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if refused.Allowed {
		t.Fatal("the weekly window admitted a request larger than its allowance")
	}
	if refused.Reason != "weekly_limit_exceeded" {
		t.Fatalf("reason %q, want weekly_limit_exceeded", refused.Reason)
	}
	if refused.Session.Used != 0 {
		t.Fatalf("the session window reports %d used on a refused request; nothing was admitted", refused.Session.Used)
	}

	// Now let the same spend through, with only the weekly window relaxed. If
	// the refused request had been charged, this window would already be
	// holding 300 and would report 600.
	allowed, err := limiter.Check(context.Background(), windowSnapshot(1000, 0, anchor), "hive-default", 300, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("refused unexpectedly: %+v", allowed)
	}
	if allowed.Session.Used != 300 {
		t.Fatalf("session used %d, want 300: the refused request was charged against a window that admitted it", allowed.Session.Used)
	}
}

// TestKeyRefusalDoesNotSpendTheAccountWindow is the same defect one level up:
// the account scope is scored in full before the key scope is consulted.
func TestKeyRefusalDoesNotSpendTheAccountWindow(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	anchor := now.Add(-24 * time.Hour)
	limiter := newLiveLimiter(t, func() time.Time { return now })

	refused, err := limiter.Check(context.Background(), windowSnapshotWithKey(1000, 0, 100, 0, anchor), "hive-default", 300, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if refused.Allowed {
		t.Fatal("the key's session window admitted a request three times its allowance")
	}
	// The reported window is the one that actually refused, not the account's
	// roomier view of the same window merged over the top of it.
	if refused.Session.Limit != 100 {
		t.Fatalf("session limit %d, want the refusing key scope's 100", refused.Session.Limit)
	}

	allowed, err := limiter.Check(context.Background(), windowSnapshotWithKey(1000, 0, 1000, 0, anchor), "hive-default", 300, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("refused unexpectedly: %+v", allowed)
	}
	if allowed.Session.Used != 300 {
		t.Fatalf("account session used %d, want 300: a key refusal charged the account window", allowed.Session.Used)
	}
}

// TestTighterScopeBindsOnSuccess covers the merge on an allowed check, which no
// test exercised because every one of them left the key policy unconfigured.
func TestTighterScopeBindsOnSuccess(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	anchor := now.Add(-24 * time.Hour)
	limiter := newLiveLimiter(t, func() time.Time { return now })

	result, err := limiter.Check(context.Background(), windowSnapshotWithKey(1000, 0, 500, 0, anchor), "hive-default", 100, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("refused unexpectedly: %+v", result)
	}
	if result.Session.Limit != 500 {
		t.Fatalf("session limit %d, want the tighter key scope's 500", result.Session.Limit)
	}
	// Post-charge on both sides, so the two are the same quantity and the
	// comparison that picked this one was a real comparison.
	if result.Session.Used != 100 || result.Session.Remaining != 400 {
		t.Fatalf("session state %+v, want 100 used and 400 remaining", result.Session)
	}
	if result.Session.UsedPercent() != 20 {
		t.Fatalf("session used %d percent, want 20", result.Session.UsedPercent())
	}
}

// TestNoAnchorSkipsTheWeeklyWindowRatherThanCountingElsewhere is the deploy-skew
// case: a new edge-api reading a snapshot an older control-plane cached, which
// carries no weekly_anchor_at at all.
//
// The old code fell back to the Unix epoch, so those requests counted into a
// bucket keyed off epoch while the console read the bucket keyed off the
// account's real anchor. The counts were not merely unenforced, they were
// permanently invisible.
func TestNoAnchorSkipsTheWeeklyWindowRatherThanCountingElsewhere(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	anchor := now.Add(-24 * time.Hour)
	limiter := newLiveLimiter(t, func() time.Time { return now })

	stale := windowSnapshot(0, 500, anchor)
	stale.AccountRatePolicy.WeeklyAnchorAt = nil

	result, err := limiter.Check(context.Background(), stale, "hive-default", 400, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("an anchorless snapshot refused a request: %+v", result)
	}
	if result.Weekly.Configured {
		t.Fatalf("the weekly window reported a state it could not count: %+v", result.Weekly)
	}

	// And nothing was written to any grid, so the account's real weekly bucket
	// still has its whole allowance once the anchor is back.
	restored, err := limiter.Check(context.Background(), windowSnapshot(0, 500, anchor), "hive-default", 500, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !restored.Allowed {
		t.Fatalf("the anchorless request was counted somewhere: %+v", restored.Weekly)
	}
}

// TestFutureAnchorIsRefusedRatherThanCountedElsewhere: the writer rejects one,
// and if a future anchor reaches the limiter anyway it must not silently move
// the account onto a second grid.
func TestFutureAnchorIsRefusedRatherThanCountedElsewhere(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	limiter := newLiveLimiter(t, func() time.Time { return now })

	future := windowSnapshot(0, 500, now.Add(48*time.Hour))
	result, err := limiter.Check(context.Background(), future, "hive-default", 400, 0, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if result.Weekly.Configured {
		t.Fatalf("a future anchor produced a counted weekly window: %+v", result.Weekly)
	}
}
