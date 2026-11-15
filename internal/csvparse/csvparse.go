// Package csvparse turns one CSV record into a validated Order (docs/01 section 4.1).
package csvparse

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const columnCount = 5

// Order is a CSV record that passed every rule in docs/01 section 4.1.
type Order struct {
	OrderID    string
	ClickID    string
	CampaignID int64
	Amount     int64
	OrderTime  time.Time // UTC, as stored in MySQL
}

// Parse validates one data record (header excluded). It checks every column and
// reports all the problems at once, joined by "; ", so the file provider can fix
// a line in one go instead of discovering one problem per resend.
// It has no shared state, so the importer's parser goroutines can call it concurrently.
func Parse(record []string) (Order, error) {
	// Without the right column count we cannot tell which value is which column.
	if len(record) != columnCount {
		return Order{}, fmt.Errorf("want %d columns, got %d", columnCount, len(record))
	}

	var problems []string
	if p := checkText("order_id", record[0], 64); p != "" {
		problems = append(problems, p)
	}
	if p := checkText("click_id", record[1], 128); p != "" {
		problems = append(problems, p)
	}
	campaignID, p := parsePositive("campaign_id", record[2])
	if p != "" {
		problems = append(problems, p)
	}
	amount, p := parsePositive("amount", record[3])
	if p != "" {
		problems = append(problems, p)
	}
	orderTime, p := parseOrderTime(record[4])
	if p != "" {
		problems = append(problems, p)
	}
	if len(problems) > 0 {
		return Order{}, errors.New(strings.Join(problems, "; "))
	}

	return Order{
		OrderID:    record[0],
		ClickID:    record[1],
		CampaignID: campaignID,
		Amount:     amount,
		OrderTime:  orderTime.UTC(),
	}, nil
}

// checkText counts characters, not bytes, because the VARCHAR limits are in characters.
// It returns the problem, or "" when the value is fine.
func checkText(name, value string, maxChars int) string {
	if value == "" {
		return "missing " + name
	}
	if utf8.RuneCountInString(value) > maxChars {
		return name + " too long"
	}
	return ""
}

// parsePositive drops the strconv error on purpose: the message is stored in
// import_rows.error, so it must stay short and stable.
func parsePositive(name, value string) (int64, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, "missing " + name
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, "invalid " + name
	}
	return n, ""
}

func parseOrderTime(value string) (time.Time, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, "missing order_time"
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, "invalid order_time"
	}
	return t, ""
}
