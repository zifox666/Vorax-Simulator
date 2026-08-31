package storage

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache struct{ Client *redis.Client }

func OpenCache(ctx context.Context, url string) (*Cache, error) {
	if url == "" {
		return nil, nil
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	opt.DialTimeout = time.Second
	opt.ReadTimeout = time.Second
	opt.WriteTimeout = time.Second
	c := redis.NewClient(opt)
	if err = c.Ping(ctx).Err(); err != nil {
		c.Close()
		return nil, err
	}
	return &Cache{Client: c}, nil
}
func (c *Cache) Get(ctx context.Context, key string) []byte {
	if c == nil {
		return nil
	}
	b, err := c.Client.Get(ctx, "vorax:response:"+key).Bytes()
	if err != nil {
		return nil
	}
	return b
}
func (c *Cache) Put(ctx context.Context, key string, b []byte) {
	if c != nil {
		c.Client.Set(ctx, "vorax:response:"+key, b, 5*time.Minute)
	}
}

// Redis is a best-effort limiter, never authoritative state. An outage fails
// open; token validation and deterministic command handling remain mandatory.
func (c *Cache) Allow(ctx context.Context, key string) bool {
	if c == nil {
		return true
	}
	x, err := c.Client.Eval(ctx, `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],60) end; return n`, []string{"vorax:rate:" + key}).Int64()
	return err != nil || x <= 120
}
