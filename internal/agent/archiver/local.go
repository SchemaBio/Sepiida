package archiver

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalBackend archives files to a local filesystem path.
type LocalBackend struct {
	rootPath string
}

// NewLocalBackend creates a local filesystem archive backend.
func NewLocalBackend(rootPath string) (*LocalBackend, error) {
	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("invalid archive path: %w", err)
	}
	return &LocalBackend{rootPath: absPath}, nil
}

func (b *LocalBackend) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	destPath := filepath.Join(b.rootPath, key)

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Atomic write: write to temp file then rename
	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	_, err = io.Copy(f, reader)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write file: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to finalize file: %w", err)
	}

	return nil
}

func (b *LocalBackend) Close() error {
	return nil
}
