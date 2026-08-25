package rcache_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/rcache"
)

func newTestCache(t *testing.T) (*rcache.Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cache, err := rcache.New("redis://"+mr.Addr(), "test:v1", 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return cache, mr
}

func TestGetJSON_LoadsAndCaches(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	calls := 0
	load := func(context.Context) (map[string]int, error) {
		calls++
		return map[string]int{"n": 42}, nil
	}

	got, err := rcache.GetJSON(ctx, cache, "k1", load)
	if err != nil || got["n"] != 42 || calls != 1 {
		t.Fatalf("first read: got=%v err=%v calls=%d", got, err, calls)
	}

	got, err = rcache.GetJSON(ctx, cache, "k1", load)
	if err != nil || got["n"] != 42 || calls != 1 {
		t.Fatalf("second read should hit cache: got=%v err=%v calls=%d", got, err, calls)
	}
	if val, _ := mr.Get("test:v1:k1"); !strings.Contains(val, "42") {
		t.Fatal("expected marshalled value stored under prefixed key")
	}
}

func TestGetJSON_TTLExpiryReloads(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	calls := 0
	load := func(context.Context) (int, error) {
		calls++
		return calls, nil
	}

	if _, err := rcache.GetJSON(ctx, cache, "k", load); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(61 * time.Second)

	got, err := rcache.GetJSON(ctx, cache, "k", load)
	if err != nil || got != 2 || calls != 2 {
		t.Fatalf("expired entry should reload: got=%d err=%v calls=%d", got, err, calls)
	}
}

func TestGetJSON_NilCachePassesThrough(t *testing.T) {
	calls := 0
	load := func(context.Context) (string, error) {
		calls++
		return "db", nil
	}
	for i := 0; i < 3; i++ {
		got, err := rcache.GetJSON(context.Background(), nil, "k", load)
		if err != nil || got != "db" {
			t.Fatalf("nil cache passthrough: got=%q err=%v", got, err)
		}
	}
	if calls != 3 {
		t.Fatalf("nil cache must always load: calls=%d", calls)
	}
}

func TestGetJSON_RedisDownStillServes(t *testing.T) {
	mr := miniredis.RunT(t)
	cache, err := rcache.New("redis://"+mr.Addr(), "t", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Load once to populate, then kill Redis.
	if _, err := rcache.GetJSON(ctx, cache, "k", func(context.Context) (int, error) {
		return 1, nil
	}); err != nil {
		t.Fatal(err)
	}
	mr.Close()

	got, err := rcache.GetJSON(ctx, cache, "k", func(context.Context) (int, error) {
		return 7, nil // DB still answers while Redis is down
	})
	if err != nil || got != 7 {
		t.Fatalf("redis failure must degrade to DB load, not error: got=%d err=%v", got, err)
	}
}

func TestGetJSON_ErrorsAreNeverCached(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()

	boom := errors.New("boom")
	if _, err := rcache.GetJSON(ctx, cache, "k", func(context.Context) (int, error) {
		return 0, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("load error must surface: %v", err)
	}

	calls := 0
	if _, err := rcache.GetJSON(ctx, cache, "k", func(context.Context) (int, error) {
		calls++
		return 5, nil
	}); err != nil || calls != 1 {
		t.Fatalf("previous error must not have been cached: calls=%d err=%v", calls, err)
	}
}

func TestDelete_RemovesKeysAndReportsErrors(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	cache.SetJSON(ctx, "a", 1)
	cache.SetJSON(ctx, "b", 2)
	if err := cache.Delete(ctx, "a"); err != nil {
		t.Fatalf("delete against healthy redis: %v", err)
	}
	if mr.Exists("test:v1:a") {
		t.Fatal("deleted key still present")
	}
	if !mr.Exists("test:v1:b") {
		t.Fatal("unrelated key was removed")
	}

	// A failed DEL must be observable to the caller: the invalidation
	// contract is that stale-data risk is logged, never silent.
	mr.Close()
	if err := cache.Delete(ctx, "a"); err == nil {
		t.Fatal("delete against dead redis must return an error")
	}
}

func TestFlush_RemovesEveryPrefixedKey(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	for _, k := range []string{"rt:pol:x", "rt:cand:y", "cat:public"} {
		cache.SetJSON(ctx, k, 1)
	}
	cache.SetJSON(ctx, "", 0) // prefix-only key must be swept too

	n, err := cache.Flush(ctx)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if n < 4 {
		t.Fatalf("flushed %d keys, want >= 4", n)
	}
	if len(mr.Keys()) != 0 {
		t.Fatalf("keys survived flush: %v", mr.Keys())
	}
}

func TestGetJSON_VersionMismatchTreatedAsMiss(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	// Simulate an entry written by an older binary with a different envelope
	// version: stored raw, no "v" field.
	if err := mr.Set("test:v1:k", `{"stale":"pre-deploy shape"}`); err != nil {
		t.Fatal(err)
	}

	calls := 0
	got, err := rcache.GetJSON(ctx, cache, "k", func(context.Context) (map[string]any, error) {
		calls++
		return map[string]any{"fresh": true}, nil
	})
	if err != nil || calls != 1 || got["fresh"] != true {
		t.Fatalf("version-mismatched entry must reload: got=%v calls=%d err=%v", got, calls, err)
	}

	// And the fresh value is now cached under the current version.
	calls = 0
	got, err = rcache.GetJSON(ctx, cache, "k", func(context.Context) (map[string]any, error) {
		calls++
		return map[string]any{}, nil
	})
	if err != nil || calls != 0 || got["fresh"] != true {
		t.Fatalf("freshly loaded value must be cached: got=%v calls=%d", got, calls)
	}
}
