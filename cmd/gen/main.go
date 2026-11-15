// Command gen sinh file CSV đơn hàng mẫu cho conversion-pipeline (docs/01 mục 4.2).
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

// Ngưỡng tích luỹ của các tình huống. Mỗi dòng chỉ rơi vào tối đa một tình huống,
// nên một lần gieo xúc xắc rồi so với các ngưỡng này là đủ.
const (
	upToMissingClick = 0.01
	upToBadAmount    = upToMissingClick + 0.005
	upToBadTime      = upToBadAmount + 0.005
	upToDuplicate    = upToBadTime + 0.01
	upToClickOld     = upToDuplicate + 0.01
	upToClickErr     = upToClickOld + 0.002
)

var (
	badAmounts = []string{"abc", "0", "-1000"}
	badTimes   = []string{"13/11/2026 19:20", "not-a-time", "2026-11-13"}
)

func main() {
	rows := flag.Int("rows", 20000, "số dòng dữ liệu (không tính header)")
	out := flag.String("out", "./data/orders.csv", "đường dẫn file đầu ra")
	seed := flag.Int64("seed", 42, "seed ngẫu nhiên, cùng seed thì cùng file")
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
	// Close lần hai ở đây để bắt lỗi ghi xuống đĩa; defer ở trên chỉ là lưới an toàn.
	return f.Close()
}

// writeOrders ghi từng dòng ra ngay, không giữ cả file trong bộ nhớ,
// để sinh được file 1 triệu dòng cho AC-11.
func writeOrders(w *csv.Writer, rows int, seed int64) error {
	rng := rand.New(rand.NewSource(seed))
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.FixedZone("ICT", 7*3600))

	header := []string{"order_id", "click_id", "campaign_id", "amount", "order_time"}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	// prev là dòng bình thường gần nhất, dùng để sinh dòng trùng order_id.
	// Chỉ nhân bản dòng bình thường để kết quả mong đợi đúng: bản đầu DONE, bản sau SKIPPED.
	var prev []string
	for i := 1; i <= rows; i++ {
		rec := newRecord(rng, base, i)
		rec, plain := applyScenario(rng, rec, prev)
		if plain {
			prev = rec
		}
		if err := w.Write(rec); err != nil {
			return fmt.Errorf("write row %d: %w", i, err)
		}
	}
	return nil
}

// newRecord tạo một dòng hợp lệ. click_id chỉ dùng chữ số hex nên không bao giờ
// bắt đầu bằng clk_old hay clk_err.
func newRecord(rng *rand.Rand, base time.Time, i int) []string {
	orderTime := base.Add(time.Duration(rng.Intn(24*3600)) * time.Second)
	return []string{
		fmt.Sprintf("A%06d", i),
		fmt.Sprintf("clk_%08x", rng.Int63n(1<<32)),
		strconv.Itoa(rng.Intn(220) + 1), // 201-220 là campaign không tồn tại (~9%)
		strconv.Itoa((rng.Intn(500) + 1) * 1000),
		orderTime.Format(time.RFC3339),
	}
}

// applyScenario có thể làm hỏng dòng theo tỉ lệ ở docs/01 mục 4.2.
// plain báo dòng trả về là dòng bình thường, được phép làm nguồn cho dòng trùng.
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
			return rec, true // chưa có dòng nào để trùng
		}
		return prev, false
	case roll < upToClickOld:
		rec[1] = fmt.Sprintf("clk_old%05x", rng.Intn(1<<20))
	case roll < upToClickErr:
		rec[1] = fmt.Sprintf("clk_err%05x", rng.Intn(1<<20))
	default:
		return rec, true
	}
	return rec, false
}
