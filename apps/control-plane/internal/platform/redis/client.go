package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

// NewClient builds a Redis client from either a redis:// URL or a raw address.
//
// ContextTimeoutEnabled is set because without it go-redis IGNORES the caller's
// context deadline for command reads and writes, falling back to its own
// timeouts (5 second dial, 3 second read) with retries on top. Every caller here
// passes a context and reasonably assumes it bounds the call; measured, it did
// not, and a Redis that accepts connections and then answers nothing held a
// caller for five seconds despite a 500 millisecond deadline
// (TestRecordSettledSpend_IsBoundedWhenRedisHangs). That matters most on the
// budget spend counter, which runs inside a per-account Postgres advisory lock
// while holding a pool connection, on a deployment where one pool has already
// turned into a chat outage.
//
// A caller that passes no deadline is unaffected and still gets the go-redis
// defaults. A caller that passes one now gets what it asked for.
func NewClient(addr string) *goredis.Client {
	opts, err := goredis.ParseURL(addr)
	if err != nil {
		opts = &goredis.Options{Addr: addr}
	}
	opts.ContextTimeoutEnabled = true

	return goredis.NewClient(opts)
}

// Ping verifies the client can reach Redis.
func Ping(ctx context.Context, client *goredis.Client) error {
	return client.Ping(ctx).Err()
}
