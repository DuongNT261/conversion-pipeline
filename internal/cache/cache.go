// Package cache keeps campaign statuses in Redis with the cache-aside pattern
// (WRK-2) and counts hits and misses (WRK-11).
package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// notFound is cached for campaigns that do not exist, so every row of
// campaign 215 does not hit MySQL again (cache penetration, docs/02 section 12).
const notFound = "NOT_FOUND"

// StatsKey is the Redis hash the report reads hits and misses from.
const StatsKey = "stats:worker"

// campaignReader is the store method the cache falls back to.
type campaignReader interface {
	CampaignStatus(ctx context.Context, id int64) (string, error)
}

// Campaigns is shared by every handler goroutine of a worker. hits and misses
// are atomic because many goroutines add to them at the same time.
type Campaigns struct {
	rdb    *redis.Client
	db     campaignReader
	ttl    time.Duration
	logger *slog.Logger

	hits   atomic.Int64
	misses atomic.Int64
}

func NewCampaigns(rdb *redis.Client, db campaignReader, ttl time.Duration, logger *slog.Logger) *Campaigns {
	return &Campaigns{rdb: rdb, db: db, ttl: ttl, logger: logger}
}

// CampaignStatus has the same signature as the store method, so the worker
// does not care which one it calls. A Redis error is logged and MySQL answers:
// the cache makes the worker faster, never less correct.
func (c *Campaigns) CampaignStatus(ctx context.Context, id int64) (string, error) {
	key := fmt.Sprintf("campaign:%d", id)
	value, err := c.rdb.Get(ctx, key).Result()
	switch {
	case err == nil:
		c.hits.Add(1)
		if value == notFound {
			return "", store.ErrNotFound
		}
		return value, nil
	case !errors.Is(err, redis.Nil): // redis.Nil only means "no such key"
		c.logger.Warn("redis get failed, reading MySQL", "key", key, "error", err)
	}

	c.misses.Add(1)
	status, err := c.db.CampaignStatus(ctx, id)
	toCache := status
	if errors.Is(err, store.ErrNotFound) {
		toCache = notFound
	} else if err != nil {
		return "", err
	}
	if err := c.rdb.Set(ctx, key, toCache, c.jitteredTTL()).Err(); err != nil {
		c.logger.Warn("redis set failed", "key", key, "error", err)
	}
	return status, err
}

// jitteredTTL spreads expiry over ttl ±10%, so keys cached in the same second
// do not all expire together and send every worker to MySQL at once.
func (c *Campaigns) jitteredTTL() time.Duration {
	spread := int64(c.ttl) / 5
	return c.ttl - time.Duration(spread/2) + time.Duration(rand.Int64N(spread+1))
}

// FlushStats adds the counts since the last flush to the Redis hash (WRK-11).
// Swap reads and resets each counter in one atomic step, so no increment made
// by another goroutine in between is lost. On error the counts are put back.
func (c *Campaigns) FlushStats(ctx context.Context) error {
	hits := c.hits.Swap(0)
	misses := c.misses.Swap(0)
	if hits == 0 && misses == 0 {
		return nil
	}

	pipe := c.rdb.TxPipeline()
	pipe.HIncrBy(ctx, StatsKey, "hits", hits)
	pipe.HIncrBy(ctx, StatsKey, "misses", misses)
	if _, err := pipe.Exec(ctx); err != nil {
		c.hits.Add(hits)
		c.misses.Add(misses)
		return fmt.Errorf("flush cache stats: %w", err)
	}
	return nil
}
