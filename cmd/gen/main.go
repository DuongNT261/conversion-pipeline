// Command gen writes a sample orders CSV for conversion-pipeline (docs/01 section 4.2).
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Cumulative thresholds of the scenarios. Each row falls into at most one scenario,
// so a single roll compared against these thresholds is enough.
const (
	upToMissingClick = 0.01
	upToBadAmount    = upToMissingClick + 0.005
	upToBadTime      = upToBadAmount + 0.005
	upToDuplicate    = upToBadTime + 0.01
	upToClickOld     = upToDuplicate + 0.01
	upToClickErr     = upToClickOld + 0.002
)

// The campaigns table only has ids 1-200, so ids 201-220 (~9% of rows) never exist.
const (
	existingCampaigns = 200
	maxCampaignID     = 220
)

var (
	badAmounts = []string{"abc", "0", "-1000"}
	badTimes   = []string{"13/11/2026 19:20", "not-a-time", "2026-11-13"}
)

func main() {
	rows := flag.Int("rows", 20000, "number of data rows (header excluded)")
	out := flag.String("out", "./data/orders.csv", "output file path")
	seed := flag.Int64("seed", 42, "random seed; the same seed produces the same file")
	flag.Parse()

	if err := run(*rows, *out, *seed); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(rows int, out string, seed int64) error {
	if rows <= 0 {
		return fmt.Errorf("rows must be > 0, got %d", rows)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := writeOrders(w, rows, seed); err != nil {
		return err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}
	// Close explicitly to catch write errors on disk; the deferred Close is only a safety net.
	return f.Close()
}

// writeOrders writes each row as soon as it is built instead of holding the whole
// file in memory, so it can produce the 1-million-row file for AC-11.
func writeOrders(w *csv.Writer, rows int, seed int64) error {
	rng := rand.New(rand.NewSource(seed))
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.FixedZone("ICT", 7*3600))

	header := []string{"order_id", "click_id", "campaign_id", "amount", "order_time"}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	// prev is the latest row allowed to be duplicated. Only plain rows with an existing
	// campaign qualify, so the first copy can reach DONE and later copies become SKIPPED.
	// PAUSED campaigns come from the seed data, which gen does not know, so a few
	// duplicates may still end as FAILED campaign_paused.
	var prev []string
	for i := 1; i <= rows; i++ {
		campaignID := rng.Intn(maxCampaignID) + 1
		rec := newRecord(rng, base, i, campaignID)
		rec, plain := applyScenario(rng, rec, prev)
		if plain && campaignID <= existingCampaigns {
			prev = rec
		}
		if err := w.Write(rec); err != nil {
			return fmt.Errorf("write row %d: %w", i, err)
		}
	}
	return nil
}

// newRecord builds a valid row. click_id uses hex digits only, so it never
// starts with clk_old or clk_err.
func newRecord(rng *rand.Rand, base time.Time, i int, campaignID int) []string {
	orderTime := base.Add(time.Duration(rng.Intn(24*3600)) * time.Second)
	return []string{
		fmt.Sprintf("A%06d", i),
		fmt.Sprintf("clk_%08x", rng.Int63n(1<<32)),
		strconv.Itoa(campaignID),
		strconv.Itoa((rng.Intn(500) + 1) * 1000),
		orderTime.Format(time.RFC3339),
	}
}

// applyScenario may break the row according to the ratios in docs/01 section 4.2.
// plain reports whether the returned row is untouched.
func applyScenario(rng *rand.Rand, rec, prev []string) (out []string, plain bool) {
	roll := rng.Float64()
	switch {
	case roll < upToMissingClick:
		rec[1] = ""
	case roll < upToBadAmount:
		rec[3] = badAmounts[rng.Intn(len(badAmounts))]
	case roll < upToBadTime:
		rec[4] = badTimes[rng.Intn(len(badTimes))]
	case roll < upToDuplicate:
		if prev == nil {
			return rec, true // nothing to duplicate yet
		}
		return prev, false
	// The worker checks the campaign before calling the API, so these rows need an
	// existing campaign to actually reach WireMock.
	case roll < upToClickOld:
		rec[1] = fmt.Sprintf("clk_old%05x", rng.Intn(1<<20))
		rec[2] = strconv.Itoa(rng.Intn(existingCampaigns) + 1)
	case roll < upToClickErr:
		rec[1] = fmt.Sprintf("clk_err%05x", rng.Intn(1<<20))
		rec[2] = strconv.Itoa(rng.Intn(existingCampaigns) + 1)
	default:
		return rec, true
	}
	return rec, false
}
