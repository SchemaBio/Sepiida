package apikey

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReloadTrimsKeyLines(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("  key-1-0123456789  \n# comment\n\tkey-2-0123456789\t\n\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	manager := NewKeyManager(keyFile)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	if !manager.Validate("key-1-0123456789") || !manager.Validate("key-2-0123456789") {
		t.Fatalf("trimmed keys were not loaded: %v", manager.List())
	}
	if manager.Validate("  key-1-0123456789  ") {
		t.Fatal("untrimmed key should not be valid")
	}
}

func TestReloadParsesScopedQueryKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("scoped-key-012345 uuid=uuid-1,uuid-2 workflow=workflow-1\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	manager := NewKeyManager(keyFile)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	scope, ok := manager.Scope("scoped-key-012345")
	if !ok {
		t.Fatal("expected scoped-key to be loaded")
	}
	if !scope.Restricted() {
		t.Fatal("expected scoped-key to be restricted")
	}
	if !scope.AllowsWorkflow("workflow-1", "") || !scope.AllowsWorkflow("", "uuid-2") {
		t.Fatalf("expected scope to allow configured workflow and uuid: %+v", scope)
	}
	if scope.AllowsWorkflow("workflow-2", "uuid-3") {
		t.Fatal("scope should not allow unconfigured workflow")
	}
}

func TestReloadFailsClosedForUnknownScopeDirective(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("typo-key-0123456 workfow=workflow-1\ncomment-key-012345 # inline comment\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	manager := NewKeyManager(keyFile)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	scope, ok := manager.Scope("typo-key-0123456")
	if !ok {
		t.Fatal("expected typo-key to be loaded")
	}
	if !scope.Restricted() {
		t.Fatalf("unknown scope directive must not create an unrestricted key: %+v", scope)
	}
	if scope.AllowsWorkflow("workflow-1", "") {
		t.Fatal("unknown scope directive should fail closed instead of allowing the intended workflow")
	}

	commentScope, ok := manager.Scope("comment-key-012345")
	if !ok {
		t.Fatal("expected comment-key to be loaded")
	}
	if commentScope.Restricted() {
		t.Fatalf("inline comments without key=value should not restrict an otherwise unrestricted key: %+v", commentScope)
	}
}

func TestReloadIgnoresPlaceholderKeys(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("query-key-001\nagent-key-002\nchange-me-api-key\nreal-random-key-012345\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	manager := NewKeyManager(keyFile)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	for _, placeholder := range []string{"query-key-001", "agent-key-002", "change-me-api-key"} {
		if manager.Validate(placeholder) {
			t.Fatalf("placeholder key %q should not be accepted", placeholder)
		}
	}
	if !manager.Validate("real-random-key-012345") {
		t.Fatalf("expected real key to be loaded, got %v", manager.List())
	}
}

func TestReloadIgnoresWeakKeys(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("short-key\nstrong-key-012345\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	manager := NewKeyManager(keyFile)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	if manager.Validate("short-key") {
		t.Fatal("weak key should not be accepted")
	}
	if !manager.Validate("strong-key-012345") {
		t.Fatalf("expected strong key to be loaded, got %v", manager.List())
	}
}

func TestEmptyKeyFileIsAllowedForDisabledKeyManager(t *testing.T) {
	manager := NewKeyManager("")
	manager.Start(0)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}
	if manager.Count() != 0 {
		t.Fatalf("expected no keys for empty key file, got %v", manager.List())
	}
}

func TestKeyWatcherStopsWhenContextIsCanceled(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("key-1-0123456789\n"), 0o644); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := NewKeyManager(keyFile)
	manager.StartContext(ctx, 10*time.Millisecond)
	if !manager.Validate("key-1-0123456789") {
		t.Fatal("expected initial key to be loaded")
	}
	cancel()
	if err := os.WriteFile(keyFile, []byte("key-2-0123456789\n"), 0o644); err != nil {
		t.Fatalf("replace key file: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if manager.Validate("key-2-0123456789") {
		t.Fatal("key watcher reloaded after its context was canceled")
	}
}

func TestReloadReturnsErrorAndKeepsExistingKeysOnReadFailure(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "keys.txt")
	if err := os.WriteFile(keyFile, []byte("key-1-0123456789\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	manager := NewKeyManager(keyFile)
	if err := manager.Reload(); err != nil {
		t.Fatalf("initial Reload returned error: %v", err)
	}
	if !manager.Validate("key-1-0123456789") {
		t.Fatal("expected initial key to be valid")
	}

	if err := os.Remove(keyFile); err != nil {
		t.Fatalf("failed to remove key file: %v", err)
	}
	if err := manager.Reload(); err == nil {
		t.Fatal("expected Reload to report missing key file")
	}
	if !manager.Validate("key-1-0123456789") {
		t.Fatal("failed reload should keep the last known good key set")
	}
}
