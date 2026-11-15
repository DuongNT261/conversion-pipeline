package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"cloud.google.com/go/pubsub"

	"github.com/duongnt261/conversion-pipeline/internal/platform"
	"github.com/duongnt261/conversion-pipeline/internal/queue"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// rowStore, campaignReader, rateLimiter and sender are the parts of store,
// cache, ratelimit and platform the worker uses; tests pass fakes.
type rowStore interface {
	GetRow(ctx context.Context, id int64) (store.WorkRow, error)
	MarkDone(ctx context.Context, row store.WorkRow, conversionID string) (string, error)
	MarkFailed(ctx context.Context, rowID int64, reason string) error
	IncrementAttempts(ctx context.Context, rowID int64) (int, error)
}

// campaignReader is cache.Campaigns in the worker: Redis first, MySQL on a miss.
type campaignReader interface {
	CampaignStatus(ctx context.Context, id int64) (string, error)
}

type rateLimiter interface {
	Wait(ctx context.Context) error
	CountCall(ctx context.Context)
}

type sender interface {
	Send(ctx context.Context, conv platform.Conversion) platform.Response
}

// worker holds no per-message state: Pub/Sub calls handle on many goroutines at
// once, and everything here is safe to share.
type worker struct {
	db          rowStore
	campaigns   campaignReader
	limiter     rateLimiter
	api         sender
	maxAttempts int
	logger      *slog.Logger
}

const maxBackoff = 10 * time.Second

// handle is the Pub/Sub callback. A message that cannot be decoded is acked:
// a Nack would bring it back forever.
func (w *worker) handle(ctx context.Context, m *pubsub.Message) {
	var msg queue.RowMessage
	if err := json.Unmarshal(m.Data, &msg); err != nil || msg.RowID <= 0 {
		w.logger.Error("bad message, dropped", "message_id", m.ID, "data", string(m.Data), "error", err)
		m.Ack()
		return
	}
	if w.process(ctx, msg.RowID) {
		m.Ack()
	} else {
		m.Nack()
	}
}

// process handles one row and reports whether the message can be acked.
// ctx is cancelled on shutdown; DB and API calls use work, which is not, so a
// row that has started is always finished (WRK-12).
func (w *worker) process(ctx context.Context, rowID int64) bool {
	work := context.WithoutCancel(ctx)

	row, err := w.db.GetRow(work, rowID)
	if errors.Is(err, store.ErrNotFound) {
		w.logger.Warn("row not found, message dropped", "row_id", rowID)
		return true
	}
	if err != nil {
		w.logger.Warn("read row failed, will retry", "row_id", rowID, "error", err)
		return false
	}
	log := w.logger.With("job_id", row.JobID, "row_id", row.ID, "order_id", row.OrderID)
	if row.Status != store.RowPending && row.Status != store.RowPublished {
		log.Info("row already final, message acked", "status", row.Status) // layer 1 (docs/02 section 10)
		return true
	}

	status, err := w.campaigns.CampaignStatus(work, row.CampaignID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return w.fail(work, log, row.ID, "campaign_not_found")
	case err != nil:
		log.Warn("read campaign failed, will retry", "error", err)
		return false
	case status == store.CampaignPaused:
		return w.fail(work, log, row.ID, "campaign_paused")
	}

	// The wait uses ctx: on shutdown we give the message back instead of
	// waiting for a free call.
	if err := w.limiter.Wait(ctx); err != nil {
		log.Info("stopped while waiting for the rate limiter, message nacked")
		return false
	}
	w.limiter.CountCall(work)
	resp := w.api.Send(work, platform.Conversion{
		OrderID: row.OrderID, ClickID: row.ClickID, CampaignID: row.CampaignID,
		Amount: row.Amount, OrderTime: row.OrderTime,
	})
	switch resp.Outcome {
	case platform.Accepted:
		return w.done(ctx, work, log, row, resp.ConversionID)
	case platform.Rejected:
		return w.fail(work, log, row.ID, resp.Reason)
	default:
		return w.retry(ctx, work, log, row.ID, resp.Reason)
	}
}

func (w *worker) done(ctx, work context.Context, log *slog.Logger, row store.WorkRow, conversionID string) bool {
	status, err := w.db.MarkDone(work, row, conversionID)
	if err != nil {
		// The API already has the conversion; a redelivery sends the same
		// Idempotency-Key and the order_id primary key stops a second record.
		// Counting it as an attempt stops a save that can never succeed (for
		// example a conversion_id longer than platform_ref) from looping forever.
		return w.retry(ctx, work, log, row.ID, "save conversion: "+err.Error())
	}
	log.Info("row processed", "status", status)
	return true
}

func (w *worker) fail(ctx context.Context, log *slog.Logger, rowID int64, reason string) bool {
	if err := w.db.MarkFailed(ctx, rowID, reason); err != nil {
		log.Warn("mark failed failed, will retry", "error", err)
		return false
	}
	log.Info("row failed", "status", store.RowFailed, "error", reason)
	return true
}

// retry counts a temporary failure (WRK-9). Under MAX_ATTEMPTS it waits the
// backoff and Nacks, so Pub/Sub delivers the message again; at the limit the
// row becomes FAILED and the message is acked.
func (w *worker) retry(ctx, work context.Context, log *slog.Logger, rowID int64, reason string) bool {
	attempts, err := w.db.IncrementAttempts(work, rowID)
	if err != nil {
		log.Warn("count attempt failed, will retry", "error", err)
		return false
	}
	if attempts >= w.maxAttempts {
		return w.fail(work, log.With("attempt", attempts, "last_error", reason), rowID, "max_attempts")
	}

	wait := backoff(attempts)
	log.Warn("temporary error, will retry", "attempt", attempts, "error", reason, "backoff", wait.String())
	select {
	case <-time.After(wait):
	case <-ctx.Done(): // shutting down: Nack now, another worker will retry
	}
	return false
}

// backoff is min(500ms × 2^attempts, 10s): 1s, 2s, 4s, 8s, then 10s.
func backoff(attempts int) time.Duration {
	d := 500 * time.Millisecond
	for i := 0; i < attempts && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}
