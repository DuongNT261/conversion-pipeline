package ratelimit

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// testRedis uses DB 15, so tests never touch the worker's keys in DB 0.
func testRedis(t *testing.T) *redis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set, skipping Redis integration test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	rdb.Del(context.Background(), "rate:"+limitKey)
	return rdb
}

// Two limiters share one Redis key, like two workers: together they get rps
// calls per second, not rps each.
func TestWaitSharedAcrossLimiters(t *testing.T) {
	rdb := testRedis(t)
	a := New(rdb, 10, true, quiet)
	b := New(rdb, 10, true, quiet)

	start := time.Now()
	var wg sync.WaitGroup
	for _, l := range []*Limiter{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				if err := l.Wait(context.Background()); err != nil {
					t.Errorf("wait: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	// 30 calls at 10/s in total need about 2s; without sharing it would be about 0.5s.
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond {
		t.Fatalf("30 calls at 10/s finished in %v, limit not shared", elapsed)
	}
}

// No clock second may get much more than rps calls, even at startup when the
// limiter has a full burst saved up (AC-6 allows 10%).
func TestWaitPeakPerSecond(t *testing.T) {
	rdb := testRedis(t)
	l := New(rdb, 100, true, quiet)

	start := time.Now()
	calls := 0
	for time.Since(start) < time.Second {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("wait: %v", err)
		}
		calls++
	}
	if calls > 110 {
		t.Fatalf("%d calls in the first second at 100/s, want at most 110", calls)
	}
}

func TestWaitDisabledAndCanceled(t *testing.T) {
	rdb := testRedis(t)
	if err := New(rdb, 1, false, quiet).Wait(context.Background()); err != nil {
		t.Fatalf("disabled limiter: %v", err)
	}

	l := New(rdb, 1, true, quiet)
	l.Wait(context.Background()) // uses the only call of this second
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatal("want ctx error while waiting, got nil")
	}
}

func TestCountCall(t *testing.T) {
	rdb := testRedis(t)
	l := New(rdb, 10, true, quiet)
	key := fmt.Sprintf("calls:%d", time.Now().Unix())
	rdb.Del(context.Background(), key)

	l.CountCall(context.Background())
	l.CountCall(context.Background())

	got, _ := strconv.Atoi(rdb.Get(context.Background(), key).Val())
	ttl := rdb.TTL(context.Background(), key).Val()
	// The second may tick between the two calls, so accept 1 or 2.
	if got < 1 || got > 2 || ttl <= 0 || ttl > time.Hour {
		t.Fatalf("want count 1-2 with ttl <= 1h, got %d, %v", got, ttl)
	}
	rdb.Del(context.Background(), key)
}
