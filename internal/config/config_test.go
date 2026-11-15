package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{"MYSQL_DSN", "MYSQL_MAX_OPEN_CONNS", "IMPORT_BATCH_SIZE", "IMPORT_PARSERS"} {
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

	got, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{
		MySQLDSN:          "app:app@tcp(localhost:3306)/pipeline?parseTime=true",
		MySQLMaxOpenConns: 20,
		ImportBatchSize:   1000,
		ImportParsers:     8,
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
