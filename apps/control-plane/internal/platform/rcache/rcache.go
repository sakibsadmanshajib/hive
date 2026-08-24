package rcache

import (
	"context"
	"encoding/json"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// DefaultTTL is the cache lifetime used when New is called with a non-positive
// ttl. Thirty seconds bounds how long any missed invalidation can serve stale
// data, while keeping the hot reads off the database for that window.
const DefaultTTL = 30 * time.Second

// Cache is a small read-through JSON cache over one Redis client.
//
// Boundary (money paths): this cache must NEVER hold credit balances,
// reservations, ledger entries, or billing state. Those are transactional
// money state (decision D-034 fail-closed); staleness there is a billing bug
// before it is a performance question. What belongs here is reference data:
// routing catalog rows (alias policies, route candidates, alias pricing) and
// per-tenant model visibility, whose staleness from any missed invalidation
// is bounded by the TTL.
//
// The cache degrades to a pass-through on any Redis failure, so it is never
// an availability dependency. Loader errors are never cached, so a transient
// database failure cannot pin a failed read for a full TTL.
type Cache struct {
	client *goredis.Client
	prefix string
	ttl    time.Duration
}

// New builds a Cache over client with key prefix and ttl; a non-positive ttl
// selects DefaultTTL. A nil client yields a cache that always misses (every
// GetJSON passes through to its loader), so callers can wire it
// unconditionally once the client exists.
func New(client *goredis.Client, prefix string, TTL time.Duration) *Cache {
	if TTL <= 0 {
		TTL = DefaultTTL
	}
	return &Cache{client: client, prefix: prefix, ttl: TTL}
}

// key returns the full Redis key for k. Nil-safe on purpose.
func (c *Cache) key(k string) string {
	if c == nil {
		return k
	}
	return c.prefix + ":" + k
}

// SetJSON stores v under key with the cache TTL. Fire-and-forget: a Redis
// failure here only costs a future cache miss, never an error to the caller.
func (c *Cache) SetJSON(ctx context.Context, key string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	if c == nil || c.client == nil {
		return
	}
	_ = c.client.Set(ctx, c.key(key), raw, c.ttl).Err()
}

// Delete removes keys (prefix applied). Fire-and-forget like SetJSON.
func (c *Cache) Delete(ctx context.Context, keys ...string) {
	if c == nil || c.client == nil || len(keys) == 0 {
		return
	}
	full := make([]string, 0, len(keys))
	for _, k := range keys {
		full = append(full, c.key(k))
	}
	_ = c.client.Del(ctx, full...).Err()
}

// GetJSON returns the cached JSON value for key, loading it through load on a
// miss and storing the result. A Redis GET failure (including redis.Nil) or a
// decode failure falls through to load; load errors propagate to the caller
// and are never stored.
func GetJSON[T any](ctx context.Context, c *Cache, key string, load func(context.Context) (T, error)) (T, error) {
	if c == nil || c.client == nil {
		return load(ctx)
	}
	if raw, err := c.client.Get(ctx, c.key(key)).Bytes(); err == nil {
		var v T
		if json.Unmarshal(raw, &v) == nil {
			return v, nil
		}
	}
	v, err := load(ctx)
	if err != nil {
		return v, err
	}
	c.SetJSON(ctx, key, v)
	return v, nil
}
