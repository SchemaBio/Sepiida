package main

import "testing"

func TestParsePostgresConnPreservesURLDSN(t *testing.T) {
	dsn := "postgres://user:p%40ss@[::1]:5432/sepiida?sslmode=require&connect_timeout=5"

	cfg := parsePostgresConn(dsn)
	if cfg.DSN != dsn {
		t.Fatalf("expected DSN to be preserved, got %+v", cfg)
	}
}

func TestParsePostgresConnSupportsLegacyFormat(t *testing.T) {
	cfg := parsePostgresConn("localhost:5433/sepiida?user=postgres&password=secret")

	if cfg.DSN != "" {
		t.Fatalf("legacy config should not set DSN: %+v", cfg)
	}
	if cfg.Host != "localhost" || cfg.Port != 5433 || cfg.Database != "sepiida" || cfg.User != "postgres" || cfg.Password != "secret" {
		t.Fatalf("unexpected legacy config: %+v", cfg)
	}
}

func TestNormalizeAuthMode(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "", want: "task-token"},
		{input: " STATIC ", want: "static"},
		{input: "task-token", want: "task-token"},
		{input: "legacy", wantErr: true},
	}
	for _, test := range tests {
		got, err := normalizeAuthMode(test.input)
		if (err != nil) != test.wantErr {
			t.Fatalf("normalizeAuthMode(%q) error = %v, wantErr %t", test.input, err, test.wantErr)
		}
		if got != test.want {
			t.Fatalf("normalizeAuthMode(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
