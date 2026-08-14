package authz

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeSnapshotStore struct {
	values  map[string]string
	getKeys []string
	setKeys []string
	setTTLs []time.Duration
}

func (s *fakeSnapshotStore) Get(_ context.Context, key string) (string, error) {
	s.getKeys = append(s.getKeys, key)
	if s.values == nil {
		return "", redis.Nil
	}
	value, ok := s.values[key]
	if !ok {
		return "", redis.Nil
	}
	return value, nil
}

func (s *fakeSnapshotStore) Set(_ context.Context, key string, value string, ttl time.Duration) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.setKeys = append(s.setKeys, key)
	s.setTTLs = append(s.setTTLs, ttl)
	s.values[key] = value
	return nil
}

func TestResolveHydratesRedisFromControlPlane(t *testing.T) {
	rawToken := "hk_test_secret"
	tokenHash := HashBearerToken(rawToken)
	expected := AuthSnapshot{
		KeyID:          "key-1",
		AccountID:      "acc-1",
		Status:         "active",
		AllowAllModels: true,
		BudgetKind:     "none",
		PolicyVersion:  2,
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/internal/apikeys/resolve" {
			t.Fatalf("expected resolver path, got %s", r.URL.Path)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["token_hash"] != tokenHash {
			t.Fatalf("expected token hash %s, got %#v", tokenHash, body["token_hash"])
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(expected); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	cache := &fakeSnapshotStore{}
	client := &Client{
		cache:      cache,
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	got, err := client.Resolve(context.Background(), "Bearer "+rawToken)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected snapshot %#v, got %#v", expected, got)
	}

	cacheKey := "auth:key:{" + tokenHash + "}"
	if len(cache.setKeys) != 1 || cache.setKeys[0] != cacheKey {
		t.Fatalf("expected one cache write for %s, got %#v", cacheKey, cache.setKeys)
	}

	// #113: the snapshot TTL is a revocation backstop. Control-plane actively
	// DELETEs this exact key on revoke/disable/rotate, but if that invalidation
	// is ever missed (transient Redis error, instance divergence) the TTL must
	// still bound how long a revoked key keeps authorizing. Acceptance: ≤60s.
	if len(cache.setTTLs) != 1 {
		t.Fatalf("expected one TTL recorded, got %#v", cache.setTTLs)
	}
	if cache.setTTLs[0] <= 0 || cache.setTTLs[0] > 60*time.Second {
		t.Fatalf("snapshot TTL must be a positive ≤60s revocation backstop, got %s", cache.setTTLs[0])
	}

	var cached AuthSnapshot
	if err := json.Unmarshal([]byte(cache.values[cacheKey]), &cached); err != nil {
		t.Fatalf("unmarshal cached snapshot: %v", err)
	}
	if !reflect.DeepEqual(cached, expected) {
		t.Fatalf("expected cached snapshot %#v, got %#v", expected, cached)
	}

	got, err = client.Resolve(context.Background(), "Bearer "+rawToken)
	if err != nil {
		t.Fatalf("Resolve cached snapshot: %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected cached snapshot %#v, got %#v", expected, got)
	}
	if requests != 1 {
		t.Fatalf("expected a single control-plane fetch, got %d", requests)
	}
}

// A cold control-plane container (post-recreate, DB pool not yet warm) answers
// slower than the resolve client's timeout. That is a transport failure, not a
// verdict on the key: Resolve must let callers tell the two apart so a slow
// backend cannot masquerade as a nonexistent key (2026-08-14 live-box incident,
// GET /v1/models 401'd a valid key while control-plane logged its own
// "context canceled" resolving that same request).
func TestResolveClassifiesClientTimeoutAsUpstreamUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		cache:      &fakeSnapshotStore{},
		httpClient: &http.Client{Timeout: 5 * time.Millisecond},
		baseURL:    server.URL,
	}

	_, err := client.Resolve(context.Background(), "Bearer hk_test")
	if err == nil {
		t.Fatal("expected an error from a timed-out resolve call")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable for a client timeout, got %v", err)
	}
}

// A canceled context (caller gave up, or the parent request deadline expired)
// is the same shape as a timeout: it says nothing about the key.
func TestResolveClassifiesContextCancellationAsUpstreamUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		cache:      &fakeSnapshotStore{},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Resolve(ctx, "Bearer hk_test")
	if err == nil {
		t.Fatal("expected an error from a canceled-context resolve call")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable for a canceled context, got %v", err)
	}
}

// The control-plane itself returning a 5xx (its own DB call failed or was
// canceled) is the same "says nothing about the key" shape as a transport
// failure, and must classify the same way.
func TestResolveClassifiesControlPlane5xxAsUpstreamUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"request could not be completed"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		cache:      &fakeSnapshotStore{},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	_, err := client.Resolve(context.Background(), "Bearer hk_test")
	if err == nil {
		t.Fatal("expected an error from a 5xx resolve response")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable for a control-plane 5xx, got %v", err)
	}
}

// A genuine control-plane verdict that the key does not exist must NOT be
// classified as upstream-unavailable: the fix must not weaken a real
// rejection into a retryable 503.
func TestResolveDoesNotClassifyGenuineNotFoundAsUpstreamUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		cache:      &fakeSnapshotStore{},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	_, err := client.Resolve(context.Background(), "Bearer hk_test")
	if err == nil {
		t.Fatal("expected an error from a 404 resolve response")
	}
	if errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("a genuine not-found must not be classified upstream-unavailable, got %v", err)
	}
}
