package authz

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// Issue #915. These two tests pin the answer to "does presenting an expired
// Hive API key authorize a request?" at the entry point that actually decides
// it, Authorizer.Authorize, rather than at either half of the check in
// isolation.
//
// Expiry is enforced twice, independently, and each layer already has its own
// unit test:
//
//   - control-plane derives the status: apikeys.Service.ResolveSnapshot calls
//     applyExpiry before projecting the snapshot, so a key whose durable
//     api_keys.status is still 'active' resolves as "expired"
//     (TestResolveSnapshotReturnsExpiredWhenExpiresAtHasPassed).
//   - edge-api re-checks the timestamp: CheckAccess step 2 compares
//     snapshot.ExpiresAt against the current time regardless of the status it
//     was handed (TestCheckAccessExpiredKey).
//
// What neither of those pins is the composition, which is what the incident
// behind #915 was actually about: six keys whose stored status read 'active'
// while expires_at had passed. Both tests below use exactly that shape, so
// removing either layer turns one of them red.
//
// The shape is also what a stale cached snapshot looks like. The edge caches a
// resolved snapshot in Redis for up to snapshotTTL, so a snapshot minted while
// the key was still live keeps its "active" status for the rest of that TTL
// after the key expires. The second test drives that path through the cache
// itself.

// pastExpiryActiveSnapshot returns the incident shape: a key whose durable
// status is still active, with expires_at an hour in the past.
func pastExpiryActiveSnapshot() AuthSnapshot {
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	return AuthSnapshot{
		KeyID:          "key-1",
		AccountID:      "acc-1",
		TenantID:       "11111111-1111-1111-1111-111111111111",
		Status:         "active",
		ExpiresAt:      &past,
		AllowAllModels: true,
		BudgetKind:     "none",
	}
}

// assertExpiredDenial fails unless err is the specific expired-key denial.
// Asserting the message, not just the code, matters: a resolve failure or any
// other invalid_api_key branch would satisfy a code-only assertion and let the
// test pass for the wrong reason.
func assertExpiredDenial(t *testing.T, err *apierrors.OpenAIError) {
	t.Helper()

	if err == nil {
		t.Fatal("expired key was authorized: Authorize returned no error")
	}
	if err.Error.Code == nil || *err.Error.Code != "invalid_api_key" {
		t.Fatalf("code = %v, want %q", err.Error.Code, "invalid_api_key")
	}
	const want = "Incorrect API key provided: API key has expired"
	if err.Error.Message != want {
		t.Fatalf("message = %q, want %q", err.Error.Message, want)
	}
}

// TestAuthorizeDeniesExpiredKeyWhoseStoredStatusIsStillActive is the direct
// answer to #915: an expired-but-not-revoked key does not authorize, and the
// denial does not depend on the durable status column having been rewritten.
func TestAuthorizeDeniesExpiredKeyWhoseStoredStatusIsStillActive(t *testing.T) {
	client := &Client{
		ResolveOverride: func(_ context.Context, _ string) (AuthSnapshot, error) {
			return pastExpiryActiveSnapshot(), nil
		},
	}

	_, _, err := NewAuthorizer(client, nil).
		Authorize(context.Background(), "Bearer hk_expired", "hive-default", 50, 100, 20)

	assertExpiredDenial(t, err)
}

// TestCachedSnapshotCannotOutliveKeyExpiry covers the cache half of the same
// question. A snapshot cached while the key was still live carries
// status "active" for the rest of snapshotTTL, and the cache hit returns
// before any control-plane call, so the only thing standing between that
// cached value and a served request is CheckAccess re-reading expires_at.
//
// baseURL is deliberately unroutable: if the cache were missed, the fallback
// HTTP resolve would fail and produce an upstream_unavailable error instead,
// which assertExpiredDenial rejects. So this cannot pass without the cache
// having actually been read and the expiry having actually been enforced on it.
func TestCachedSnapshotCannotOutliveKeyExpiry(t *testing.T) {
	cached, err := json.Marshal(pastExpiryActiveSnapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	// Same key derivation as Client.Resolve: sha256 of the raw token, wrapped
	// in the Redis hash tag.
	const rawToken = "hk_cached_expired"
	store := &fakeSnapshotStore{
		values: map[string]string{
			"auth:key:{" + HashBearerToken("Bearer "+rawToken) + "}": string(cached),
		},
	}

	client := &Client{cache: store, baseURL: "http://control-plane.invalid"}

	_, _, authErr := NewAuthorizer(client, nil).
		Authorize(context.Background(), "Bearer "+rawToken, "hive-default", 50, 100, 20)

	assertExpiredDenial(t, authErr)

	if len(store.getKeys) == 0 {
		t.Fatal("cache was never read, so this test did not exercise the cached path")
	}
}
