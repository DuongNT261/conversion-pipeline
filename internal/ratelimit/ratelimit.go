// Package ratelimit shares one API rate limit between every worker through
// Redis (WRK-4) and counts the real calls per second (WRK-5).
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
)

// limitKey becomes rate:adplatform: redis_rate adds the "rate:" prefix itself.
const limitKey = "adplatform"

// callCounterTTL keeps the per-second counters long enough for the report.
const callCounterTTL = time.Hour

type Limiter struct {
	rdb     *redis.Client
	limiter *redis_rate.Limiter
	rps     int
	enabled bool
	logger  *slog.Logger

	// redisDown makes an outage log one warning instead of one per call.
	redisDown atomic.Bool
}

func New(rdb *redis.Client, rps int, enabled bool, logger *slog.Logger) *Limiter {
	return &Limiter{rdb: rdb, limiter: redis_rate.NewLimiter(rdb), rps: rps, enabled: enabled, logger: logger}
}

// Wait blocks until every worker together is under rps calls per second.
// It returns ctx.Err() when the worker shuts down while waiting.
//
// If Redis fails, Wait lets the call through and logs a warning: an outage of
// the limiter should slow nothing down, and the API's own 429s are still
// handled as temporary errors.
func (l *Limiter) Wait(ctx context.Context) error {
	if !l.enabled {
		return nil
	}
	for {
		res, err := l.limiter.Allow(ctx, limitKey, l.limit())
		if err != nil {
			if l.redisDown.CompareAndSwap(false, true) {
				l.logger.Warn("rate limiter unavailable, calling without it", "error", err)
			}
			return nil
		}
		if l.redisDown.CompareAndSwap(true, false) {
			l.logger.Info("rate limiter available again")
		}
		if res.Allowed > 0 {
			return nil
		}
		select {
		case <-time.After(res.RetryAfter):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// limit is rps per second with a small burst. redis_rate.PerSecond sets the
// burst to rps, which lets up to 2×rps calls through in one clock second (the
// saved-up burst plus the normal rate). A 5% burst keeps every second within
// about rps + 5%, inside the 10% allowed by AC-6.
func (l *Limiter) limit() redis_rate.Limit {
	return redis_rate.Limit{Rate: l.rps, Period: time.Second, Burst: max(1, l.rps/20)}
}

// CountCall adds one to calls:{unix_second} (WRK-5). INCR is atomic in Redis,
// so calls from all workers add up without losing any.
func (l *Limiter) CountCall(ctx context.Context) {
	key := fmt.Sprintf("calls:%d", time.Now().Unix())
	pipe := l.rdb.TxPipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, callCounterTTL)
	// Its errors are not logged: an outage is already reported by Wait, and
	// this counter only measures.
	pipe.Exec(ctx)
}
