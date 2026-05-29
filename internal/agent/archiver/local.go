package archiver

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalBackend archives files to a local filesystem directory.
type LocalBackend struct {
	baseDir string
}

// NewLocalBackend creates a local archive backend rooted at baseDir.
func NewLocalBackend(baseDir string) (*LocalBackend, error) {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil, fmt.Errorf("local archive path is required")
	}

	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local archive path: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create local archive path: %w", err)
	}

	return &LocalBackend{baseDir: filepath.Clean(abs)}, nil
}

func (b *LocalBackend) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dst, err := b.resolveKey(key)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".sepiida-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary archive file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, reader); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write archive file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close archive file: %w", err)
	}

	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("failed to move archive file into place: %w", err)
	}
	return nil
}

func (b *LocalBackend) BasePath() string {
	return b.baseDir
}

func (b *LocalBackend) Close() error {
	return nil
}

func (b *LocalBackend) resolveKey(key string) (string, error) {
	key = strings.TrimPrefix(filepath.ToSlash(key), "/")
	cleanKey := filepath.Clean(filepath.FromSlash(key))
	if cleanKey == "." || strings.HasPrefix(cleanKey, ".."+string(os.PathSeparator)) || cleanKey == ".." || filepath.IsAbs(cleanKey) {
		return "", fmt.Errorf("invalid archive key %q", key)
	}

	dst := filepath.Join(b.baseDir, cleanKey)
	rel, err := filepath.Rel(b.baseDir, dst)
	if err != nil {
		return "", fmt.Errorf("failed to validate archive key: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive key escapes base directory: %q", key)
	}
	return dst, nil
}
