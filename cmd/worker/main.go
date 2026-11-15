// Command worker sends published rows to the ad platform (docs/01 section 6.3).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"github.com/duongnt261/conversion-pipeline/internal/cache"
	"github.com/duongnt261/conversion-pipeline/internal/config"
	"github.com/duongnt261/conversion-pipeline/internal/platform"
	"github.com/duongnt261/conversion-pipeline/internal/queue"
	"github.com/duongnt261/conversion-pipeline/internal/ratelimit"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// startupTimeout bounds the connection checks at startup (docs/03 section 8).
const startupTimeout = 30 * time.Second

// statsInterval is fixed by WRK-11.
const statsInterval = 5 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, logger, cfg)
	stop()
	if err != nil {
		logger.Error("worker failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.MySQLMaxOpenConns)
	db.SetMaxIdleConns(cfg.MySQLMaxOpenConns)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(startCtx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	client, err := queue.NewClient(ctx, cfg.PubSubEmulatorHost, cfg.PubSubProjectID)
	if err != nil {
		return err
	}
	defer client.Close()
	// Creating them here too means the worker does not depend on the relay
	// having started first.
	if _, err := queue.EnsureTopic(startCtx, client, cfg.PubSubTopic, cfg.PubSubSubscription); err != nil {
		return err
	}

	// No retries inside go-redis: the cache and the limiter already fall back
	// (MySQL, or calling without a limit), so retrying only slows every row
	// down while Redis is out (about 400 ms per row measured with 3 retries).
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, MaxRetries: -1})
	defer rdb.Close()
	// Redis down is not fatal (WRK-2): the cache falls back to MySQL and the
	// limiter lets calls through, so only warn.
	if err := rdb.Ping(startCtx).Err(); err != nil {
		logger.Warn("redis unavailable at startup, running without cache and rate limit", "addr", cfg.RedisAddr, "error", err)
	}

	st := store.New(db)
	campaigns := cache.NewCampaigns(rdb, st, cfg.CacheTTL, logger)
	w := &worker{
		db:          st,
		campaigns:   campaigns,
		limiter:     ratelimit.New(rdb, cfg.RateLimitRPS, cfg.RateLimitEnabled, logger),
		api:         platform.New(cfg.PlatformURL),
		maxAttempts: cfg.MaxAttempts,
		logger:      logger,
	}

	// The flusher gets its own ctx: if Receive fails early, ctx is never
	// cancelled, and the flusher would block flusher.Wait() forever.
	flushCtx, stopFlush := context.WithCancel(ctx)
	defer stopFlush()
	var flusher sync.WaitGroup
	flusher.Add(1)
	go func() {
		defer flusher.Done()
		flushStats(flushCtx, logger, campaigns)
	}()
	sub := client.Subscription(cfg.PubSubSubscription)
	sub.ReceiveSettings.MaxOutstandingMessages = cfg.WorkerMaxOutstanding // WRK-10

	logger.Info("worker started", "subscription", cfg.PubSubSubscription, "max_outstanding", cfg.WorkerMaxOutstanding,
		"rate_limit_enabled", cfg.RateLimitEnabled, "rate_limit_rps", cfg.RateLimitRPS)
	// Receive calls w.handle on many goroutines and blocks until ctx is
	// cancelled and every handler in flight has returned (WRK-12).
	err = sub.Receive(ctx, w.handle)

	// Last flush after every handler is done, so no hit or miss is lost (WRK-11).
	stopFlush()
	flusher.Wait()
	if flushErr := campaigns.FlushStats(context.WithoutCancel(ctx)); flushErr != nil {
		logger.Warn("final cache stats flush failed", "error", flushErr)
	}
	if err != nil {
		return fmt.Errorf("receive: %w", err)
	}
	logger.Info("worker stopped")
	return nil
}

// flushStats adds the cache counters to Redis every statsInterval until ctx is cancelled.
func flushStats(ctx context.Context, logger *slog.Logger, campaigns *cache.Campaigns) {
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := campaigns.FlushStats(ctx); err != nil {
				logger.Warn("cache stats flush failed, kept for the next round", "error", err)
			}
		}
	}
}
