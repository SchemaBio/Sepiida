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
