// Package store holds every SQL statement of the project (docs/03 section 5).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/duongnt261/conversion-pipeline/internal/csvparse"
)

// Job statuses (docs/01 section 5.3).
const (
	JobImporting  = "IMPORTING"
	JobProcessing = "PROCESSING"
	JobDone       = "DONE"
)

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
