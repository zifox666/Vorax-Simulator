package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"vorax/internal/engine"
)

func TestPostgresImmutableContentRoundTrip(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := LoadContent(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadContent(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != engine.RulesVersion || len(first.Cards) != len(second.Cards) {
		t.Fatal("content round trip drift")
	}
}

func TestRedisCacheRateLimitAndFailureFallback(t *testing.T) {
	url := os.Getenv("REDIS_TEST_URL")
	if url == "" {
		t.Skip("REDIS_TEST_URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cache, err := OpenCache(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("test-%d", time.Now().UnixNano())
	cache.Put(ctx, key, []byte("response"))
	if string(cache.Get(ctx, key)) != "response" {
		t.Fatal("cache round trip failed")
	}
	for i := 0; i < 120; i++ {
		if !cache.Allow(ctx, key) {
			t.Fatal("limited too early")
		}
	}
	if cache.Allow(ctx, key) {
		t.Fatal("rate limit not enforced")
	}
	cache.Client.Close()
	if cache.Get(ctx, key) != nil || !cache.Allow(ctx, key) {
		t.Fatal("Redis failure changed correctness path")
	}
}
