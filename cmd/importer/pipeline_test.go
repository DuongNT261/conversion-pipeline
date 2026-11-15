package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// fakeInserter records every row. Only the writer goroutine calls it, so it
// needs no mutex; the test reads it after runPipeline has returned.
type fakeInserter struct {
	rows []store.Row
	err  error
}

func (f *fakeInserter) InsertRows(ctx context.Context, jobID int64, rows []store.Row) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.rows = append(f.rows, rows...) // copies the rows, since the writer reuses its batch
	return int64(len(rows)), nil
}

// bigCSV builds enough lines to fill both channel buffers.
func bigCSV(n int) string {
	var b strings.Builder
	b.WriteString("order_id,click_id,campaign_id,amount,order_time\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "A%d,clk_%d,1,1000,2026-11-13T19:20:00+07:00\n", i, i)
	}
	return b.String()
}

func TestRunPipeline(t *testing.T) {
	csvText := "order_id,click_id,campaign_id,amount,order_time\n" +
		"A1,clk_1,17,1000,2026-11-13T19:20:00+07:00\n" +
		"A2,,17,1000,2026-11-13T19:20:00+07:00\n" +
		"A3,clk_3,17,1000\n"
	fake := &fakeInserter{}

	total, inserted, err := runPipeline(context.Background(), strings.NewReader(csvText), fake, 1, 2, 2)
	if err != nil || total != 3 || inserted != 3 {
		t.Fatalf("want 3, 3, nil; got %d, %d, %v", total, inserted, err)
	}

	sort.Slice(fake.rows, func(i, j int) bool { return fake.rows[i].LineNo < fake.rows[j].LineNo })
	want := []struct {
		lineNo  int
		rawLine string
		err     string
	}{
		{2, "A1,clk_1,17,1000,2026-11-13T19:20:00+07:00", ""},
		{3, "A2,,17,1000,2026-11-13T19:20:00+07:00", "missing click_id"},
		{4, "A3,clk_3,17,1000", "want 5 columns, got 4"},
	}
	for i, w := range want {
		got := fake.rows[i]
		if got.LineNo != w.lineNo || got.RawLine != w.rawLine || got.Err != w.err {
			t.Errorf("row %d: want %+v, got line=%d raw=%q err=%q", i, w, got.LineNo, got.RawLine, got.Err)
		}
	}
	if fake.rows[0].Order.OrderID != "A1" {
		t.Errorf("valid row lost its order: %+v", fake.rows[0].Order)
	}
}

func TestRunPipelineWriteErrorStopsAllStages(t *testing.T) {
	dbErr := errors.New("db down")
	fake := &fakeInserter{err: dbErr}

	// If any stage had no way out, this call would block forever.
	_, _, err := runPipeline(context.Background(), strings.NewReader(bigCSV(10000)), fake, 1, 4, 500)
	if !errors.Is(err, dbErr) {
		t.Fatalf("want db error, got %v", err)
	}
}

func TestRunPipelineCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := runPipeline(ctx, strings.NewReader(bigCSV(10000)), &fakeInserter{}, 1, 4, 500)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRunPipelineBadCSV(t *testing.T) {
	csvText := "order_id,click_id,campaign_id,amount,order_time\n" +
		"A1,\"clk_1,17,1000,2026-11-13T19:20:00+07:00\n"

	_, _, err := runPipeline(context.Background(), strings.NewReader(csvText), &fakeInserter{}, 1, 2, 500)
	if err == nil || !strings.Contains(err.Error(), "read csv") {
		t.Fatalf("want read csv error, got %v", err)
	}
}
