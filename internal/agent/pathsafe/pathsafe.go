package pathsafe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsAbsoluteLocalPath reports whether s looks like an absolute filesystem path.
func IsAbsoluteLocalPath(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && !strings.Contains(s, "://") && filepath.IsAbs(s)
}

// RealPath returns the absolute, symlink-resolved path for an existing path.
func RealPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(realPath), nil
}

// ResolveExistingWithin resolves candidate and verifies that both the requested
// path and its real symlink target stay inside root.
func ResolveExistingWithin(root string, candidate string) (string, error) {
	rootReal, err := RealPath(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}

	candidatePath := candidate
	if !filepath.IsAbs(candidatePath) {
		candidatePath = filepath.Join(rootReal, candidatePath)
	}

	candidateAbs, err := filepath.Abs(candidatePath)
	if err != nil {
		return "", err
	}
	candidateAbs = filepath.Clean(candidateAbs)

	if ok, err := Within(rootReal, candidateAbs); err != nil || !ok {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("path %q escapes root %q", candidate, root)
	}

	realPath, err := filepath.EvalSymlinks(candidateAbs)
	if err != nil {
		return "", err
	}
	realPath = filepath.Clean(realPath)

	if ok, err := Within(rootReal, realPath); err != nil || !ok {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("path %q resolves outside root %q", candidate, root)
	}

	return realPath, nil
}

// Within reports whether target is root or below root.
func Within(root string, target string) (bool, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false, err
	}

	rel, err := filepath.Rel(filepath.Clean(rootAbs), filepath.Clean(targetAbs))
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return false, nil
	}
	return true, nil
}
