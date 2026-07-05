package apikey

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReloadTrimsKeyLines(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("  key-1  \n# comment\n\tkey-2\t\n\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	manager := NewKeyManager(keyFile)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	if !manager.Validate("key-1") || !manager.Validate("key-2") {
		t.Fatalf("trimmed keys were not loaded: %v", manager.List())
	}
	if manager.Validate("  key-1  ") {
		t.Fatal("untrimmed key should not be valid")
	}
}

func TestReloadParsesScopedQueryKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("scoped-key uuid=uuid-1,uuid-2 workflow=workflow-1\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	manager := NewKeyManager(keyFile)
	if err := manager.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	scope, ok := manager.Scope("scoped-key")
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
