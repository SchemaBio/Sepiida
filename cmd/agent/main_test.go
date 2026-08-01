package main

import (
	"strings"
	"testing"
)

func TestParseWatchDirsDropsEmptyEntries(t *testing.T) {
	got := parseWatchDirs(` /data/a,,"/data/b", '' `)
	want := []string{"/data/a", "/data/b"}

	if len(got) != len(want) {
		t.Fatalf("unexpected dirs: got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected dirs: got %#v want %#v", got, want)
		}
	}
}

func TestRedactURLForLog(t *testing.T) {
	got := redactURLForLog("https://user:secret@example.test/archive?token=abc&prefix=ok")

	if strings.Contains(got, "secret") || strings.Contains(got, "token=abc") {
		t.Fatalf("URL was not redacted: %s", got)
	}
	if !strings.Contains(got, "prefix=ok") {
		t.Fatalf("non-sensitive query parameter should be preserved: %s", got)
	}
}

func TestDefaultServerURLIgnoresRemovedAliases(t *testing.T) {
	t.Setenv("SEPIIDA_SERVER_URL", "")
	t.Setenv("SEPIIDA_API_URL", "http://legacy-api:9090")
	t.Setenv("SEPIIDA_SERVER", "http://legacy-server:9090")
	if got := defaultServerURL(); got != "" {
		t.Fatalf("removed server URL aliases still affected configuration: %q", got)
	}
}

func TestValidateAgentCredentialsRequiresExactlyOneMode(t *testing.T) {
	if err := validateAgentCredentials("static-key", ""); err != nil {
		t.Fatalf("static key should be valid: %v", err)
	}
	if err := validateAgentCredentials("", "task-token"); err != nil {
		t.Fatalf("task token should be valid: %v", err)
	}
	if err := validateAgentCredentials("", ""); err == nil {
		t.Fatal("missing credentials should fail")
	}
	if err := validateAgentCredentials("static-key", "task-token"); err == nil {
		t.Fatal("mixed authentication modes should fail")
	}
}
