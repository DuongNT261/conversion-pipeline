package main

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/duongnt261/conversion-pipeline/internal/store"
)

func TestPrintJob(t *testing.T) {
	created := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	base := store.JobReport{
		ID: 1, FileName: "/data/orders.csv", Status: store.JobDone, CreatedAt: created,
		FinishedAt:  sql.NullTime{Time: created.Add(125 * time.Second), Valid: true},
		TotalRows:   10,
		ByStatus:    map[string]int{"DONE": 5, "SKIPPED": 1, "FAILED": 3, "INVALID": 1},
		FailReasons: map[string]int{"max_attempts": 1, "campaign_not_found": 2},
	}
	running := base
	running.Status, running.FinishedAt = store.JobProcessing, sql.NullTime{}
	running.ByStatus = map[string]int{"DONE": 5, "PUBLISHED": 5}

	tests := []struct {
		name string
		r    store.JobReport
		want []string
	}{
		{"finished job", base, []string{
			"Job 1  file=/data/orders.csv  status=DONE  duration=2m5s",
			"failed   3   (campaign_not_found=2, max_attempts=1)",
			"= total  -> OK",
		}},
		{"running job", running, []string{
			"duration=running",
			"pending  5   (still being processed)",
			"-> MISMATCH (sum=5)",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			printJob(&out, tt.r)
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output does not contain %q:\n%s", w, out.String())
				}
			}
		})
	}
}
