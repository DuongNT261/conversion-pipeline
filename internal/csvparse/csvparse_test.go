package csvparse

import (
	"strings"
	"testing"
	"time"
)

const validTime = "2026-11-13T19:20:00+07:00"

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		record  []string
		wantErr string
	}{
		{"valid", []string{"A1", "clk_1", "17", "1000", validTime}, ""},
		{"too few columns", []string{"A1", "clk_1", "17", "1000"}, "want 5 columns, got 4"},
		{"too many columns", []string{"A1", "clk_1", "17", "1000", validTime, "x"}, "want 5 columns, got 6"},

		{"missing order_id", []string{"", "clk_1", "17", "1000", validTime}, "missing order_id"},
		{"order_id 64 chars", []string{strings.Repeat("A", 64), "clk_1", "17", "1000", validTime}, ""},
		{"order_id 65 chars", []string{strings.Repeat("A", 65), "clk_1", "17", "1000", validTime}, "order_id too long"},
		{"order_id 64 multi-byte chars", []string{strings.Repeat("Đ", 64), "clk_1", "17", "1000", validTime}, ""},

		{"missing click_id", []string{"A1", "", "17", "1000", validTime}, "missing click_id"},
		{"click_id 128 chars", []string{"A1", strings.Repeat("c", 128), "17", "1000", validTime}, ""},
		{"click_id 129 chars", []string{"A1", strings.Repeat("c", 129), "17", "1000", validTime}, "click_id too long"},

		{"missing campaign_id", []string{"A1", "clk_1", "", "1000", validTime}, "missing campaign_id"},
		{"blank campaign_id", []string{"A1", "clk_1", "   ", "1000", validTime}, "missing campaign_id"},
		{"campaign_id not a number", []string{"A1", "clk_1", "abc", "1000", validTime}, "invalid campaign_id"},
		{"campaign_id zero", []string{"A1", "clk_1", "0", "1000", validTime}, "invalid campaign_id"},
		{"campaign_id negative", []string{"A1", "clk_1", "-3", "1000", validTime}, "invalid campaign_id"},

		{"missing amount", []string{"A1", "clk_1", "17", "", validTime}, "missing amount"},
		{"amount not a number", []string{"A1", "clk_1", "17", "abc", validTime}, "invalid amount"},
		{"amount zero", []string{"A1", "clk_1", "17", "0", validTime}, "invalid amount"},
		{"amount negative", []string{"A1", "clk_1", "17", "-1000", validTime}, "invalid amount"},
		{"amount decimal", []string{"A1", "clk_1", "17", "1.5", validTime}, "invalid amount"},
		{"amount exponent", []string{"A1", "clk_1", "17", "1e3", validTime}, "invalid amount"},
		{"amount with plus sign", []string{"A1", "clk_1", "17", "+500", validTime}, ""},
		{"amount with spaces", []string{"A1", "clk_1", "17", " 500 ", validTime}, ""},

		{"missing order_time", []string{"A1", "clk_1", "17", "1000", ""}, "missing order_time"},
		{"order_time date only", []string{"A1", "clk_1", "17", "1000", "2026-11-13"}, "invalid order_time"},
		{"order_time wrong layout", []string{"A1", "clk_1", "17", "1000", "13/11/2026 19:20"}, "invalid order_time"},
		{"order_time utc", []string{"A1", "clk_1", "17", "1000", "2026-11-13T12:00:00Z"}, ""},
		{"order_time with spaces", []string{"A1", "clk_1", "17", "1000", " " + validTime + " "}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.record)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr) {
				t.Fatalf("want error %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestParseValues(t *testing.T) {
	got, err := Parse([]string{"A1", "clk_1", " 17 ", "+500", validTime})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := Order{
		OrderID:    "A1",
		ClickID:    "clk_1",
		CampaignID: 17,
		Amount:     500,
		OrderTime:  time.Date(2026, 11, 13, 12, 20, 0, 0, time.UTC),
	}
	if got != want {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}
