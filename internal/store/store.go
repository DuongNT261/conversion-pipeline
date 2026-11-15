// Package store holds every SQL statement of the project (docs/03 section 5).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/duongnt261/conversion-pipeline/internal/csvparse"
)

// Job statuses (docs/01 section 5.3).
const (
	JobImporting  = "IMPORTING"
	JobProcessing = "PROCESSING"
	JobDone       = "DONE"
)

// Row statuses (docs/01 section 5.2). DONE, SKIPPED, FAILED and INVALID are final.
const (
	RowPending   = "PENDING"
	RowPublished = "PUBLISHED"
	RowDone      = "DONE"
	RowSkipped   = "SKIPPED"
	RowFailed    = "FAILED"
)

const CampaignPaused = "PAUSED"

// ErrNotFound hides sql.ErrNoRows from the callers (docs/03 section 2).
var ErrNotFound = errors.New("not found")

type Job struct {
	ID     int64
	Status string
}

// Store wraps the single *sql.DB of the process.
type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// FindOrCreateJob returns the job for this file content (IMP-1, IMP-2).
// It inserts first and lets uq_file decide: when two importers race on the same
// file, the loser gets error 1062 and reads the job the winner created.
func (s *Store) FindOrCreateJob(ctx context.Context, fileName, sha256 string) (Job, error) {
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO import_jobs (file_name, file_sha256, status) VALUES (?, ?, ?)",
		fileName, sha256, JobImporting)
	if isDuplicate(err) {
		var job Job
		err := s.db.QueryRowContext(ctx,
			"SELECT id, status FROM import_jobs WHERE file_sha256 = ?", sha256).
			Scan(&job.ID, &job.Status)
		if err != nil {
			return Job{}, fmt.Errorf("select job sha256=%s: %w", sha256, err)
		}
		return job, nil
	}
	if err != nil {
		return Job{}, fmt.Errorf("insert job sha256=%s: %w", sha256, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Job{}, fmt.Errorf("last insert id: %w", err)
	}
	return Job{ID: id, Status: JobImporting}, nil
}

// MarkJobImported records how many data lines the file has and hands the job
// to the relay (IMP-7). Only an IMPORTING job may move to PROCESSING, so a job
// that is already further along is left untouched.
func (s *Store) MarkJobImported(ctx context.Context, jobID int64, totalRows int) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE import_jobs SET status = ?, total_rows = ? WHERE id = ? AND status = ?",
		JobProcessing, totalRows, jobID, JobImporting)
	if err != nil {
		return fmt.Errorf("mark job imported job=%d: %w", jobID, err)
	}
	return nil
}

// isDuplicate reports a MySQL unique key violation (error 1062).
func isDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// Row is one CSV data line ready to be saved in import_rows.
type Row struct {
	LineNo  int
	RawLine string
	Order   csvparse.Order // ignored when Err is not empty
	Err     string         // empty means the line is valid
}

const insertRowsPrefix = "INSERT IGNORE INTO import_rows " +
	"(job_id, line_no, order_id, click_id, campaign_id, amount, order_time, raw_line, status, error) VALUES "

// InsertRows saves one batch with a single multi-row INSERT IGNORE (IMP-6).
// Lines already saved for this job are skipped thanks to uq_job_line, so the
// returned count only includes newly inserted rows.
func (s *Store) InsertRows(ctx context.Context, jobID int64, rows []Row) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	var query strings.Builder
	query.WriteString(insertRowsPrefix)
	args := make([]any, 0, len(rows)*10)
	for i, r := range rows {
		if i > 0 {
			query.WriteString(", ")
		}
		query.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")

		if r.Err == "" {
			o := r.Order
			args = append(args, jobID, r.LineNo, o.OrderID, o.ClickID, o.CampaignID, o.Amount, o.OrderTime, r.RawLine, "PENDING", nil)
		} else {
			args = append(args, jobID, r.LineNo, nil, nil, nil, nil, nil, r.RawLine, "INVALID", r.Err)
		}
	}

	res, err := s.db.ExecContext(ctx, query.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("insert rows job=%d: %w", jobID, err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected job=%d: %w", jobID, err)
	}
	return inserted, nil
}

// PendingRow is the part of a PENDING row the relay puts in a message.
type PendingRow struct {
	ID    int64
	JobID int64
}

// PublishPending is one relay round (REL-1 to REL-3). It locks up to limit
// PENDING rows, calls publish while holding the lock, then marks them PUBLISHED
// in the same transaction. If publish fails the transaction rolls back, so the
// rows stay PENDING and are picked up again in the next round.
//
// SKIP LOCKED makes several relays safe (REL-4): each one skips the rows another
// relay has locked instead of publishing them twice or waiting for them.
func (s *Store) PublishPending(ctx context.Context, limit int, publish func(context.Context, []PendingRow) error) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() // no-op after a successful Commit

	rows, err := lockPending(ctx, tx, limit)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	if err := publish(ctx, rows); err != nil {
		return 0, fmt.Errorf("publish %d rows: %w", len(rows), err)
	}
	if err := markPublished(ctx, tx, rows); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(rows), nil
}

func lockPending(ctx context.Context, tx *sql.Tx, limit int) ([]PendingRow, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT id, job_id FROM import_rows WHERE status = 'PENDING' ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED",
		limit)
	if err != nil {
		return nil, fmt.Errorf("lock pending rows: %w", err)
	}
	defer rows.Close()

	var pending []PendingRow
	for rows.Next() {
		var p PendingRow
		if err := rows.Scan(&p.ID, &p.JobID); err != nil {
			return nil, fmt.Errorf("scan pending row: %w", err)
		}
		pending = append(pending, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read pending rows: %w", err)
	}
	return pending, nil
}

func markPublished(ctx context.Context, tx *sql.Tx, rows []PendingRow) error {
	placeholders := strings.Repeat("?, ", len(rows)-1) + "?"
	args := make([]any, 0, len(rows))
	for _, r := range rows {
		args = append(args, r.ID)
	}
	_, err := tx.ExecContext(ctx,
		"UPDATE import_rows SET status = 'PUBLISHED' WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return fmt.Errorf("mark %d rows published: %w", len(rows), err)
	}
	return nil
}

// FinishJobs moves PROCESSING jobs that have no PENDING or PUBLISHED row left
// to DONE (REL-5). finished_at is written in UTC like every other time.
func (s *Store) FinishJobs(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE import_jobs j SET j.status = ?, j.finished_at = UTC_TIMESTAMP()
		 WHERE j.status = ? AND NOT EXISTS (
		   SELECT 1 FROM import_rows r WHERE r.job_id = j.id AND r.status IN ('PENDING', 'PUBLISHED'))`,
		JobDone, JobProcessing)
	if err != nil {
		return 0, fmt.Errorf("finish jobs: %w", err)
	}
	finished, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("finish jobs rows affected: %w", err)
	}
	return finished, nil
}

// WorkRow is what the worker needs from a PENDING or PUBLISHED row.
type WorkRow struct {
	ID         int64
	JobID      int64
	OrderID    string
	ClickID    string
	CampaignID int64
	Amount     int64
	OrderTime  time.Time
	Status     string
}

// GetRow reads one row by id (WRK-1). The parsed columns are NULL only on
// INVALID rows, which never get a message, so COALESCE just keeps Scan simple.
// order_time is read as sql.NullTime instead: COALESCE with a string literal
// would turn the DATETIME into a string that cannot be scanned into time.Time.
func (s *Store) GetRow(ctx context.Context, id int64) (WorkRow, error) {
	var r WorkRow
	var orderTime sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, job_id, COALESCE(order_id, ''), COALESCE(click_id, ''), COALESCE(campaign_id, 0),
		        COALESCE(amount, 0), order_time, status
		 FROM import_rows WHERE id = ?`, id).
		Scan(&r.ID, &r.JobID, &r.OrderID, &r.ClickID, &r.CampaignID, &r.Amount, &orderTime, &r.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkRow{}, ErrNotFound
	}
	if err != nil {
		return WorkRow{}, fmt.Errorf("get row %d: %w", id, err)
	}
	r.OrderTime = orderTime.Time
	return r, nil
}

// CampaignStatus returns ACTIVE or PAUSED, or ErrNotFound (WRK-3).
func (s *Store) CampaignStatus(ctx context.Context, id int64) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, "SELECT status FROM campaigns WHERE id = ?", id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get campaign %d: %w", id, err)
	}
	return status, nil
}

// MarkDone saves an accepted conversion and finishes the row in one
// transaction (WRK-7). A duplicate order_id (1062) means another row already
// reported this order, so this row becomes SKIPPED instead. It returns the
// status it wrote.
func (s *Store) MarkDone(ctx context.Context, row WorkRow, conversionID string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() // no-op after a successful Commit

	_, err = tx.ExecContext(ctx,
		`INSERT INTO conversions (order_id, row_id, click_id, campaign_id, amount, platform_ref)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		row.OrderID, row.ID, row.ClickID, row.CampaignID, row.Amount, conversionID)
	status, reason := RowDone, ""
	if isDuplicate(err) {
		// InnoDB only undoes the failed statement, so the transaction goes on.
		status, reason = RowSkipped, "duplicate order_id"
	} else if err != nil {
		return "", fmt.Errorf("insert conversion row=%d: %w", row.ID, err)
	}

	if err := finishRow(ctx, tx, row.ID, status, reason); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit row=%d: %w", row.ID, err)
	}
	return status, nil
}

// MarkFailed finishes the row as FAILED with the reason in error (WRK-3, 8, 9).
func (s *Store) MarkFailed(ctx context.Context, rowID int64, reason string) error {
	return finishRow(ctx, s.db, rowID, RowFailed, reason)
}

// execer is what *sql.DB and *sql.Tx have in common, so finishRow works inside
// and outside a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// finishRow only changes a row that is not final yet: when the same message is
// handled twice at once, the second result can never overwrite the first.
func finishRow(ctx context.Context, db execer, rowID int64, status, reason string) error {
	var errorValue any // NULL for DONE
	if reason != "" {
		errorValue = reason
	}
	_, err := db.ExecContext(ctx,
		"UPDATE import_rows SET status = ?, error = ? WHERE id = ? AND status IN (?, ?)",
		status, errorValue, rowID, RowPending, RowPublished)
	if err != nil {
		return fmt.Errorf("set row %d %s: %w", rowID, status, err)
	}
	return nil
}

// IncrementAttempts adds one temporary failure and returns the new count (WRK-9).
// The UPDATE locks the row, so the SELECT in the same transaction reads our own
// value even when another worker is retrying the same row.
func (s *Store) IncrementAttempts(ctx context.Context, rowID int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "UPDATE import_rows SET attempts = attempts + 1 WHERE id = ?", rowID); err != nil {
		return 0, fmt.Errorf("increment attempts row=%d: %w", rowID, err)
	}
	var attempts int
	if err := tx.QueryRowContext(ctx, "SELECT attempts FROM import_rows WHERE id = ?", rowID).Scan(&attempts); err != nil {
		return 0, fmt.Errorf("read attempts row=%d: %w", rowID, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit attempts row=%d: %w", rowID, err)
	}
	return attempts, nil
}
