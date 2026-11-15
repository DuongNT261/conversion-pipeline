package store

import (
	"context"
	"database/sql"
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
