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
