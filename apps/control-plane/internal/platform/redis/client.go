package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

// NewClient builds a Redis client from either a redis:// URL or a raw address.
func NewClient(addr string) *goredis.Client {
	opts, err := goredis.ParseURL(addr)
	if err != nil {
		opts = &goredis.Options{Addr: addr}
	}

	return goredis.NewClient(opts)
}

// Ping verifies the client can reach Redis.
func Ping(ctx context.Context, client *goredis.Client) error {
	return client.Ping(ctx).Err()
}
