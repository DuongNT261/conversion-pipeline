// Command report prints the result of one import job (docs/01 section 6.5).
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"github.com/duongnt261/conversion-pipeline/internal/config"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// waitPoll is how often -wait checks whether the job is DONE.
const waitPoll = 2 * time.Second

func main() {
	jobID := flag.Int64("job", 0, "job id to report (default: the latest job)")
	wait := flag.Bool("wait", false, "wait until the job is DONE, then print")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "report: load config:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, cfg, *jobID, *wait, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "report:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, jobID int64, wait bool, out io.Writer) error {
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	st := store.New(db)

	if jobID == 0 {
		jobID, err = st.LatestJobID(ctx)
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("no job yet: run the importer first")
		}
		if err != nil {
			return err
		}
	}
	r, err := waitForJob(ctx, st, jobID, wait)
	if err != nil {
		return err
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, MaxRetries: -1})
	defer rdb.Close()

	printJob(out, r)
	printRedis(ctx, out, rdb, cfg.RateLimitRPS)
	printWireMock(ctx, out, cfg.PlatformURL)
	check := "OK"
	if r.Conversions != r.ByStatus[store.RowDone] {
		check = "MISMATCH"
	}
	fmt.Fprintf(out, "Conversions    rows for this job = %d   (must equal done = %d) -> %s\n",
		r.Conversions, r.ByStatus[store.RowDone], check)
	return nil
}

// waitForJob reads the job report, and with wait=true reads it again every
// waitPoll until the relay has marked the job DONE.
func waitForJob(ctx context.Context, st *store.Store, jobID int64, wait bool) (store.JobReport, error) {
	for {
		r, err := st.GetJobReport(ctx, jobID)
		if errors.Is(err, store.ErrNotFound) {
			return store.JobReport{}, fmt.Errorf("job %d does not exist", jobID)
		}
		if err != nil || !wait || r.Status == store.JobDone {
			return r, err
		}
		select {
		case <-ctx.Done():
			return store.JobReport{}, ctx.Err()
		case <-time.After(waitPoll):
		}
	}
}
