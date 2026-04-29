package archiver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Archiver archives workflow outputs to a configurable destination.
type Archiver struct {
	backend Backend
}

// NewArchiver creates a new archiver with the given backend.
func NewArchiver(backend Backend) *Archiver {
	return &Archiver{backend: backend}
}

// NewFromPath creates an Archiver by auto-detecting the backend from the path.
// If accessKeyID and secretAccessKey are non-empty, they are used for S3-compatible
// storage instead of reading from environment variables.
//   - Local path (e.g., /mnt/archive/) → LocalBackend
//   - s3://bucket/prefix → S3Backend (AWS S3)
//   - oss://region/bucket/prefix → S3Backend (Alibaba Cloud OSS)
//   - cos://region/bucket/prefix → S3Backend (Tencent Cloud COS, short URL)
//   - https://<bucket>.cos.<region>.myqcloud.com/prefix → S3Backend (Tencent Cloud COS, virtual-hosted)
//   - http(s)://host:port/bucket/prefix → S3Backend (MinIO)
func NewFromPath(archivePath string, accessKeyID string, secretAccessKey string) (*Archiver, error) {
	var backend Backend
	var err error

	if isS3URL(archivePath) {
		backend, err = NewS3Backend(archivePath, accessKeyID, secretAccessKey)
	} else {
		backend, err = NewLocalBackend(archivePath)
	}

	if err != nil {
		return nil, err
	}

	return NewArchiver(backend), nil
}

// isS3URL checks if the path refers to S3-compatible storage.
func isS3URL(path string) bool {
	return strings.HasPrefix(path, "s3://") ||
		strings.HasPrefix(path, "oss://") ||
		strings.HasPrefix(path, "cos://") ||
		strings.HasPrefix(path, "http://") ||
		strings.HasPrefix(path, "https://")
}

// Archive archives a completed workflow's outputs.
// It archives inputs.json, workflow.log, and outputs.json as-is,
// then resolves outputs.json (recursive file references + symlinks) to find
// real output files. Text files (.txt, .csv) are consolidated into a Parquet
// file; other files are archived individually. Finally, a rewritten outputs.json
// with paths pointing to the archive location is uploaded.
func (a *Archiver) Archive(ctx context.Context, uuid string, executionDir string) (int, error) {
	archived := 0

	// 1. Archive workflow.log
	logPath := filepath.Join(executionDir, "workflow.log")
	if err := a.archiveFile(ctx, uuid, executionDir, logPath); err != nil {
		log.Printf("Warning: failed to archive workflow.log for %s: %v", uuid, err)
	} else {
		archived++
	}

	// 2. Archive original outputs.json
	outputsPath := filepath.Join(executionDir, "outputs.json")
	if err := a.archiveFile(ctx, uuid, executionDir, outputsPath); err != nil {
		log.Printf("Warning: failed to archive outputs.json for %s: %v", uuid, err)
	} else {
		archived++
	}

	// 3. Archive inputs.json
	inputsPath := filepath.Join(executionDir, "inputs.json")
	if err := a.archiveFile(ctx, uuid, executionDir, inputsPath); err != nil {
		log.Printf("Warning: failed to archive inputs.json for %s: %v", uuid, err)
	} else {
		archived++
	}

	// 4. Resolve outputs.json and archive real files
	resolvedJSON, pathMap, err := resolveOutputs(outputsPath)
	if err != nil {
		log.Printf("Warning: failed to resolve outputs.json for %s: %v", uuid, err)
		return archived, nil
	}

	var textFiles []string
	for realPath := range pathMap {
		if isTextFile(realPath) {
			textFiles = append(textFiles, realPath)
		}
	}

	// 5. Archive all files individually
	for realPath := range pathMap {
		archiveKey := pathMap[realPath]
		if err := a.archiveFileByKey(ctx, archiveKey, realPath); err != nil {
			log.Printf("Warning: failed to archive %s for %s: %v", realPath, uuid, err)
			continue
		}
		archived++
	}

	// 6. Consolidate text files into a single Parquet file (in addition to individual uploads)
	if len(textFiles) > 0 {
		if err := a.archiveTextParquet(ctx, uuid, executionDir, textFiles); err != nil {
			log.Printf("Warning: failed to archive text parquet for %s: %v", uuid, err)
		} else {
			archived++
			log.Printf("Consolidated %d text files into parquet for UUID %s", len(textFiles), uuid)
		}
	}

	// 7. Upload rewritten outputs.json with archive paths
	if err := a.uploadRewrittenOutputs(ctx, uuid, resolvedJSON, pathMap); err != nil {
		log.Printf("Warning: failed to upload rewritten outputs.json for %s: %v", uuid, err)
	} else {
		archived++
	}

	log.Printf("Archived %d items for UUID %s", archived, uuid)
	return archived, nil
}

// archiveFileByKey uploads a local file to a specific archive key.
func (a *Archiver) archiveFileByKey(ctx context.Context, key string, filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("file not found: %w", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	return a.backend.Upload(ctx, key, f, info.Size())
}

// uploadRewrittenOutputs builds and uploads an outputs.json where all file paths
// are rewritten to point to the archive location.
func (a *Archiver) uploadRewrittenOutputs(ctx context.Context, uuid string, resolvedJSON interface{}, pathMap map[string]string) error {
	basePath := a.backend.BasePath()
	rewritten := rewritePaths(resolvedJSON, pathMap, basePath)

	data, err := json.MarshalIndent(rewritten, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal rewritten outputs.json: %w", err)
	}

	key := uuid + "/outputs.resolved.json"
	return a.backend.Upload(ctx, key, bytes.NewReader(data), int64(len(data)))
}

// resolveOutputs reads outputs.json and recursively resolves file references
// (tmp JSON files) and symlinks. Returns the resolved JSON structure and a map
// from real file path → archive key (uuid/relPath, but uuid is added later).
func resolveOutputs(outputsPath string) (interface{}, map[string]string, error) {
	data, err := os.ReadFile(outputsPath)
	if err != nil {
		return nil, nil, err
	}

	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}

	// Recursive resolve: tmp files + symlinks
	resolved := resolveValue(raw)

	// Collect real file paths and compute archive keys
	pathMap := make(map[string]string)
	execDir := filepath.Dir(outputsPath)
	collectPaths(resolved, execDir, pathMap)

	return resolved, pathMap, nil
}

// resolveValue recursively resolves a JSON value: if a string is a file path
// to a JSON file, read and parse it; if it's a symlink, resolve to real path.
func resolveValue(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return resolveFilePath(val)
	case map[string]interface{}:
		for k, child := range val {
			val[k] = resolveValue(child)
		}
		return val
	case []interface{}:
		for i, child := range val {
			val[i] = resolveValue(child)
		}
		return val
	default:
		return v
	}
}

// resolveFilePath resolves a single string value: symlinks first, then
// recursively reads JSON file content.
func resolveFilePath(s string) interface{} {
	if len(s) == 0 || s[0] != '/' || strings.Contains(s, "://") {
		return s
	}

	// Resolve symlinks
	realPath, err := filepath.EvalSymlinks(s)
	if err != nil {
		return s
	}
	realPath = filepath.Clean(realPath)

	info, err := os.Stat(realPath)
	if err != nil || info.IsDir() {
		return realPath
	}

	// Try reading as JSON
	data, err := os.ReadFile(realPath)
	if err != nil {
		return realPath
	}

	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return realPath
	}

	// It's a JSON file — recursively resolve its contents
	return resolveValue(parsed)
}

// collectPaths walks the resolved JSON and collects file paths that exist on disk,
// computing their archive key as uuid-relative path from execDir.
func collectPaths(v interface{}, execDir string, pathMap map[string]string) {
	switch val := v.(type) {
	case string:
		if len(val) > 0 && val[0] != '/' && !strings.Contains(val, "://") {
			return
		}
		realPath, err := filepath.EvalSymlinks(val)
		if err != nil {
			return
		}
		info, err := os.Stat(realPath)
		if err != nil || info.IsDir() {
			return
		}
		// Compute key: use original path relative to execDir for the archive key
		relPath, err := filepath.Rel(execDir, val)
		if err != nil {
			relPath = filepath.Base(val)
		}
		pathMap[realPath] = filepath.ToSlash(relPath)
	case map[string]interface{}:
		for _, child := range val {
			collectPaths(child, execDir, pathMap)
		}
	case []interface{}:
		for _, child := range val {
			collectPaths(child, execDir, pathMap)
		}
	}
}

// rewritePaths recursively rewrites file path strings in the JSON structure
// to point to the archive location.
func rewritePaths(v interface{}, pathMap map[string]string, basePath string) interface{} {
	switch val := v.(type) {
	case string:
		// Check if this string is a file we archived
		if len(val) > 0 && val[0] != '/' && !strings.Contains(val, "://") {
			return val
		}
		realPath, err := filepath.EvalSymlinks(val)
		if err != nil {
			return val
		}
		realPath = filepath.Clean(realPath)
		if key, ok := pathMap[realPath]; ok {
			return basePath + "/" + key
		}
		return val
	case map[string]interface{}:
		for k, child := range val {
			val[k] = rewritePaths(child, pathMap, basePath)
		}
		return val
	case []interface{}:
		for i, child := range val {
			val[i] = rewritePaths(child, pathMap, basePath)
		}
		return val
	default:
		return v
	}
}

// archiveTextParquet builds a Parquet file from text files and uploads it.
func (a *Archiver) archiveTextParquet(ctx context.Context, uuid string, executionDir string, textFiles []string) error {
	data, err := buildParquetData(executionDir, textFiles)
	if err != nil {
		return err
	}

	key := uuid + "/outputs.parquet"
	return a.backend.Upload(ctx, key, bytes.NewReader(data), int64(len(data)))
}

// archiveFile uploads a single file, preserving its path relative to executionDir.
func (a *Archiver) archiveFile(ctx context.Context, uuid string, executionDir string, filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("file not found: %w", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	relPath, err := relativePath(executionDir, filePath)
	if err != nil {
		relPath = filepath.Base(filePath)
	}

	key := uuid + "/" + relPath
	return a.backend.Upload(ctx, key, f, info.Size())
}

// Close releases resources held by the archiver.
func (a *Archiver) Close() error {
	return a.backend.Close()
}

