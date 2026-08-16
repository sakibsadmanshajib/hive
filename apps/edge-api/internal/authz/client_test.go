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

// TestNewClientClonesDefaultTransportRatherThanReplacingIt is the regression
// guard for the PR #903 security review follow-up: a bare &http.Transport{}
// setting only MaxConnsPerHost silently zeroes every other default,
// including IdleConnTimeout, so a pooled connection to a control-plane
// container that has since been recreated would never expire and could
// serve a stale/broken connection across exactly the container-recreate
// scenario this PR exists to survive. NewClient must Clone()
// http.DefaultTransport and set MaxConnsPerHost on the clone, inheriting
// every other default deliberately instead of by omission.
func TestNewClientClonesDefaultTransportRatherThanReplacingIt(t *testing.T) {
	client, err := NewClient("http://control-plane.internal", "redis://localhost:6379/0")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.httpClient.Transport)
	}
	if transport.MaxConnsPerHost != resolveMaxConnsPerHost {
		t.Fatalf("MaxConnsPerHost = %d, want %d", transport.MaxConnsPerHost, resolveMaxConnsPerHost)
	}

	defaultTransport := http.DefaultTransport.(*http.Transport)
	if transport.IdleConnTimeout != defaultTransport.IdleConnTimeout {
		t.Fatalf("IdleConnTimeout = %v, want the inherited default %v (a bare &http.Transport{} would zero this, and a zero IdleConnTimeout means idle connections never expire across a control-plane container recreate)", transport.IdleConnTimeout, defaultTransport.IdleConnTimeout)
	}
	// MaxIdleConnsPerHost is deliberately NOT left at the inherited default
	// (2): it is raised to match MaxConnsPerHost so the 64 connections this
	// cap permits are actually kept alive for reuse instead of opened fresh
	// and discarded (second-pass security review).
	if transport.MaxIdleConnsPerHost != resolveMaxConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d (raised to match MaxConnsPerHost, not left at the stdlib default of 2)", transport.MaxIdleConnsPerHost, resolveMaxConnsPerHost)
	}
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

// A 200 status only means the headers arrived; the client's own Timeout still
// covers the body read (net/http cancels the request context mid-read when it
// fires), so a response that starts with a 200 and then stalls before
// finishing the body must classify the same as a transport failure, not fall
// through unclassified into a default invalid_api_key.
func TestResolveClassifiesStalledBodyReadAfterOKStatusAsUpstreamUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(50 * time.Millisecond)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		cache:      &fakeSnapshotStore{},
		httpClient: &http.Client{Timeout: 5 * time.Millisecond},
		baseURL:    server.URL,
	}

	_, err := client.Resolve(context.Background(), "Bearer hk_test")
	if err == nil {
		t.Fatal("expected an error from a stalled body read")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable for a stalled body read after a 200 status, got %v", err)
	}
}

// A genuine control-plane verdict that the key does not exist must NOT be
// classified as upstream-unavailable: the fix must not weaken a real
// rejection into a retryable 503. Uses the same Content-Type header the real
// handleKeyError writes (apikeys/http.go's writeJSON always sets
// application/json), which is exactly the signal that distinguishes this
// from the unmounted-route case below.
func TestResolveDoesNotClassifyGenuineNotFoundAsUpstreamUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"key not found"}`))
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

// TestResolveClassifiesUnmountedResolveRouteAsUpstreamUnavailable is the
// regression guard for the PR #903 security review HIGH finding: a
// control-plane that booted without a database pool never mounts
// /internal/apikeys/resolve at all (issue #816, router.go's
// RouterConfig.DBReady), so a request to it falls through to Go's own
// ServeMux 404 -- the exact cold/degraded boot state this PR exists to stop
// mapping to a false 401. A bare *http.ServeMux with the route deliberately
// never registered reproduces that exact response (plain-text body,
// Content-Type text/plain) rather than a hand-approximation of it.
func TestResolveClassifiesUnmountedResolveRouteAsUpstreamUnavailable(t *testing.T) {
	mux := http.NewServeMux() // /internal/apikeys/resolve deliberately not registered
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := &Client{
		cache:      &fakeSnapshotStore{},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	_, err := client.Resolve(context.Background(), "Bearer hk_test")
	if err == nil {
		t.Fatal("expected an error from an unmounted resolve route")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("an unmounted route (control-plane booted with no DB pool, issue #816) must classify as upstream-unavailable, not a genuine key verdict, got %v", err)
	}
}

// TestResolveClassifiesIntermediaryJSON404AsUpstreamUnavailable is the
// regression guard for the PR #903 second-pass security review finding that
// Content-Type alone is not a safe discriminator: a reverse proxy or API
// gateway in front of an operator-supplied CONTROL_PLANE_URL can answer its
// own unrouted-404 as application/json too (Kong and AWS API Gateway both
// do, with shapes like {"message": "..."}), which would satisfy the old
// Content-Type-only check and reproduce the exact false-401 this PR exists
// to remove, in a new costume. Requiring the body to also decode as
// control-plane's own {"error": "..."} envelope closes that gap.
func TestResolveClassifiesIntermediaryJSON404AsUpstreamUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		// Shaped like a gateway's own unrouted-404, not control-plane's
		// {"error": "..."} envelope: same Content-Type, different body.
		_, _ = w.Write([]byte(`{"message":"no Route matched with those values"}`))
	}))
	t.Cleanup(server.Close)

	client := &Client{
		cache:      &fakeSnapshotStore{},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	_, err := client.Resolve(context.Background(), "Bearer hk_test")
	if err == nil {
		t.Fatal("expected an error from an intermediary's JSON 404")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("a JSON 404 that isn't control-plane's own error envelope must classify as upstream-unavailable, not a genuine key verdict, got %v", err)
	}
}

// TestResolveClassifiesInternalTokenRejectionAsUpstreamUnavailable covers the
// PR #903 security review MEDIUM finding: /internal/apikeys/resolve
// authenticates only edge-api's own X-Internal-Token, so its 401 can only
// mean CONTROL_PLANE_INTERNAL_TOKEN is misconfigured on one side -- a
// permanent condition, still surfaced to the customer as the same retryable
// 503 (not their key's fault), but classified distinctly so it can be logged
// loudly rather than blending into transient-failure noise.
func TestResolveClassifiesInternalTokenRejectionAsUpstreamUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(server.Close)

	client := &Client{
		cache:      &fakeSnapshotStore{},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	_, err := client.Resolve(context.Background(), "Bearer hk_test")
	if err == nil {
		t.Fatal("expected an error from a rejected internal token")
	}
	if !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable (still a retryable 503 to the customer), got %v", err)
	}
	if !errors.Is(err, ErrInternalTokenRejected) {
		t.Fatalf("expected ErrInternalTokenRejected preserved for the operator-facing log, got %v", err)
	}
}
