// Package config reads every setting from environment variables (docs/01 section 7).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds the settings of every program; each one reads the fields it needs.
type Config struct {
	MySQLDSN          string
	MySQLMaxOpenConns int

	ImportBatchSize int
	ImportParsers   int

	PubSubEmulatorHost string
	PubSubProjectID    string
	PubSubTopic        string
	PubSubSubscription string
	RelayBatchSize     int
	RelayInterval      time.Duration
}

// maxImportBatchSize keeps one batch INSERT under MySQL's limit of 65535
// placeholders: each import_rows line uses 10 of them.
const maxImportBatchSize = 65535 / 10

// maxRelayBatchSize keeps the UPDATE ... WHERE id IN (...) of one relay round
// under the same limit: one placeholder per row.
const maxRelayBatchSize = 65535

// Load reads and validates the settings once at startup, so a bad value stops
// the program before it touches any data.
func Load() (Config, error) {
	cfg := Config{
		MySQLDSN:           stringOr("MYSQL_DSN", "app:app@tcp(mysql:3306)/pipeline?parseTime=true"),
		PubSubEmulatorHost: stringOr("PUBSUB_EMULATOR_HOST", "pubsub:8085"),
		PubSubProjectID:    stringOr("PUBSUB_PROJECT_ID", "demo"),
		PubSubTopic:        stringOr("PUBSUB_TOPIC", "rows-to-process"),
		PubSubSubscription: stringOr("PUBSUB_SUBSCRIPTION", "rows-worker"),
	}

	var err error
	if cfg.MySQLMaxOpenConns, err = positiveInt("MYSQL_MAX_OPEN_CONNS", 10); err != nil {
		return Config{}, err
	}
	if cfg.ImportBatchSize, err = positiveInt("IMPORT_BATCH_SIZE", 500); err != nil {
		return Config{}, err
	}
	if cfg.ImportBatchSize > maxImportBatchSize {
		return Config{}, fmt.Errorf("IMPORT_BATCH_SIZE must be at most %d, got %d", maxImportBatchSize, cfg.ImportBatchSize)
	}
	if cfg.ImportParsers, err = positiveInt("IMPORT_PARSERS", 4); err != nil {
		return Config{}, err
	}
	if cfg.RelayBatchSize, err = positiveInt("RELAY_BATCH_SIZE", 500); err != nil {
		return Config{}, err
	}
	if cfg.RelayBatchSize > maxRelayBatchSize {
		return Config{}, fmt.Errorf("RELAY_BATCH_SIZE must be at most %d, got %d", maxRelayBatchSize, cfg.RelayBatchSize)
	}
	if cfg.RelayInterval, err = positiveDuration("RELAY_INTERVAL", time.Second); err != nil {
		return Config{}, err
	}
	return cfg, nil
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

// positiveDuration accepts Go durations such as "500ms" or "2s"; 0 would make
// time.NewTicker panic.
func positiveDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 1s, got %q", key, value)
	}
	return d, nil
}
