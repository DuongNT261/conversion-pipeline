// Command relay publishes PENDING rows to Pub/Sub (outbox pattern, docs/01 section 6.2).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"

	"github.com/duongnt261/conversion-pipeline/internal/config"
	"github.com/duongnt261/conversion-pipeline/internal/queue"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// finishInterval is fixed by REL-5.
const finishInterval = 5 * time.Second

// startupTimeout bounds the connection checks at startup (docs/03 section 8:
// a wrong setting must stop the program with a clear message).
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
		logger.Error("relay failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	// Without a deadline, a wrong MySQL or Pub/Sub address makes the client retry
	// forever and the relay hangs at startup with no log at all.
	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	db, err := store.Open(ctx, cfg.MySQLDSN, cfg.MySQLMaxOpenConns)
	if err != nil {
		return err
	}
	defer db.Close()
	st := store.New(db)

	client, err := queue.NewClient(ctx, cfg.PubSubEmulatorHost, cfg.PubSubProjectID)
	if err != nil {
		return err
	}
	defer client.Close()
	topic, err := queue.EnsureTopic(startCtx, client, cfg.PubSubTopic, cfg.PubSubSubscription)
	if err != nil {
		return err
	}
	defer topic.Stop() // sends anything still buffered before exit

	logger.Info("relay started", "topic", cfg.PubSubTopic, "batch_size", cfg.RelayBatchSize, "interval", cfg.RelayInterval.String())
	loop(ctx, logger, st, topic, cfg)
	logger.Info("relay stopped")
	return nil
}

// loop runs until ctx is cancelled. Each round uses workCtx, which a stop
// signal does not cancel, so a transaction that has started always finishes
// (REL-7); the signal is only checked between rounds.
func loop(ctx context.Context, logger *slog.Logger, st *store.Store, topic *pubsub.Topic, cfg config.Config) {
	workCtx := context.WithoutCancel(ctx)
	publish := func(ctx context.Context, rows []store.PendingRow) error {
		return queue.PublishRows(ctx, topic, rows)
	}

	publishTicker := time.NewTicker(cfg.RelayInterval)
	defer publishTicker.Stop()
	finishTicker := time.NewTicker(finishInterval)
	defer finishTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-publishTicker.C:
			n, err := st.PublishPending(workCtx, cfg.RelayBatchSize, publish)
			if err != nil {
				logger.Warn("publish round failed, rows stay PENDING and are retried", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("rows published", "rows", n)
			}
		case <-finishTicker.C:
			n, err := st.FinishJobs(workCtx)
			if err != nil {
				logger.Warn("finish jobs failed, retried next round", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("jobs finished", "jobs", n, "status", store.JobDone)
			}
		}
	}
}
