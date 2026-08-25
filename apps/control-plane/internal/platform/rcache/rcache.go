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

// Hot-path dial/read/write timeouts. Deliberately far tighter than go-redis's
// defaults (dial 5s, read/write 3s): the cache serves several GETs per
// inference request, so a hung Redis must cost milliseconds, not seconds,
// before the pass-through path takes over. hotOpTimeout additionally bounds
// every single cache operation by context deadline, and MaxRetries is 1, so
// the worst case a fully hung Redis adds to a request is well under a second
// across all of its reads combined. MaxRetries=1 means one retry after the
// first failure; go-redis's default is 3 retries on its own backoff schedule.
const (
	hotDialTimeout = 500 * time.Millisecond
	hotIOTimeout   = 250 * time.Millisecond
	hotOpTimeout   = 150 * time.Millisecond
)

// cacheVersion stamps every stored envelope. Bump it whenever any wrapped
// struct's JSON shape changes: an entry written by an older binary then fails
// to decode as the current version and is treated as a miss (reloaded and
// overwritten) instead of partially decoding into wrong zero fields. The
// startup Flush in cmd/server/main.go makes this belt-and-braces for deploys,
// but rolling restarts with two live binaries make it load-bearing.
const cacheVersion = 1

type envelope struct {
	V int             `json:"v"`
	D json.RawMessage `json:"d"`
}

// New builds a Cache with its own Redis client from url (redis:// or bare
// host:port), key prefix, and ttl; a non-positive ttl selects DefaultTTL.
// The client is dedicated to this cache so the hot-path timeouts below never
// trade off against other Redis consumers sharing a client.
func New(url string, prefix string, ttl time.Duration) (*Cache, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		opts = &goredis.Options{Addr: url}
	}
	opts.DialTimeout = hotDialTimeout
	opts.ReadTimeout = hotIOTimeout
	opts.WriteTimeout = hotIOTimeout
	opts.MaxRetries = 1
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Cache{client: goredis.NewClient(opts), prefix: prefix, ttl: ttl}, nil
}

// Ping verifies connectivity. Callers use it to decide whether to enable the
// cache at startup; a failure here means run uncached, not fail startup.
func (c *Cache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Close releases the cache's dedicated client.
func (c *Cache) Close() error {
	return c.client.Close()
}

// Flush deletes every key under the cache prefix and returns how many were
// removed. Called once at startup so keys written by a previous binary (for
// example rows a migration has since repriced) cannot outlive the deploy;
// afterwards the cache repopulates from current database state.
func (c *Cache) Flush(ctx context.Context) (int, error) {
	pattern := c.prefix + ":*"
	var cursor uint64
	removed := 0
	for {
		keys, next, err := c.client.Scan(ctx, cursor, pattern, 500).Result()
		if err != nil {
			return removed, err
		}
		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return removed, err
			}
			removed += len(keys)
		}
		cursor = next
		if cursor == 0 {
			return removed, nil
		}
	}
}

// key returns the full Redis key for k. Nil-safe on purpose.
func (c *Cache) key(k string) string {
	if c == nil {
		return k
	}
	return c.prefix + ":" + k
}

// SetJSON stores v under key with the cache TTL, wrapped in a versioned
// envelope. Fire-and-forget: a Redis failure here only costs a future cache
// miss, never an error to the caller. The SET runs on an uncanceled copy of
// ctx with its own short deadline so a canceled request still warms the
// cache for its successors.
func (c *Cache) SetJSON(ctx context.Context, key string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	if c == nil || c.client == nil {
		return
	}
	body, err := json.Marshal(envelope{V: cacheVersion, D: raw})
	if err != nil {
		return
	}
	setCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*hotOpTimeout)
	defer cancel()
	_ = c.client.Set(setCtx, c.key(key), body, c.ttl).Err()
}

// Delete removes keys (prefix applied) and reports whether Redis accepted
// the deletion. It runs on an uncanceled copy of ctx on purpose: callers
// invoke it AFTER the database write has committed, often inside a request
// whose context may already be canceled, and a dropped DEL would leave a
// stale entry live for a full TTL with no signal. Callers must log a non-nil
// error loudly rather than fail the request: the database write is already
// durable, but a failed DEL means stale data can serve until the TTL
// expires, so the failure must be observable in logs.
func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if c == nil || c.client == nil {
		return nil
	}
	full := make([]string, 0, len(keys))
	for _, k := range keys {
		full = append(full, c.key(k))
	}
	delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*hotOpTimeout)
	defer cancel()
	return c.client.Del(delCtx, full...).Err()
}

// GetJSON returns the cached JSON value for key, loading it through load on a
// miss and storing the result. The GET runs under a short deadline so a hung
// Redis costs the request at most hotOpTimeout before the pass-through path
// takes over. A Redis failure (including redis.Nil), an undecodable body, or
// an envelope from a different cacheVersion all fall through to load; load
// errors propagate to the caller and are never stored.
func GetJSON[T any](ctx context.Context, c *Cache, key string, load func(context.Context) (T, error)) (T, error) {
	if c == nil || c.client == nil {
		return load(ctx)
	}
	getCtx, cancel := context.WithTimeout(ctx, hotOpTimeout)
	raw, err := c.client.Get(getCtx, c.key(key)).Bytes()
	cancel()
	if err == nil {
		var env envelope
		if json.Unmarshal(raw, &env) == nil && env.V == cacheVersion {
			var v T
			if json.Unmarshal(env.D, &v) == nil {
				return v, nil
			}
		}
	}
	v, err := load(ctx)
	if err != nil {
		return v, err
	}
	c.SetJSON(ctx, key, v)
	return v, nil
}
