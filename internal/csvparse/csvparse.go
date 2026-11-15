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

// Parse validates one data record (header excluded) and returns the first rule it breaks.
// It has no shared state, so the importer's parser goroutines can call it concurrently.
func Parse(record []string) (Order, error) {
	if len(record) != columnCount {
		return Order{}, fmt.Errorf("want %d columns, got %d", columnCount, len(record))
	}
	if err := checkText("order_id", record[0], 64); err != nil {
		return Order{}, err
	}
	if err := checkText("click_id", record[1], 128); err != nil {
		return Order{}, err
	}
	campaignID, err := parsePositive("campaign_id", record[2])
	if err != nil {
		return Order{}, err
	}
	amount, err := parsePositive("amount", record[3])
	if err != nil {
		return Order{}, err
	}
	rawTime := strings.TrimSpace(record[4])
	if rawTime == "" {
		return Order{}, errors.New("missing order_time")
	}
	orderTime, err := time.Parse(time.RFC3339, rawTime)
	if err != nil {
		return Order{}, errors.New("invalid order_time")
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
func checkText(name, value string, maxChars int) error {
	if value == "" {
		return fmt.Errorf("missing %s", name)
	}
	if utf8.RuneCountInString(value) > maxChars {
		return fmt.Errorf("%s too long", name)
	}
	return nil
}

// parsePositive drops the strconv error on purpose: the message is stored in
// import_rows.error, so it must stay short and stable.
func parsePositive(name, value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("missing %s", name)
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return n, nil
}
