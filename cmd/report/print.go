package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/duongnt261/conversion-pipeline/internal/cache"
	"github.com/duongnt261/conversion-pipeline/internal/store"
)

func printJob(out io.Writer, r store.JobReport) {
	duration := "running"
	if r.FinishedAt.Valid {
		duration = r.FinishedAt.Time.Sub(r.CreatedAt).Round(time.Second).String()
	}
	fmt.Fprintf(out, "Job %d  file=%s  status=%s  duration=%s\n", r.ID, r.FileName, r.Status, duration)

	done, skipped := r.ByStatus[store.RowDone], r.ByStatus[store.RowSkipped]
	failed, invalid := r.ByStatus[store.RowFailed], r.ByStatus[store.RowInvalid]
	inFlight := r.ByStatus[store.RowPending] + r.ByStatus[store.RowPublished]

	fmt.Fprintf(out, "  total    %d\n", r.TotalRows)
	fmt.Fprintf(out, "  done     %d\n", done)
	fmt.Fprintf(out, "  skipped  %d\n", skipped)
	fmt.Fprintf(out, "  failed   %d   (%s)\n", failed, reasons(r.FailReasons))
	fmt.Fprintf(out, "  invalid  %d\n", invalid)
	if inFlight > 0 {
		fmt.Fprintf(out, "  pending  %d   (still being processed)\n", inFlight)
	}

	check := "OK"
	if sum := done + skipped + failed + invalid; sum != r.TotalRows {
		check = fmt.Sprintf("MISMATCH (sum=%d)", sum)
	}
	fmt.Fprintf(out, "  check    done + skipped + failed + invalid = total  -> %s\n", check)
}

// reasons prints "a=1, b=2" sorted by name, so the output is stable.
func reasons(counts map[string]int) string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, counts[name]))
	}
	return strings.Join(parts, ", ")
}

// printRedis prints the cache stats and the highest calls:{second} counter.
// The counters cover every run of the last hour, not only this job.
func printRedis(ctx context.Context, out io.Writer, rdb *redis.Client, limit int) {
	stats, err := rdb.HGetAll(ctx, cache.StatsKey).Result()
	if err != nil {
		fmt.Fprintf(out, "Redis cache    unavailable (%v)\n", err)
		return
	}
	fmt.Fprintf(out, "Redis cache    hits=%s  misses=%s\n", orZero(stats["hits"]), orZero(stats["misses"]))

	// SCAN walks the keys in small steps instead of blocking Redis like KEYS would.
	peak := 0
	iter := rdb.Scan(ctx, 0, "calls:*", 1000).Iterator()
	for iter.Next(ctx) {
		n, err := rdb.Get(ctx, iter.Val()).Int()
		if err == nil && n > peak {
			peak = n
		}
	}
	if err := iter.Err(); err != nil {
		fmt.Fprintf(out, "API calls/sec  unavailable (%v)\n", err)
		return
	}
	fmt.Fprintf(out, "API calls/sec  peak=%d  limit=%d  (from calls:* in Redis, last hour)\n", peak, limit)
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// printWireMock asks WireMock how many conversion requests it has received.
func printWireMock(ctx context.Context, out io.Writer, platformURL string) {
	count, err := wireMockCount(ctx, platformURL)
	if err != nil {
		fmt.Fprintf(out, "WireMock       unavailable (%v)\n", err)
		return
	}
	fmt.Fprintf(out, "WireMock       POST /v1/conversions received=%d\n", count)
}

func wireMockCount(ctx context.Context, platformURL string) (int, error) {
	body := []byte(`{"method":"POST","url":"/v1/conversions"}`)
	url := strings.TrimSuffix(platformURL, "/") + "/__admin/requests/count"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http %d", resp.StatusCode)
	}
	var result struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode count: %w", err)
	}
	return result.Count, nil
}
