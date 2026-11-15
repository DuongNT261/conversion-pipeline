package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{"MYSQL_DSN", "MYSQL_MAX_OPEN_CONNS", "IMPORT_BATCH_SIZE", "IMPORT_PARSERS",
		"PUBSUB_EMULATOR_HOST", "PUBSUB_PROJECT_ID", "PUBSUB_TOPIC", "PUBSUB_SUBSCRIPTION", "RELAY_BATCH_SIZE", "RELAY_INTERVAL"} {
		t.Setenv(key, "")
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{
		MySQLDSN:          "app:app@tcp(mysql:3306)/pipeline?parseTime=true",
		MySQLMaxOpenConns: 10,
		ImportBatchSize:   500,
		ImportParsers:     4,

		PubSubEmulatorHost: "pubsub:8085",
		PubSubProjectID:    "demo",
		PubSubTopic:        "rows-to-process",
		PubSubSubscription: "rows-worker",
		RelayBatchSize:     500,
		RelayInterval:      time.Second,
	}
	if got != want {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}

func TestLoadOverride(t *testing.T) {
	t.Setenv("MYSQL_DSN", "app:app@tcp(localhost:3306)/pipeline?parseTime=true")
	t.Setenv("MYSQL_MAX_OPEN_CONNS", "20")
	t.Setenv("IMPORT_BATCH_SIZE", "1000")
	t.Setenv("IMPORT_PARSERS", "8")
	t.Setenv("PUBSUB_EMULATOR_HOST", "localhost:8085")
	t.Setenv("PUBSUB_PROJECT_ID", "p")
	t.Setenv("PUBSUB_TOPIC", "t")
	t.Setenv("PUBSUB_SUBSCRIPTION", "s")
	t.Setenv("RELAY_BATCH_SIZE", "100")
	t.Setenv("RELAY_INTERVAL", "250ms")

	got, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{
		MySQLDSN:          "app:app@tcp(localhost:3306)/pipeline?parseTime=true",
		MySQLMaxOpenConns: 20,
		ImportBatchSize:   1000,
		ImportParsers:     8,

		PubSubEmulatorHost: "localhost:8085",
		PubSubProjectID:    "p",
		PubSubTopic:        "t",
		PubSubSubscription: "s",
		RelayBatchSize:     100,
		RelayInterval:      250 * time.Millisecond,
	}
	if got != want {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{"not a number", "IMPORT_PARSERS", "abc", `IMPORT_PARSERS must be a positive integer, got "abc"`},
		{"zero", "IMPORT_PARSERS", "0", `IMPORT_PARSERS must be a positive integer, got "0"`},
		{"negative", "IMPORT_BATCH_SIZE", "-5", `IMPORT_BATCH_SIZE must be a positive integer, got "-5"`},
		{"decimal", "MYSQL_MAX_OPEN_CONNS", "1.5", `MYSQL_MAX_OPEN_CONNS must be a positive integer, got "1.5"`},
		{"relay interval not a duration", "RELAY_INTERVAL", "1", `RELAY_INTERVAL must be a positive duration such as 1s, got "1"`},
		{"relay interval zero", "RELAY_INTERVAL", "0s", `RELAY_INTERVAL must be a positive duration such as 1s, got "0s"`},
		{"relay batch zero", "RELAY_BATCH_SIZE", "0", `RELAY_BATCH_SIZE must be a positive integer, got "0"`},
		{"relay batch over placeholder limit", "RELAY_BATCH_SIZE", "65536", "RELAY_BATCH_SIZE must be at most 65535, got 65536"},
		{"batch over placeholder limit", "IMPORT_BATCH_SIZE", "6554", "IMPORT_BATCH_SIZE must be at most 6553, got 6554"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			_, err := Load()
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("want error %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLoadBatchSizeAtLimit(t *testing.T) {
	t.Setenv("IMPORT_BATCH_SIZE", "6553")

	got, err := Load()
	if err != nil || got.ImportBatchSize != 6553 {
		t.Fatalf("want 6553, nil; got %d, %v", got.ImportBatchSize, err)
	}
}
