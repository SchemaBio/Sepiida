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
	return resolveExisting(abs)
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

	realPath, err := resolveExisting(candidateAbs)
	if err != nil {
		return "", err
	}

	if ok, err := Within(rootReal, realPath); err != nil || !ok {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("path %q resolves outside root %q", candidate, root)
	}

	return realPath, nil
}

// resolveExisting resolves symlinks for the usual case. Some locked-down
// Windows hosts deny the final-path query used by filepath.EvalSymlinks even
// when the process can read every directory entry. In that environment we
// retain the same containment guarantee by checking every existing component
// with Lstat and refusing symlinks before returning the cleaned absolute path.
// If a component cannot be inspected, the operation still fails closed.
func resolveExisting(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(realPath), nil
	}
	if !os.IsPermission(err) {
		return "", err
	}
	if err := rejectSymlinkComponents(abs); err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func rejectSymlinkComponents(abs string) error {
	volume := filepath.VolumeName(abs)
	root := volume + string(os.PathSeparator)
	relative := strings.TrimPrefix(abs, root)
	current := root
	if relative == "" {
		info, err := os.Lstat(filepath.Clean(abs))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to follow symlink: %s", abs)
		}
		return nil
	}
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to follow symlink: %s", current)
		}
	}
	return nil
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
