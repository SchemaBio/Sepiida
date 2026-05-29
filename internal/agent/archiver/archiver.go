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

	"github.com/SchemaBio/Sepiida/internal/common/model"
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
//   - oss://region/bucket/prefix → OSSBackend (Alibaba Cloud OSS)
//   - https://<bucket>.oss-<region>.aliyuncs.com/prefix → OSSBackend (Alibaba Cloud OSS)
//   - cos://region/bucket/prefix → COSBackend (Tencent Cloud COS)
//   - https://<bucket>.cos.<region>.myqcloud.com/prefix → COSBackend (Tencent Cloud COS)
//   - http(s)://host:port/bucket/prefix → S3Backend (MinIO)
func NewFromPath(archivePath string, accessKeyID string, secretAccessKey string) (*Archiver, error) {
	var backend Backend
	var err error

	if isCOSURL(archivePath) {
		backend, err = NewCOSBackend(archivePath, accessKeyID, secretAccessKey)
	} else if isOSSURL(archivePath) {
		backend, err = NewOSSBackend(archivePath, accessKeyID, secretAccessKey)
	} else if isS3URL(archivePath) {
		backend, err = NewS3Backend(archivePath, accessKeyID, secretAccessKey)
	} else {
		backend, err = NewLocalBackend(archivePath)
	}

	if err != nil {
		return nil, err
	}

	return NewArchiver(backend), nil
}

// isCOSURL checks if the path refers to Tencent Cloud COS.
func isCOSURL(path string) bool {
	if strings.HasPrefix(path, "cos://") {
		return true
	}
	if strings.HasPrefix(path, "https://") {
		host := strings.SplitN(strings.TrimPrefix(path, "https://"), "/", 2)[0]
		if strings.Contains(host, ".cos.") && strings.HasSuffix(host, ".myqcloud.com") {
			return true
		}
	}
	return false
}

// isOSSURL checks if the path refers to Alibaba Cloud OSS.
func isOSSURL(path string) bool {
	if strings.HasPrefix(path, "oss://") {
		return true
	}
	if strings.HasPrefix(path, "https://") {
		host := strings.SplitN(strings.TrimPrefix(path, "https://"), "/", 2)[0]
		if strings.Contains(host, ".oss-") && strings.HasSuffix(host, ".aliyuncs.com") {
			return true
		}
	}
	return false
}

// isS3URL checks if the path refers to S3-compatible storage (excluding COS and OSS).
func isS3URL(path string) bool {
	if strings.HasPrefix(path, "s3://") {
		return true
	}
	if strings.HasPrefix(path, "http://") {
		return true
	}
	if strings.HasPrefix(path, "https://") && !isCOSURL(path) && !isOSSURL(path) {
		return true
	}
	return false
}

// Archive archives a completed workflow's outputs.
// It archives inputs.json, workflow.log, and outputs.json as-is,
// then resolves outputs.json (recursive file references + symlinks) to find
// real output files. Text files (.txt, .csv, .tsv) are converted to individual Parquet
// files with dynamic schema based on header row; other files are archived individually
// with flattened names (basename only). Finally, a rewritten outputs.json with paths
// pointing to the archive location is uploaded.
func (a *Archiver) Archive(ctx context.Context, uuid string, executionDir string) (*model.ArchiveResult, error) {
	return a.ArchiveWorkflow(ctx, uuid, "", executionDir)
}

// ArchiveWorkflow archives a completed workflow and records the concrete
// workflow ID in the result so the server can update the correct execution.
func (a *Archiver) ArchiveWorkflow(ctx context.Context, uuid string, workflowID string, executionDir string) (*model.ArchiveResult, error) {
	archived := 0
	result := &model.ArchiveResult{
		UUID:               uuid,
		WorkflowID:         workflowID,
		ArchiveBase:        a.backend.BasePath(),
		BasePath:           a.backend.BasePath(),
		OutputsResolvedKey: uuid + "/outputs.resolved.json",
		ObjectPrefix:       uuid,
		KeyPrefix:          uuid,
	}

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

	// 4. Resolve outputs.json (recursive tmp file resolution only, preserve original paths)
	resolvedJSON, pathMap, err := resolveOutputs(outputsPath)
	if err != nil {
		log.Printf("Warning: failed to resolve outputs.json for %s: %v", uuid, err)
		result.ArchivedCount = archived
		return result, nil
	}

	log.Printf("Archive: resolved %d files for UUID %s, execDir=%s", len(pathMap), uuid, executionDir)

	// Collect text files (before modifying pathMap)
	var textFiles []string
	for origPath := range pathMap {
		if isTextFile(origPath) {
			textFiles = append(textFiles, origPath)
		}
	}

	// Update pathMap: convert text file paths to .parquet extensions
	// This ensures outputs.resolved.json references .parquet files instead of .txt
	for origPath, archiveKey := range pathMap {
		if isTextFile(origPath) {
			// Replace extension with .parquet
			ext := filepath.Ext(archiveKey)
			base := strings.TrimSuffix(archiveKey, ext)
			pathMap[origPath] = base + ".parquet"
		}
	}

	// Prepend UUID to all archive keys
	for origPath, relKey := range pathMap {
		pathMap[origPath] = uuid + "/" + relKey
	}

	for origPath, archiveKey := range pathMap {
		log.Printf("Archive: pathMap[%s] -> %s", origPath, archiveKey)
	}

	// 5. Archive non-text files individually (resolve symlinks to get real file for reading)
	// Text files will be converted to Parquet instead
	for origPath, archiveKey := range pathMap {
		// Skip text files - they will be converted to parquet
		if isTextFile(origPath) {
			continue
		}

		realPath, err := filepath.EvalSymlinks(origPath)
		if err != nil {
			log.Printf("Warning: failed to resolve symlink %s: %v", origPath, err)
			continue
		}
		log.Printf("Archive: uploading %s (real: %s) -> key=%s", origPath, realPath, archiveKey)
		if err := a.archiveFileByKey(ctx, archiveKey, realPath); err != nil {
			log.Printf("Warning: failed to archive %s for %s: %v", origPath, uuid, err)
			continue
		}
		archived++
	}

	// 6. Convert each text file to individual Parquet file with dynamic schema
	for _, textFile := range textFiles {
		parquetKey := pathMap[textFile]
		if err := a.archiveSingleParquet(ctx, textFile, parquetKey); err != nil {
			log.Printf("Warning: failed to archive parquet for %s: %v", textFile, err)
			continue
		}
		archived++
		log.Printf("Converted %s to parquet -> key=%s", filepath.Base(textFile), parquetKey)
	}

	// 7. Upload rewritten outputs.json with archive paths
	if err := a.uploadRewrittenOutputs(ctx, uuid, resolvedJSON, pathMap); err != nil {
		log.Printf("Warning: failed to upload rewritten outputs.json for %s: %v", uuid, err)
	} else {
		archived++
	}

	log.Printf("Archived %d items for UUID %s", archived, uuid)
	result.ArchivedCount = archived
	return result, nil
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

// resolveOutputs reads outputs.json and recursively resolves JSON file references
// (tmp files). Symlinks are NOT resolved here — original paths are preserved so
// that archive keys (relative to execDir) can be computed correctly.
// Returns the resolved JSON structure and a map from original path → archive key.
func resolveOutputs(outputsPath string) (interface{}, map[string]string, error) {
	data, err := os.ReadFile(outputsPath)
	if err != nil {
		return nil, nil, err
	}

	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}

	// Only resolve JSON tmp files, preserve original symlink paths
	resolved := resolveJSONFiles(raw)

	// Collect file paths and compute flattened archive keys (basename only)
	pathMap := make(map[string]string)
	execDir := filepath.Dir(outputsPath)
	collectPathsFlat(resolved, execDir, pathMap)

	return resolved, pathMap, nil
}

// resolveJSONFiles recursively resolves a JSON value: if a string is a file path
// whose content is valid JSON, read and replace with the parsed content.
// Does NOT resolve symlinks — original paths are preserved.
func resolveJSONFiles(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return resolveJSONFile(val)
	case map[string]interface{}:
		for k, child := range val {
			val[k] = resolveJSONFiles(child)
		}
		return val
	case []interface{}:
		for i, child := range val {
			val[i] = resolveJSONFiles(child)
		}
		return val
	default:
		return v
	}
}

// resolveJSONFile checks if a string is a path to a JSON file and resolves it.
// Symlinks are resolved only to read the file content; the original path string
// is returned if the file is not JSON.
func resolveJSONFile(s string) interface{} {
	if len(s) == 0 || s[0] != '/' || strings.Contains(s, "://") {
		return s
	}

	// Need to resolve symlinks to check if the file exists and read it
	realPath, err := filepath.EvalSymlinks(s)
	if err != nil {
		return s
	}

	info, err := os.Stat(realPath)
	if err != nil || info.IsDir() {
		return s
	}

	data, err := os.ReadFile(realPath)
	if err != nil {
		return s
	}

	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return s // Not JSON — return original path
	}

	// It's a JSON file — recursively resolve its contents
	log.Printf("Resolved outputs reference: %s", s)
	return resolveJSONFiles(parsed)
}

// collectPathsFlat collects all file paths from the resolved JSON structure,
// then generates flattened archive keys (using basename only) with conflict resolution.
// Text files (txt/csv/tsv) are included in pathMap but will not be uploaded individually.
func collectPathsFlat(v interface{}, execDir string, pathMap map[string]string) {
	// Step 1: Collect all valid file paths
	var allPaths []string
	collectAllPaths(v, &allPaths)

	// Step 2: Generate flattened archive keys with conflict resolution
	usedKeys := make(map[string]int)
	for _, path := range allPaths {
		filename := filepath.Base(path)
		archiveKey := filename

		// Handle filename conflicts by adding sequence number
		if count, exists := usedKeys[archiveKey]; exists {
			ext := filepath.Ext(archiveKey)
			base := strings.TrimSuffix(archiveKey, ext)
			archiveKey = fmt.Sprintf("%s_%d%s", base, count+1, ext)
		}
		usedKeys[archiveKey]++

		pathMap[path] = archiveKey
	}
}

// collectAllPaths recursively collects all valid file paths from the JSON structure.
func collectAllPaths(v interface{}, paths *[]string) {
	switch val := v.(type) {
	case string:
		if len(val) == 0 || val[0] != '/' || strings.Contains(val, "://") {
			return
		}
		// Check that the file actually exists (possibly via symlink)
		if _, err := os.Stat(val); err != nil {
			return
		}
		*paths = append(*paths, val)
	case map[string]interface{}:
		for _, child := range val {
			collectAllPaths(child, paths)
		}
	case []interface{}:
		for _, child := range val {
			collectAllPaths(child, paths)
		}
	}
}

// rewritePaths recursively rewrites file path strings in the JSON structure
// to point to the archive location. The pathMap uses original paths as keys.
func rewritePaths(v interface{}, pathMap map[string]string, basePath string) interface{} {
	switch val := v.(type) {
	case string:
		if len(val) == 0 || val[0] != '/' || strings.Contains(val, "://") {
			return val
		}
		if key, ok := pathMap[val]; ok {
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

// archiveSingleParquet converts a single text file to Parquet and uploads it.
// The Parquet schema is dynamically generated from the file's header row.
func (a *Archiver) archiveSingleParquet(ctx context.Context, textFilePath string, parquetKey string) error {
	parquetData, columns, err := buildSingleFileParquet(textFilePath)
	if err != nil {
		return fmt.Errorf("failed to build parquet: %w", err)
	}

	log.Printf("Parquet schema for %s: columns=%v", filepath.Base(textFilePath), columns)
	return a.backend.Upload(ctx, parquetKey, bytes.NewReader(parquetData), int64(len(parquetData)))
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
