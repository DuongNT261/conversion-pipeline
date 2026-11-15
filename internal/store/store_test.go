package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/duongnt261/conversion-pipeline/internal/csvparse"
)

// openTestDB connects to the MySQL started by compose. Without MYSQL_DSN the
// test is skipped, so `go test ./...` still works with no infrastructure.
func openTestDB(t *testing.T) *sql.DB {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		t.Skip("MYSQL_DSN not set, skipping MySQL integration test")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInsertRowsEmpty(t *testing.T) {
	inserted, err := New(nil).InsertRows(context.Background(), 1, nil)
	if err != nil || inserted != 0 {
		t.Fatalf("want 0, nil; got %d, %v", inserted, err)
	}
}

func TestInsertRows(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()

	// A job id no real job uses, so the test never touches other data.
	jobID := time.Now().UnixNano()
	t.Cleanup(func() { db.Exec("DELETE FROM import_rows WHERE job_id = ?", jobID) })

	rows := []Row{
		{
			LineNo:  2,
			RawLine: "A1,clk_1,17,1000,2026-11-13T19:20:00+07:00",
			Order: csvparse.Order{
				OrderID:    "A1",
				ClickID:    "clk_1",
				CampaignID: 17,
				Amount:     1000,
				OrderTime:  time.Date(2026, 11, 13, 12, 20, 0, 0, time.UTC),
			},
		},
		{LineNo: 3, RawLine: "A2,,17,1000,2026-11-13T19:20:00+07:00", Err: "missing click_id"},
	}

	inserted, err := s.InsertRows(ctx, jobID, rows)
	if err != nil || inserted != 2 {
		t.Fatalf("first insert: want 2, nil; got %d, %v", inserted, err)
	}

	// Same batch again, as after an importer crash: nothing new is inserted (IMP-6).
	inserted, err = s.InsertRows(ctx, jobID, rows)
	if err != nil || inserted != 0 {
		t.Fatalf("second insert: want 0, nil; got %d, %v", inserted, err)
	}

	var status string
	var orderID, errText sql.NullString
	var orderTime sql.NullTime
	query := "SELECT status, order_id, error, order_time FROM import_rows WHERE job_id = ? AND line_no = ?"

	if err := db.QueryRowContext(ctx, query, jobID, 2).Scan(&status, &orderID, &errText, &orderTime); err != nil {
		t.Fatalf("select line 2: %v", err)
	}
	if status != "PENDING" || orderID.String != "A1" || errText.Valid || !orderTime.Time.Equal(rows[0].Order.OrderTime) {
		t.Fatalf("line 2: got status=%s order_id=%v error=%v order_time=%v", status, orderID, errText, orderTime)
	}

	if err := db.QueryRowContext(ctx, query, jobID, 3).Scan(&status, &orderID, &errText, &orderTime); err != nil {
		t.Fatalf("select line 3: %v", err)
	}
	if status != "INVALID" || orderID.Valid || errText.String != "missing click_id" || orderTime.Valid {
		t.Fatalf("line 3: got status=%s order_id=%v error=%v order_time=%v", status, orderID, errText, orderTime)
	}
}

func TestFindOrCreateJob(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()

	// A sha256 no real file has, so the test never touches other jobs.
	sha := fmt.Sprintf("%064d", time.Now().UnixNano())
	t.Cleanup(func() { db.Exec("DELETE FROM import_jobs WHERE file_sha256 = ?", sha) })

	first, err := s.FindOrCreateJob(ctx, "orders.csv", sha)
	if err != nil || first.Status != JobImporting {
		t.Fatalf("first call: want IMPORTING, nil; got %+v, %v", first, err)
	}

	// Same file again, as after an importer crash: reuse the job (IMP-2).
	again, err := s.FindOrCreateJob(ctx, "orders.csv", sha)
	if err != nil || again != first {
		t.Fatalf("second call: want %+v, got %+v, %v", first, again, err)
	}

	// A finished job is returned as is, so the importer can exit (IMP-1).
	if _, err := db.Exec("UPDATE import_jobs SET status = ? WHERE id = ?", JobDone, first.ID); err != nil {
		t.Fatalf("mark job done: %v", err)
	}
	done, err := s.FindOrCreateJob(ctx, "orders.csv", sha)
	if err != nil || done.ID != first.ID || done.Status != JobDone {
		t.Fatalf("done job: want id=%d DONE, got %+v, %v", first.ID, done, err)
	}
}

func TestMarkJobImported(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()

	sha := fmt.Sprintf("%064d", time.Now().UnixNano())
	t.Cleanup(func() { db.Exec("DELETE FROM import_jobs WHERE file_sha256 = ?", sha) })

	job, err := s.FindOrCreateJob(ctx, "orders.csv", sha)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	var status string
	var total int
	query := "SELECT status, total_rows FROM import_jobs WHERE id = ?"

	if err := s.MarkJobImported(ctx, job.ID, 20000); err != nil {
		t.Fatalf("mark imported: %v", err)
	}
	if err := db.QueryRowContext(ctx, query, job.ID).Scan(&status, &total); err != nil {
		t.Fatalf("select job: %v", err)
	}
	if status != JobProcessing || total != 20000 {
		t.Fatalf("after mark: want PROCESSING 20000, got %s %d", status, total)
	}

	// A DONE job must never go back to PROCESSING.
	if _, err := db.Exec("UPDATE import_jobs SET status = ? WHERE id = ?", JobDone, job.ID); err != nil {
		t.Fatalf("mark job done: %v", err)
	}
	if err := s.MarkJobImported(ctx, job.ID, 1); err != nil {
		t.Fatalf("mark imported on done job: %v", err)
	}
	if err := db.QueryRowContext(ctx, query, job.ID).Scan(&status, &total); err != nil {
		t.Fatalf("select job: %v", err)
	}
	if status != JobDone || total != 20000 {
		t.Fatalf("done job: want DONE 20000, got %s %d", status, total)
	}
}

// requireNoPendingRows skips tests that work on every PENDING row of the
// database, so they never touch real data. Run them against pipeline_test.
func requireNoPendingRows(t *testing.T, db *sql.DB) {
	var pending int
	if err := db.QueryRow("SELECT COUNT(*) FROM import_rows WHERE status IN ('PENDING', 'PUBLISHED')").Scan(&pending); err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	if pending > 0 {
		t.Skipf("database has %d PENDING/PUBLISHED rows; run against pipeline_test", pending)
	}
}

func insertPendingRows(t *testing.T, s *Store, jobID int64, n int) {
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{
			LineNo:  i + 2,
			RawLine: "x",
			Order: csvparse.Order{
				OrderID: fmt.Sprintf("P%d", i), ClickID: "clk", CampaignID: 1, Amount: 1000,
				OrderTime: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
			},
		}
	}
	if _, err := s.InsertRows(context.Background(), jobID, rows); err != nil {
		t.Fatalf("insert rows: %v", err)
	}
}

func countStatus(t *testing.T, db *sql.DB, jobID int64, status string) int {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM import_rows WHERE job_id = ? AND status = ?", jobID, status).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", status, err)
	}
	return n
}

func TestPublishPending(t *testing.T) {
	db := openTestDB(t)
	requireNoPendingRows(t, db)
	s := New(db)
	ctx := context.Background()
	jobID := time.Now().UnixNano()
	t.Cleanup(func() { db.Exec("DELETE FROM import_rows WHERE job_id = ?", jobID) })
	insertPendingRows(t, s, jobID, 3)

	// Publish fails: the transaction rolls back and nothing changes (REL-3).
	failing := func(context.Context, []PendingRow) error { return errors.New("pubsub down") }
	if _, err := s.PublishPending(ctx, 10, failing); err == nil {
		t.Fatal("want publish error, got nil")
	}
	if got := countStatus(t, db, jobID, "PENDING"); got != 3 {
		t.Fatalf("after failed publish: want 3 PENDING, got %d", got)
	}

	var published []PendingRow
	collect := func(_ context.Context, rows []PendingRow) error {
		published = append(published, rows...)
		return nil
	}
	for _, want := range []int{2, 1, 0} {
		n, err := s.PublishPending(ctx, 2, collect)
		if err != nil || n != want {
			t.Fatalf("want %d, nil; got %d, %v", want, n, err)
		}
	}
	if len(published) != 3 || countStatus(t, db, jobID, "PUBLISHED") != 3 {
		t.Fatalf("want 3 rows published once each, got %+v", published)
	}
	for _, p := range published {
		if p.JobID != jobID {
			t.Fatalf("wrong job id in %+v", p)
		}
	}
}

// While one relay holds its lock, a second relay must take the other rows
// right away instead of waiting or publishing the same rows (REL-4).
func TestPublishPendingSkipLocked(t *testing.T) {
	db := openTestDB(t)
	requireNoPendingRows(t, db)
	s := New(db)
	ctx := context.Background()
	jobID := time.Now().UnixNano()
	t.Cleanup(func() { db.Exec("DELETE FROM import_rows WHERE job_id = ?", jobID) })
	insertPendingRows(t, s, jobID, 4)

	locked := make(chan []PendingRow)
	release := make(chan struct{})
	firstDone := make(chan error)
	go func() {
		_, err := s.PublishPending(ctx, 2, func(_ context.Context, rows []PendingRow) error {
			locked <- rows
			<-release
			return nil
		})
		firstDone <- err
	}()
	first := <-locked

	var second []PendingRow
	n, err := s.PublishPending(ctx, 10, func(_ context.Context, rows []PendingRow) error {
		second = rows
		return nil
	})
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first relay: %v", err)
	}
	if err != nil || n != 2 {
		t.Fatalf("second relay: want 2 rows, got %d, %v", n, err)
	}
	for _, a := range first {
		for _, b := range second {
			if a.ID == b.ID {
				t.Fatalf("row %d taken by both relays", a.ID)
			}
		}
	}
}

func TestFinishJobs(t *testing.T) {
	db := openTestDB(t)
	requireNoPendingRows(t, db)
	s := New(db)
	ctx := context.Background()

	sha := fmt.Sprintf("%064d", time.Now().UnixNano())
	job, err := s.FindOrCreateJob(ctx, "orders.csv", sha)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM import_rows WHERE job_id = ?", job.ID)
		db.Exec("DELETE FROM import_jobs WHERE id = ?", job.ID)
	})
	insertPendingRows(t, s, job.ID, 2)
	if err := s.MarkJobImported(ctx, job.ID, 2); err != nil {
		t.Fatalf("mark imported: %v", err)
	}

	// One row still PUBLISHED: the job is not finished yet.
	if _, err := db.Exec("UPDATE import_rows SET status = 'PUBLISHED' WHERE job_id = ?", job.ID); err != nil {
		t.Fatalf("set published: %v", err)
	}
	if _, err := db.Exec("UPDATE import_rows SET status = 'DONE' WHERE job_id = ? AND line_no = 2", job.ID); err != nil {
		t.Fatalf("set one done: %v", err)
	}
	if n, err := s.FinishJobs(ctx); err != nil || n != 0 {
		t.Fatalf("with a PUBLISHED row: want 0 finished, got %d, %v", n, err)
	}

	if _, err := db.Exec("UPDATE import_rows SET status = 'FAILED' WHERE job_id = ? AND line_no = 3", job.ID); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if n, err := s.FinishJobs(ctx); err != nil || n != 1 {
		t.Fatalf("all rows final: want 1 finished, got %d, %v", n, err)
	}
	var status string
	var finishedAt sql.NullTime
	if err := db.QueryRow("SELECT status, finished_at FROM import_jobs WHERE id = ?", job.ID).Scan(&status, &finishedAt); err != nil {
		t.Fatalf("select job: %v", err)
	}
	if status != JobDone || !finishedAt.Valid || time.Since(finishedAt.Time) > time.Minute {
		t.Fatalf("want DONE with a recent finished_at, got %s %v", status, finishedAt)
	}
}

func rowIDs(t *testing.T, db *sql.DB, jobID int64) []int64 {
	rows, err := db.Query("SELECT id FROM import_rows WHERE job_id = ? ORDER BY line_no", jobID)
	if err != nil {
		t.Fatalf("select ids: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

func TestWorkerRowLifecycle(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	jobID := time.Now().UnixNano()
	orderID := fmt.Sprintf("W%d", jobID)
	t.Cleanup(func() {
		db.Exec("DELETE FROM conversions WHERE order_id = ?", orderID)
		db.Exec("DELETE FROM import_rows WHERE job_id = ?", jobID)
	})

	// Two lines with the same order_id, as the gen duplicate scenario, plus one more.
	order := csvparse.Order{OrderID: orderID, ClickID: "clk_1", CampaignID: 17, Amount: 1000,
		OrderTime: time.Date(2026, 11, 13, 12, 20, 0, 0, time.UTC)}
	rows := []Row{{LineNo: 2, RawLine: "a", Order: order}, {LineNo: 3, RawLine: "b", Order: order},
		{LineNo: 4, RawLine: "c", Order: order}}
	if _, err := s.InsertRows(ctx, jobID, rows); err != nil {
		t.Fatalf("insert rows: %v", err)
	}
	ids := rowIDs(t, db, jobID)

	first, err := s.GetRow(ctx, ids[0])
	if err != nil || first.OrderID != orderID || first.Status != RowPending || !first.OrderTime.Equal(order.OrderTime) {
		t.Fatalf("get row: got %+v, %v", first, err)
	}
	if _, err := s.GetRow(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing row: want ErrNotFound, got %v", err)
	}

	if status, err := s.MarkDone(ctx, first, "cv_1"); err != nil || status != RowDone {
		t.Fatalf("first MarkDone: want DONE, got %q, %v", status, err)
	}
	second, _ := s.GetRow(ctx, ids[1])
	if status, err := s.MarkDone(ctx, second, "cv_1"); err != nil || status != RowSkipped {
		t.Fatalf("duplicate order_id: want SKIPPED, got %q, %v", status, err)
	}
	// The same message handled again must not change a final row.
	if _, err := s.MarkDone(ctx, second, "cv_1"); err != nil {
		t.Fatalf("MarkDone again: %v", err)
	}
	if err := s.MarkFailed(ctx, ids[0], "click_expired"); err != nil {
		t.Fatalf("MarkFailed on done row: %v", err)
	}

	for _, n := range []int{1, 2} {
		if got, err := s.IncrementAttempts(ctx, ids[2]); err != nil || got != n {
			t.Fatalf("attempts: want %d, got %d, %v", n, got, err)
		}
	}
	if err := s.MarkFailed(ctx, ids[2], "max_attempts"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	want := []struct{ status, err string }{{RowDone, ""}, {RowSkipped, "duplicate order_id"}, {RowFailed, "max_attempts"}}
	for i, w := range want {
		var status string
		var errText sql.NullString
		db.QueryRow("SELECT status, error FROM import_rows WHERE id = ?", ids[i]).Scan(&status, &errText)
		if status != w.status || errText.String != w.err {
			t.Errorf("row %d: want %s %q, got %s %q", i, w.status, w.err, status, errText.String)
		}
	}
	var conversions int
	db.QueryRow("SELECT COUNT(*) FROM conversions WHERE order_id = ?", orderID).Scan(&conversions)
	if conversions != 1 {
		t.Fatalf("want exactly 1 conversion for the order, got %d", conversions)
	}
}

func TestCampaignStatus(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	ctx := context.Background()
	if got, err := s.CampaignStatus(ctx, 1); err != nil || got != "ACTIVE" {
		t.Fatalf("campaign 1: want ACTIVE, got %q, %v", got, err)
	}
	if got, err := s.CampaignStatus(ctx, 10); err != nil || got != CampaignPaused {
		t.Fatalf("campaign 10: want PAUSED, got %q, %v", got, err)
	}
	if _, err := s.CampaignStatus(ctx, 215); !errors.Is(err, ErrNotFound) {
		t.Fatalf("campaign 215: want ErrNotFound, got %v", err)
	}
}
