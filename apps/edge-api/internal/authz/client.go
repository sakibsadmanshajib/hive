package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type snapshotStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
}

type redisSnapshotStore struct {
	client *redis.Client
}

func (s *redisSnapshotStore) Get(ctx context.Context, key string) (string, error) {
	if s == nil || s.client == nil {
		return "", redis.Nil
	}
	return s.client.Get(ctx, key).Result()
}

func (s *redisSnapshotStore) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Set(ctx, key, value, ttl).Err()
}

// HashBearerToken extracts the raw token from a Bearer header and returns its SHA-256 hash.
func HashBearerToken(authHeader string) string {
	raw := strings.TrimPrefix(authHeader, "Bearer ")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	h := sha256.Sum256([]byte(raw))
	return strings.ToLower(hex.EncodeToString(h[:]))
}

// Client orchestrates reading AuthSnapshots from Redis with fallback to the control plane.
type Client struct {
	cache      snapshotStore
	httpClient *http.Client
	baseURL    string

	// ResolveOverride is a test hook for bypassing Redis/control-plane I/O.
	ResolveOverride func(ctx context.Context, rawToken string) (AuthSnapshot, error)
}

// NewClient returns a new Client.
func NewClient(baseURL string, redisURL string) (*Client, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("authz: parse redis URL: %w", err)
	}

	return &Client{
		cache:      &redisSnapshotStore{client: redis.NewClient(opt)},
		httpClient: &http.Client{Timeout: 5 * time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
	}, nil
}

// Resolve returns the AuthSnapshot for the given raw token or Bearer header.
// It checks Redis first, then falls back to the control plane.
func (c *Client) Resolve(ctx context.Context, rawToken string) (AuthSnapshot, error) {
	if c != nil && c.ResolveOverride != nil {
		return c.ResolveOverride(ctx, rawToken)
	}
	rawToken = strings.TrimPrefix(rawToken, "Bearer ")
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return AuthSnapshot{}, errors.New("authz: empty token")
	}

	h := sha256.Sum256([]byte(rawToken))
	tokenHash := strings.ToLower(hex.EncodeToString(h[:]))
	redisKey := "auth:key:{" + tokenHash + "}"

	// 1. Try Redis cache.
	if c.cache != nil {
		cached, err := c.cache.Get(ctx, redisKey)
		if err == nil {
			var snap AuthSnapshot
			if err := json.Unmarshal([]byte(cached), &snap); err == nil {
				return snap, nil
			}
			// on decode error, fall through to fetch
		} else if err != redis.Nil {
			// Log error but fall through to fetch if Redis fails
			// TODO: hook up logger
		}
	}

	// 2. Fallback to control plane.
	body := fmt.Sprintf(`{"token_hash":%q}`, tokenHash)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/apikeys/resolve",
		strings.NewReader(body),
	)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("authz: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("authz: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return AuthSnapshot{}, fmt.Errorf("authz: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("authz: read response: %w", err)
	}

	var snap AuthSnapshot
	if err := json.Unmarshal(respBytes, &snap); err != nil {
		return AuthSnapshot{}, fmt.Errorf("authz: decode snapshot: %w", err)
	}

	// 3. Cache in Redis (fire and forget).
	// We use an arbitrary TTL here, though Plan 05-03 might change this to
	// rely on explicit invalidation from the control plane. Set to 1 hour for now.
	if c.cache != nil {
		_ = c.cache.Set(ctx, redisKey, string(respBytes), time.Hour)
	}

	return snap, nil
}
