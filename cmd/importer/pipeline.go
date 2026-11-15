package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/duongnt261/conversion-pipeline/internal/csvparse"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

// channelBuffer lets the reader run ahead of the parsers, and the parsers ahead
// of a slow DB write, while memory stays bounded (docs/02 section 4).
const channelBuffer = 1000

// rowInserter is the one store method the pipeline needs; tests pass a fake.
type rowInserter interface {
	InsertRows(ctx context.Context, jobID int64, rows []store.Row) (int64, error)
}

// line is one CSV record with its line number, fixed by the reader so the
// parsers can finish in any order.
type line struct {
	no     int
	record []string
}

// firstError keeps the first failure of any stage and cancels all the others.
type firstError struct {
	once   sync.Once
	err    error
	cancel context.CancelFunc
}

func (f *firstError) set(err error) {
	f.once.Do(func() {
		f.err = err
		f.cancel()
	})
}

// runPipeline streams the file through 1 reader, `parsers` parsers and 1 writer
// (IMP-3, IMP-4). It returns the number of data lines and of newly inserted rows.
func runPipeline(ctx context.Context, src io.Reader, db rowInserter, jobID int64, parsers, batchSize int) (int, int64, error) {
	// A stop signal must not cancel the final writes: IMP-8 wants the
	// collected rows saved before exit.
	writeCtx := context.WithoutCancel(ctx)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fail := &firstError{cancel: cancel}

	lines := make(chan line, channelBuffer)
	rows := make(chan store.Row, channelBuffer)

	var total int
	var inserted int64
	var readerAndWriter, parserGroup sync.WaitGroup

	readerAndWriter.Add(2)
	go func() {
		defer readerAndWriter.Done()
		defer close(lines) // the reader is the only sender on lines
		n, err := readLines(ctx, src, lines)
		if err != nil {
			fail.set(err)
			return
		}
		total = n
	}()

	parserGroup.Add(parsers)
	for i := 0; i < parsers; i++ {
		go func() {
			defer parserGroup.Done()
			parseLines(ctx, lines, rows)
		}()
	}
	// rows has several senders, so it is closed only after all of them are done.
	go func() {
		parserGroup.Wait()
		close(rows)
	}()

	go func() {
		defer readerAndWriter.Done()
		n, err := writeRows(writeCtx, db, jobID, batchSize, rows)
		if err != nil {
			fail.set(err)
		}
		inserted = n
	}()

	readerAndWriter.Wait()
	if fail.err != nil {
		return 0, inserted, fail.err
	}
	return total, inserted, nil
}

// readLines skips the header and sends every data record with its line number.
func readLines(ctx context.Context, src io.Reader, out chan<- line) (int, error) {
	r := csv.NewReader(src)
	r.FieldsPerRecord = -1 // a wrong column count becomes an INVALID row, not a fatal error
	if _, err := r.Read(); err != nil {
		return 0, fmt.Errorf("read header: %w", err)
	}

	count := 0
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			return count, nil
		}
		if err != nil {
			return 0, fmt.Errorf("read csv: %w", err)
		}
		lineNo, _ := r.FieldPos(0)

		select {
		case out <- line{no: lineNo, record: record}:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		count++
	}
}

// parseLines turns lines into rows; a bad line is still saved, as INVALID (IMP-5).
func parseLines(ctx context.Context, in <-chan line, out chan<- store.Row) {
	for l := range in {
		row := store.Row{LineNo: l.no, RawLine: rawLine(l.record)}
		order, err := csvparse.Parse(l.record)
		if err != nil {
			row.Err = err.Error()
		} else {
			row.Order = order
		}

		select {
		case out <- row:
		case <-ctx.Done():
			return
		}
	}
}

// writeRows groups rows into batches of batchSize and saves each batch with one
// INSERT IGNORE (IMP-6). Rows still in the channel when it closes are flushed.
func writeRows(ctx context.Context, db rowInserter, jobID int64, batchSize int, rows <-chan store.Row) (int64, error) {
	var inserted int64
	batch := make([]store.Row, 0, batchSize)
	for row := range rows {
		batch = append(batch, row)
		if len(batch) < batchSize {
			continue
		}
		n, err := db.InsertRows(ctx, jobID, batch)
		if err != nil {
			return inserted, err
		}
		inserted += n
		batch = batch[:0]
	}

	n, err := db.InsertRows(ctx, jobID, batch) // an empty batch is a no-op
	return inserted + n, err
}

// rawLine re-encodes the record as one CSV line, because csv.Reader does not
// keep the original text.
func rawLine(record []string) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Write(record) // only fails when the destination fails, and strings.Builder never does
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}
