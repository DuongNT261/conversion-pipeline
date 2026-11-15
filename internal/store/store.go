// Package store holds every SQL statement of the project (docs/03 section 5).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/duongnt261/conversion-pipeline/internal/csvparse"
)

// Store wraps the single *sql.DB of the process.
type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
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
