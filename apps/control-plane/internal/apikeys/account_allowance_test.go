package apikeys

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/packages/ratewindows"
)

// windowRepo adds account-scope window storage to the shared stub repository.
// The service reaches it through an optional interface, so this is all it takes.
type windowRepo struct {
	*stubRepo
	limits map[uuid.UUID]AccountRateLimits
}

func newWindowRepo() *windowRepo {
	return &windowRepo{stubRepo: newStubRepo(), limits: map[uuid.UUID]AccountRateLimits{}}
}

func (r *windowRepo) GetAccountRateLimits(_ context.Context, accountID uuid.UUID) (AccountRateLimits, error) {
	if existing, ok := r.limits[accountID]; ok {
		return existing, nil
	}
	return AccountRateLimits{AccountID: accountID}, nil
}

func (r *windowRepo) UpsertAccountRateLimits(_ context.Context, accountID uuid.UUID, input AccountRateLimitsInput) (AccountRateLimits, error) {
	if err := validateWindowLimit(input.SessionLimit); err != nil {
		return AccountRateLimits{}, err
	}
	if err := validateWindowLimit(input.WeeklyLimit); err != nil {
		return AccountRateLimits{}, err
	}
	// COALESCE($4, public.account_rate_policies.weekly_anchor_at), exactly as
	// the pgx writer does. This double used to substitute time.Now() for an
	// omitted anchor, which is the opposite behaviour: moving an account's
	// anchor changes the weekly bucket KEY, which resets that account's weekly
	// consumption to zero mid week.
	anchor := time.Now().UTC()
	if existing, ok := r.limits[accountID]; ok && !existing.WeeklyAnchorAt.IsZero() {
		anchor = existing.WeeklyAnchorAt
	}
	if input.WeeklyAnchorAt != nil {
		anchor = *input.WeeklyAnchorAt
	}
	stored := AccountRateLimits{
		AccountID:      accountID,
		SessionLimit:   input.SessionLimit,
		WeeklyLimit:    input.WeeklyLimit,
		WeeklyAnchorAt: anchor,
	}
	r.limits[accountID] = stored
	return stored, nil
}

func newWindowHandler(t *testing.T, admin bool) (*Handler, *windowRepo, uuid.UUID) {
	t.Helper()
	repo := newWindowRepo()
	vc := ownerVC()
	h := &Handler{svc: NewService(repo), testVC: &vc, policy: authz.NewPolicy()}
	if admin {
		h.testActor = &authz.Actor{IsAdmin: true, Verified: true, Role: "owner"}
	}
	return h, repo, vc.CurrentAccount.ID
}

// TestAccountWindowWriterExists is the whole of stage one: before this, the two
// columns had no writer anywhere and no configured limit could exist.
func TestAccountWindowWriterExists(t *testing.T) {
	h, _, _ := newWindowHandler(t, true)

	rr := doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{
		"session_limit": 1_000_000,
		"weekly_limit":  20_000_000,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("put rate limits: %d %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["session_limit"].(float64) != 1_000_000 {
		t.Fatalf("session_limit not stored: %#v", body["session_limit"])
	}
	if body["session_configured"] != true || body["weekly_configured"] != true {
		t.Fatalf("configured flags wrong: %#v", body)
	}

	rr = doRequest(t, h, http.MethodGet, accountRateLimitsPath, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get rate limits: %d %s", rr.Code, rr.Body.String())
	}
	if decodeBody(t, rr)["weekly_limit"].(float64) != 20_000_000 {
		t.Fatal("weekly limit did not round trip")
	}
}

// TestZeroIsNotAConfigurableLimit: zero used to mean "unlimited" silently, so an
// operator typing it got the opposite of what they asked for.
func TestZeroIsNotAConfigurableLimit(t *testing.T) {
	h, _, _ := newWindowHandler(t, true)

	rr := doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{"session_limit": 0})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a zero limit was accepted: %d %s", rr.Code, rr.Body.String())
	}

	// Set one, so the clearing assertion below is about clearing rather than
	// about a value that was never there. The first version of this test
	// cleared an already-unset limit and passed over a handler that could not
	// tell an omitted field from an explicit null at all.
	rr = doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{
		"session_limit": 900_000,
		"weekly_limit":  800_000,
	})
	if rr.Code != http.StatusOK || decodeBody(t, rr)["session_configured"] != true {
		t.Fatalf("seeding a limit failed: %d %s", rr.Code, rr.Body.String())
	}

	// An omitted field leaves its limit alone.
	rr = doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{"weekly_limit": 700_000})
	if rr.Code != http.StatusOK {
		t.Fatalf("partial update failed: %d %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["session_limit"] == nil || body["session_limit"].(float64) != 900_000 {
		t.Fatalf("an omitted field cleared its limit: %#v", body["session_limit"])
	}

	// Null is how a limit is removed, and it has to be told apart from absent.
	rr = doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{"session_limit": nil})
	if rr.Code != http.StatusOK {
		t.Fatalf("clearing a limit failed: %d %s", rr.Code, rr.Body.String())
	}
	body = decodeBody(t, rr)
	if body["session_configured"] != false {
		t.Fatalf("a cleared limit is still reported as configured: %#v", body)
	}
	if body["weekly_configured"] != true {
		t.Fatalf("clearing one window cleared the other: %#v", body)
	}
}

// TestOnlyPlatformAdminSetsAnAllowance: the allowance is Hive's lever, not the
// customer's (issue #1684).
func TestOnlyPlatformAdminSetsAnAllowance(t *testing.T) {
	h, _, _ := newWindowHandler(t, false)

	rr := doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{"session_limit": 5})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("an account owner set their own allowance: %d %s", rr.Code, rr.Body.String())
	}
}

// TestUsageWindowsReportPercentAndReset is the display half, read end to end
// over the real key shapes the edge limiter writes.
func TestUsageWindowsReportPercentAndReset(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	h, repo, accountID := newWindowHandler(t, true)
	reader := NewUsageWindowReader(client)
	now := time.Date(2026, time.September, 2, 15, 0, 0, 0, time.UTC)
	reader.now = func() time.Time { return now }
	h = h.WithUsageWindows(reader)

	anchor := time.Date(2026, time.August, 31, 9, 0, 0, 0, time.UTC)
	sessionLimit := int64(1000)
	weeklyLimit := int64(4000)
	repo.limits[accountID] = AccountRateLimits{
		AccountID:      accountID,
		SessionLimit:   &sessionLimit,
		WeeklyLimit:    &weeklyLimit,
		WeeklyAnchorAt: anchor,
	}

	// Write counters exactly as the limiter would.
	session := ratewindows.SessionShape()
	sessionKey := ratewindows.BucketKey(
		ratewindows.FamilyPrefix(ratewindows.AccountPrefix(accountID.String()), session.Name),
		ratewindows.Bucket(now, session.EffectiveAnchor(anchor), session.BucketSize),
	)
	if err := client.Set(context.Background(), sessionKey, strconv.FormatInt(370, 10), time.Hour).Err(); err != nil {
		t.Fatalf("seed session bucket: %v", err)
	}
	weekly := ratewindows.WeeklyShape()
	weeklyKey := ratewindows.BucketKey(
		ratewindows.FamilyPrefix(ratewindows.AccountPrefix(accountID.String()), weekly.Name),
		ratewindows.Bucket(now, anchor, weekly.BucketSize),
	)
	if err := client.Set(context.Background(), weeklyKey, strconv.FormatInt(1000, 10), time.Hour).Err(); err != nil {
		t.Fatalf("seed weekly bucket: %v", err)
	}

	rr := doRequest(t, h, http.MethodGet, accountUsageWindowsPath, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage windows: %d %s", rr.Code, rr.Body.String())
	}
	raw := rr.Body.String()

	// The response has to carry percentages and a reset, and must not carry the
	// allowance itself: it is a credit figure, credits convert to dollars by a
	// constant the console publishes, and a subscription's internal credit
	// value is confidential (D-068, D-070).
	for _, forbidden := range []string{"1000", "4000", "session_limit", "weekly_limit", "credits"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("usage window response leaks %q: %s", forbidden, raw)
		}
	}

	body := decodeBody(t, rr)
	windows, ok := body["windows"].([]interface{})
	if !ok || len(windows) != 2 {
		t.Fatalf("expected two windows, got %#v", body["windows"])
	}
	first := windows[0].(map[string]interface{})
	if first["window"] != ratewindows.Session {
		t.Fatalf("first window %#v", first)
	}
	if first["used_percent"].(float64) != 37 {
		t.Fatalf("session used_percent %#v, want 37", first["used_percent"])
	}
	if first["resets_at"] == nil || first["resets_at"].(string) == "" {
		t.Fatal("session window carries no reset time")
	}
	if first["anchored"] != false {
		t.Fatal("the session window is sliding, not anchored")
	}

	second := windows[1].(map[string]interface{})
	if second["used_percent"].(float64) != 25 {
		t.Fatalf("weekly used_percent %#v, want 25", second["used_percent"])
	}
	if second["anchored"] != true {
		t.Fatal("the weekly window is anchored (D-069)")
	}
	wantReset := anchor.Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if second["resets_at"] != wantReset {
		t.Fatalf("weekly resets_at %#v, want %q", second["resets_at"], wantReset)
	}
}

// TestUnconfiguredWindowIsReportedUnlimited: absence has to be visible, or a
// surface prints an empty bar for a limit nobody ever set.
func TestUnconfiguredWindowIsReportedUnlimited(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	h, _, _ := newWindowHandler(t, true)
	h = h.WithUsageWindows(NewUsageWindowReader(client))

	rr := doRequest(t, h, http.MethodGet, accountUsageWindowsPath, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage windows: %d %s", rr.Code, rr.Body.String())
	}
	for _, window := range decodeBody(t, rr)["windows"].([]interface{}) {
		entry := window.(map[string]interface{})
		if entry["configured"] != false {
			t.Fatalf("window %#v reported as configured while unset", entry)
		}
		if entry["used_percent"].(float64) != 0 {
			t.Fatalf("window %#v reports consumption of an unset limit", entry)
		}
	}
}

// TestUsageWindowsUnavailableIsNotZero: a display that reads zero when its
// backing store is unreachable tells the customer they have used nothing.
func TestUsageWindowsUnavailableIsNotZero(t *testing.T) {
	h, _, _ := newWindowHandler(t, true)

	rr := doRequest(t, h, http.MethodGet, accountUsageWindowsPath, nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no reader wired, got %d %s", rr.Code, rr.Body.String())
	}
}

// TestPartialUpdateDoesNotMoveTheAnchor is the anchor half of "an omitted field
// is left alone", which the limit half was covering and this was not.
//
// It is load bearing: the weekly Redis bucket key is derived from the anchor,
// so moving it resets that account's weekly consumption to zero part way
// through the week. The writer COALESCEs for exactly this reason.
func TestPartialUpdateDoesNotMoveTheAnchor(t *testing.T) {
	h, _, _ := newWindowHandler(t, true)

	anchor := time.Date(2026, time.August, 31, 9, 0, 0, 0, time.UTC)
	rr := doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{
		"session_limit":    900_000,
		"weekly_anchor_at": anchor.Format(time.RFC3339),
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", rr.Code, rr.Body.String())
	}
	if got := decodeBody(t, rr)["weekly_anchor_at"]; got != anchor.Format(time.RFC3339) {
		t.Fatalf("anchor did not round trip: %#v", got)
	}

	rr = doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{"weekly_limit": 700_000})
	if rr.Code != http.StatusOK {
		t.Fatalf("partial update: %d %s", rr.Code, rr.Body.String())
	}
	if got := decodeBody(t, rr)["weekly_anchor_at"]; got != anchor.Format(time.RFC3339) {
		t.Fatalf("an omitted anchor moved to %#v; the account's weekly consumption just reset mid week", got)
	}
}

// TestFutureAnchorIsRefused: the limiter and this service's own consumption
// reader both derive the weekly bucket key from the anchor and both refuse to
// count from one in the future. Accepting it here would leave the gateway
// refusing on a window the console reports as zero percent used.
func TestFutureAnchorIsRefused(t *testing.T) {
	h, _, _ := newWindowHandler(t, true)

	rr := doRequest(t, h, http.MethodPut, accountRateLimitsPath, map[string]interface{}{
		"session_limit":    1000,
		"weekly_anchor_at": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a future anchor was accepted: %d %s", rr.Code, rr.Body.String())
	}
	if decodeBody(t, rr)["code"] != "weekly_anchor_not_usable" {
		t.Fatalf("wrong code: %s", rr.Body.String())
	}
}

// TestAdminConfiguresAnotherAccount: the allowance is Hive's lever on a
// SUBSCRIBER. A route that can only ever write the caller's own account row is
// machinery, not a usable stage one.
func TestAdminConfiguresAnotherAccount(t *testing.T) {
	h, repo, ownAccount := newWindowHandler(t, true)
	other := uuid.New()

	rr := doRequest(t, h, http.MethodPut, accountRateLimitsPath+"?account_id="+other.String(), map[string]interface{}{
		"session_limit": 1_234_000,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("put on another account: %d %s", rr.Code, rr.Body.String())
	}
	if body := decodeBody(t, rr); body["account_id"] != other.String() {
		t.Fatalf("wrote to %#v, want %s", body["account_id"], other)
	}
	if stored, ok := repo.limits[other]; !ok || stored.SessionLimit == nil || *stored.SessionLimit != 1_234_000 {
		t.Fatalf("the other account's limit was not stored: %#v", repo.limits)
	}
	if _, ok := repo.limits[ownAccount]; ok {
		t.Fatal("the admin's own account was written instead of the named one")
	}

	rr = doRequest(t, h, http.MethodGet, accountRateLimitsPath+"?account_id="+other.String(), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get on another account: %d %s", rr.Code, rr.Body.String())
	}
	if decodeBody(t, rr)["session_limit"].(float64) != 1_234_000 {
		t.Fatalf("read back the wrong account: %s", rr.Body.String())
	}

	rr = doRequest(t, h, http.MethodGet, accountRateLimitsPath+"?account_id=not-a-uuid", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("a malformed account_id was accepted: %d %s", rr.Code, rr.Body.String())
	}
}

// TestUsageWindowsUnavailableWhenTheAnchorIsUnusable: an anchored window with
// no countable anchor reads as unavailable, never as zero percent used. A bar
// at zero is a claim about the customer's consumption; this code cannot support
// one, because it does not know which counter to read.
func TestUsageWindowsUnavailableWhenTheAnchorIsUnusable(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	h, repo, accountID := newWindowHandler(t, true)
	now := time.Date(2026, time.September, 2, 15, 0, 0, 0, time.UTC)
	reader := NewUsageWindowReader(client)
	reader.now = func() time.Time { return now }
	h = h.WithUsageWindows(reader)

	weeklyLimit := int64(4000)
	repo.limits[accountID] = AccountRateLimits{
		AccountID:      accountID,
		WeeklyLimit:    &weeklyLimit,
		WeeklyAnchorAt: now.Add(72 * time.Hour),
	}

	rr := doRequest(t, h, http.MethodGet, accountUsageWindowsPath, nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("a future anchor was reported as consumption: %d %s", rr.Code, rr.Body.String())
	}
}
