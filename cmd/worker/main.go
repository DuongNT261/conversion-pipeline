// Command worker sends published rows to the ad platform (docs/01 section 6.3).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/duongnt261/conversion-pipeline/internal/config"
	"github.com/duongnt261/conversion-pipeline/internal/platform"
	"github.com/duongnt261/conversion-pipeline/internal/queue"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// startupTimeout bounds the connection checks at startup (docs/03 section 8).
const startupTimeout = 30 * time.Second

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

	w := &worker{
		db:          store.New(db),
		api:         platform.New(cfg.PlatformURL),
		maxAttempts: cfg.MaxAttempts,
		logger:      logger,
	}
	sub := client.Subscription(cfg.PubSubSubscription)
	sub.ReceiveSettings.MaxOutstandingMessages = cfg.WorkerMaxOutstanding // WRK-10

	logger.Info("worker started", "subscription", cfg.PubSubSubscription, "max_outstanding", cfg.WorkerMaxOutstanding)
	// Receive calls w.handle on many goroutines and blocks until ctx is
	// cancelled and every handler in flight has returned (WRK-12).
	if err := sub.Receive(ctx, w.handle); err != nil {
		return fmt.Errorf("receive: %w", err)
	}
	logger.Info("worker stopped")
	return nil
}
