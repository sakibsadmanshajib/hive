package authz

import (
	"context"
	"encoding/json"
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

func (s *fakeSnapshotStore) Set(_ context.Context, key string, value string, _ time.Duration) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.setKeys = append(s.setKeys, key)
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
