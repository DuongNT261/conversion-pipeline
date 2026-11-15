// Command importer loads one CSV file into import_rows (docs/01 section 6.1).
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/duongnt261/conversion-pipeline/internal/config"
	"github.com/duongnt261/conversion-pipeline/internal/source"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

func main() {
	file := flag.String("file", "", "path of the CSV file to import")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if *file == "" {
		logger.Error("missing -file flag")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, logger, cfg, *file)
	stop()
	if err != nil {
		logger.Error("import failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, cfg config.Config, name string) error {
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.MySQLMaxOpenConns)
	db.SetMaxIdleConns(cfg.MySQLMaxOpenConns)
	db.SetConnMaxLifetime(5 * time.Minute)
	// sql.Open does not connect; fail now if the DSN or MySQL is wrong.
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	st := store.New(db)
	var src source.Source = source.Local{}

	sha, err := fileSHA256(ctx, src, name)
	if err != nil {
		return err
	}
	job, err := st.FindOrCreateJob(ctx, name, sha)
	if err != nil {
		return err
	}
	if job.Status != store.JobImporting {
		logger.Info("file already imported, nothing to do", "job_id", job.ID, "status", job.Status)
		return nil
	}
	logger.Info("import started", "job_id", job.ID, "file", name)

	f, err := src.Open(ctx, name)
	if err != nil {
		return err
	}
	defer f.Close()

	total, inserted, err := runPipeline(ctx, f, st, job.ID, cfg.ImportParsers, cfg.ImportBatchSize)
	if err != nil {
		return fmt.Errorf("job %d stays IMPORTING, run again to resume: %w", job.ID, err)
	}
	if err := st.MarkJobImported(ctx, job.ID, total); err != nil {
		return err
	}
	logger.Info("import finished", "job_id", job.ID, "total_rows", total, "inserted", inserted, "status", store.JobProcessing)
	return nil
}

// fileSHA256 hashes the file while streaming it, so a 5 GB file never sits in memory (IMP-1).
func fileSHA256(ctx context.Context, src source.Source, name string) (string, error) {
	f, err := src.Open(ctx, name)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", name, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
