package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/duongnt261/conversion-pipeline/internal/platform"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// fakeStore keeps one row in memory and records what the worker wrote.
type fakeStore struct {
	row      store.WorkRow
	rowErr   error
	campaign string
	campErr  error
	attempts int
	doneErr  error

	doneWith string // conversion id given to MarkDone
	failed   string // reason given to MarkFailed
}

func (f *fakeStore) GetRow(context.Context, int64) (store.WorkRow, error) { return f.row, f.rowErr }
func (f *fakeStore) CampaignStatus(context.Context, int64) (string, error) {
	return f.campaign, f.campErr
}
func (f *fakeStore) MarkDone(_ context.Context, _ store.WorkRow, id string) (string, error) {
	if f.doneErr != nil {
		return "", f.doneErr
	}
	f.doneWith = id
	return store.RowDone, nil
}
func (f *fakeStore) MarkFailed(_ context.Context, _ int64, reason string) error {
	f.failed = reason
	return nil
}
func (f *fakeStore) IncrementAttempts(context.Context, int64) (int, error) {
	f.attempts++
	return f.attempts, nil
}

type fakeSender struct {
	resp  platform.Response
	calls int
}

func (f *fakeSender) Send(context.Context, platform.Conversion) platform.Response {
	f.calls++
	return f.resp
}

func TestProcess(t *testing.T) {
	pending := store.WorkRow{ID: 1, JobID: 1, OrderID: "A1", CampaignID: 17, Status: store.RowPending}
	accepted := platform.Response{Outcome: platform.Accepted, ConversionID: "cv_A1"}
	tests := []struct {
		name      string
		db        fakeStore
		resp      platform.Response
		wantAck   bool
		wantCalls int
		wantDone  string
		wantFail  string
	}{
		{"accepted", fakeStore{row: pending, campaign: "ACTIVE"}, accepted, true, 1, "cv_A1", ""},
		{"published row is processed too", fakeStore{row: store.WorkRow{ID: 1, Status: store.RowPublished}, campaign: "ACTIVE"}, accepted, true, 1, "cv_A1", ""},
		{"already done", fakeStore{row: store.WorkRow{ID: 1, Status: store.RowDone}}, accepted, true, 0, "", ""},
		{"row missing", fakeStore{rowErr: store.ErrNotFound}, accepted, true, 0, "", ""},
		{"db down", fakeStore{rowErr: errors.New("db down")}, accepted, false, 0, "", ""},
		{"campaign missing", fakeStore{row: pending, campErr: store.ErrNotFound}, accepted, true, 0, "", "campaign_not_found"},
		{"campaign paused", fakeStore{row: pending, campaign: store.CampaignPaused}, accepted, true, 0, "", "campaign_paused"},
		{"rejected", fakeStore{row: pending, campaign: "ACTIVE"},
			platform.Response{Outcome: platform.Rejected, Reason: "click_expired"}, true, 1, "", "click_expired"},
		{"retryable, attempts left", fakeStore{row: pending, campaign: "ACTIVE"},
			platform.Response{Outcome: platform.Retryable, Reason: "http 500"}, false, 1, "", ""},
		{"save fails, attempts left", fakeStore{row: pending, campaign: "ACTIVE", doneErr: errors.New("data too long")},
			accepted, false, 1, "", ""},
		{"save keeps failing, last attempt", fakeStore{row: pending, campaign: "ACTIVE", doneErr: errors.New("data too long"), attempts: 4},
			accepted, true, 1, "", "max_attempts"},
		{"retryable, last attempt", fakeStore{row: pending, campaign: "ACTIVE", attempts: 4},
			platform.Response{Outcome: platform.Retryable, Reason: "http 500"}, true, 1, "", "max_attempts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := tt.db
			api := &fakeSender{resp: tt.resp}
			w := &worker{db: &db, api: api, maxAttempts: 5, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

			// An already cancelled ctx skips the backoff wait, as on shutdown.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			ack := w.process(ctx, 1)

			if ack != tt.wantAck || api.calls != tt.wantCalls || db.doneWith != tt.wantDone || db.failed != tt.wantFail {
				t.Fatalf("want ack=%v calls=%d done=%q failed=%q; got ack=%v calls=%d done=%q failed=%q",
					tt.wantAck, tt.wantCalls, tt.wantDone, tt.wantFail, ack, api.calls, db.doneWith, db.failed)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}, {4, 8 * time.Second},
		{5, 10 * time.Second}, {50, 10 * time.Second},
	}
	for _, tt := range tests {
		if got := backoff(tt.attempts); got != tt.want {
			t.Errorf("backoff(%d): want %v, got %v", tt.attempts, tt.want, got)
		}
	}
}
