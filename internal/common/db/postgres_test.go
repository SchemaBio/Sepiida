package db

import "testing"

func TestNormalizeJSONB(t *testing.T) {
	if got := normalizeJSONB(""); got != nil {
		t.Fatalf("empty JSON should become nil, got %#v", got)
	}
	if got := normalizeJSONB("  "); got != nil {
		t.Fatalf("blank JSON should become nil, got %#v", got)
	}
	if got := normalizeJSONB(`{"ok":true}`); got != `{"ok":true}` {
		t.Fatalf("valid JSON changed: %#v", got)
	}
	if got := normalizeJSONB(`not-json`); got != `"not-json"` {
		t.Fatalf("invalid JSON should be stored as JSON string, got %#v", got)
	}
}

func TestPostgresURLDSNEscapesComponents(t *testing.T) {
	got := postgresURLDSN(Config{
		Host:     "db.internal",
		Port:     5433,
		User:     "sepiida user",
		Password: `p@ss word sslmode=require`,
		Database: "sep/iida",
	})

	want := "postgres://sepiida%20user:p%40ss%20word%20sslmode=require@db.internal:5433/sep%2Fiida?sslmode=disable"
	if got != want {
		t.Fatalf("unexpected DSN\nwant: %s\n got: %s", want, got)
	}
}
