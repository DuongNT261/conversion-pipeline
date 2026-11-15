package cache

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/duongnt261/conversion-pipeline/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeDB answers from a map and counts how often MySQL would be asked.
type fakeDB struct {
	statuses map[int64]string
	calls    int
}

func (f *fakeDB) CampaignStatus(_ context.Context, id int64) (string, error) {
	f.calls++
	status, ok := f.statuses[id]
	if !ok {
		return "", store.ErrNotFound
	}
	return status, nil
}

// testRedis uses DB 15, so tests never touch the worker's keys in DB 0.
func testRedis(t *testing.T) *redis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set, skipping Redis integration test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	rdb.Del(context.Background(), "campaign:1", "campaign:10", "campaign:215", StatsKey)
	return rdb
}

func TestCampaignStatusCacheAside(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	db := &fakeDB{statuses: map[int64]string{1: "ACTIVE", 10: "PAUSED"}}
	c := NewCampaigns(rdb, db, time.Minute, quiet)

	for i := 0; i < 3; i++ {
		if got, err := c.CampaignStatus(ctx, 1); err != nil || got != "ACTIVE" {
			t.Fatalf("campaign 1: want ACTIVE, got %q, %v", got, err)
		}
		if _, err := c.CampaignStatus(ctx, 215); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("campaign 215: want ErrNotFound, got %v", err)
		}
	}
	// Only the first lookup of each campaign reaches MySQL, "not found" included.
	if db.calls != 2 || c.hits.Load() != 4 || c.misses.Load() != 2 {
		t.Fatalf("want 2 db calls, 4 hits, 2 misses; got %d, %d, %d", db.calls, c.hits.Load(), c.misses.Load())
	}
	ttl := rdb.TTL(ctx, "campaign:1").Val()
	if ttl < 50*time.Second || ttl > 70*time.Second {
		t.Fatalf("want ttl of 1m ±10%%, got %v", ttl)
	}

	if err := c.FlushStats(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	stats := rdb.HGetAll(ctx, StatsKey).Val()
	if stats["hits"] != "4" || stats["misses"] != "2" || c.hits.Load() != 0 {
		t.Fatalf("want hits=4 misses=2 and counters reset, got %v and %d", stats, c.hits.Load())
	}
}

func TestCampaignStatusRedisDown(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:1", MaxRetries: -1}) // nothing listens there
	defer rdb.Close()
	db := &fakeDB{statuses: map[int64]string{1: "ACTIVE"}}
	c := NewCampaigns(rdb, db, time.Minute, quiet)

	// WRK-2: a broken Redis must not break processing; MySQL answers every time.
	for i := 0; i < 2; i++ {
		if got, err := c.CampaignStatus(context.Background(), 1); err != nil || got != "ACTIVE" {
			t.Fatalf("want ACTIVE from MySQL, got %q, %v", got, err)
		}
	}
	if db.calls != 2 {
		t.Fatalf("want 2 db calls, got %d", db.calls)
	}
	if err := c.FlushStats(context.Background()); err == nil || c.misses.Load() != 2 {
		t.Fatalf("want flush error and counts kept, got %v, misses=%d", err, c.misses.Load())
	}
}

// Many goroutines failing at once must log the outage only once.
func TestRedisDownLoggedOnce(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:1", MaxRetries: -1})
	defer rdb.Close()
	var logs countingWriter
	c := NewCampaigns(rdb, &fakeDB{statuses: map[int64]string{1: "ACTIVE"}}, time.Minute,
		slog.New(slog.NewTextHandler(&logs, nil)))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.CampaignStatus(context.Background(), 1)
		}()
	}
	wg.Wait()
	if n := logs.lines.Load(); n != 1 {
		t.Fatalf("want 1 warning for the outage, got %d", n)
	}
}

// countingWriter counts log lines; slog writes one line per call.
type countingWriter struct{ lines atomic.Int64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.lines.Add(1)
	return len(p), nil
}
