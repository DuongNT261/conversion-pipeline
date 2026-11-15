// Package config reads every setting from environment variables (docs/01 section 7).
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds the settings the importer needs. Fields for relay and worker
// are added when those programs are written.
type Config struct {
	MySQLDSN          string
	MySQLMaxOpenConns int
	ImportBatchSize   int
	ImportParsers     int
}

// Load reads and validates the settings once at startup, so a bad value stops
// the program before it touches any data.
func Load() (Config, error) {
	maxOpenConns, err := positiveInt("MYSQL_MAX_OPEN_CONNS", 10)
	if err != nil {
		return Config{}, err
	}
	batchSize, err := positiveInt("IMPORT_BATCH_SIZE", 500)
	if err != nil {
		return Config{}, err
	}
	parsers, err := positiveInt("IMPORT_PARSERS", 4)
	if err != nil {
		return Config{}, err
	}

	return Config{
		MySQLDSN:          stringOr("MYSQL_DSN", "app:app@tcp(mysql:3306)/pipeline?parseTime=true"),
		MySQLMaxOpenConns: maxOpenConns,
		ImportBatchSize:   batchSize,
		ImportParsers:     parsers,
	}, nil
}

// stringOr treats an empty variable as unset, the same way compose's ${VAR:-default} does.
func stringOr(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

// positiveInt rejects zero and negatives: 0 parsers or a batch of 0 rows would
// make the importer hang or do nothing.
func positiveInt(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, value)
	}
	return n, nil
}
