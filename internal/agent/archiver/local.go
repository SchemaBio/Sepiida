package archiver

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/agent/pathsafe"
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
	realBase, err := pathsafe.RealPath(abs)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local archive path: %w", err)
	}

	return &LocalBackend{baseDir: realBase}, nil
}

func (b *LocalBackend) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dst, cleanKey, err := b.resolveKey(key)
	if err != nil {
		return err
	}

	if err := b.ensureRelativeDir(filepath.Dir(cleanKey)); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}
	if _, err := pathsafe.ResolveExistingWithin(b.baseDir, filepath.Dir(dst)); err != nil {
		return fmt.Errorf("archive directory escapes base directory: %w", err)
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

func (b *LocalBackend) resolveKey(key string) (string, string, error) {
	if _, err := validateObjectKey("archive key", key); err != nil {
		return "", "", err
	}
	key = strings.TrimPrefix(filepath.ToSlash(key), "/")
	cleanKey := filepath.Clean(filepath.FromSlash(key))
	if cleanKey == "." || strings.HasPrefix(cleanKey, ".."+string(os.PathSeparator)) || cleanKey == ".." || filepath.IsAbs(cleanKey) {
		return "", "", fmt.Errorf("invalid archive key %q", key)
	}

	dst := filepath.Join(b.baseDir, cleanKey)
	rel, err := filepath.Rel(b.baseDir, dst)
	if err != nil {
		return "", "", fmt.Errorf("failed to validate archive key: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("archive key escapes base directory: %q", key)
	}
	return dst, cleanKey, nil
}

func (b *LocalBackend) ensureRelativeDir(relDir string) error {
	if relDir == "." || relDir == "" {
		return nil
	}
	if filepath.IsAbs(relDir) || relDir == ".." || strings.HasPrefix(relDir, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("invalid archive directory %q", relDir)
	}

	current := b.baseDir
	for _, segment := range strings.Split(relDir, string(os.PathSeparator)) {
		if segment == "" || segment == "." {
			continue
		}
		if segment == ".." {
			return fmt.Errorf("invalid archive directory segment %q", segment)
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to use symlink archive directory: %s", current)
			}
			if !info.IsDir() {
				return fmt.Errorf("archive path component is not a directory: %s", current)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.Mkdir(current, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
		if info, err := os.Lstat(current); err != nil {
			return err
		} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("archive path component is not a safe directory: %s", current)
		}
	}
	return nil
}
