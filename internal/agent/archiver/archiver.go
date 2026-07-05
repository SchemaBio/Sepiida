package archiver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/agent/pathsafe"
	"github.com/SchemaBio/Sepiida/internal/common/model"
)

// Archiver archives workflow outputs to a configurable destination.
type Archiver struct {
	backend Backend
}

const (
	maxArchiveManifestBytes = 10 << 20 // workflow inputs/outputs JSON manifests
	maxNestedJSONBytes      = 1 << 20  // nested JSON references inside outputs
	maxJSONResolveDepth     = 32
)

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

	if err := validateArchivePath(archivePath); err != nil {
		return nil, err
	}
	archivePath = normalizeArchiveURLScheme(archivePath)

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

func validateArchivePath(archivePath string) error {
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" {
		return fmt.Errorf("archive path is required")
	}
	if strings.ContainsFunc(archivePath, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("archive path contains control characters")
	}
	if strings.HasPrefix(archivePath, "//") || strings.HasPrefix(archivePath, `\\`) {
		return fmt.Errorf("archive path must not be scheme-relative or UNC-style; use an explicit local path or supported URL scheme")
	}
	if !strings.Contains(archivePath, "://") {
		return nil
	}

	u, err := url.Parse(archivePath)
	if err != nil {
		return fmt.Errorf("invalid archive URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("archive URL must include scheme and host")
	}
	if u.User != nil {
		return fmt.Errorf("archive URL must not include username or password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("archive URL must not include query string or fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "s3", "http", "https", "oss", "cos":
		return nil
	default:
		return fmt.Errorf("unsupported archive URL scheme %q", u.Scheme)
	}
}

func normalizeArchiveURLScheme(archivePath string) string {
	archivePath = strings.TrimSpace(archivePath)
	if !strings.Contains(archivePath, "://") {
		return archivePath
	}
	u, err := url.Parse(archivePath)
	if err != nil || u.Scheme == "" {
		return archivePath
	}
	u.Scheme = strings.ToLower(u.Scheme)
	return u.String()
}

// isCOSURL checks if the path refers to Tencent Cloud COS.
func isCOSURL(path string) bool {
	u, err := url.Parse(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "cos" {
		return true
	}
	if scheme == "https" {
		host := strings.ToLower(u.Hostname())
		if strings.Contains(host, ".cos.") && strings.HasSuffix(host, ".myqcloud.com") {
			return true
		}
	}
	return false
}

// isOSSURL checks if the path refers to Alibaba Cloud OSS.
func isOSSURL(path string) bool {
	u, err := url.Parse(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "oss" {
		return true
	}
	if scheme == "https" {
		host := strings.ToLower(u.Hostname())
		if strings.Contains(host, ".oss-") && strings.HasSuffix(host, ".aliyuncs.com") {
			return true
		}
	}
	return false
}

// isS3URL checks if the path refers to S3-compatible storage (excluding COS and OSS).
func isS3URL(path string) bool {
	u, err := url.Parse(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "s3" {
		return true
	}
	if scheme == "http" {
		return true
	}
	if scheme == "https" && !isCOSURL(path) && !isOSSURL(path) {
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
	var archiveErrs []error
	result := &model.ArchiveResult{
		UUID:               uuid,
		WorkflowID:         workflowID,
		ArchiveBase:        a.backend.BasePath(),
		BasePath:           a.backend.BasePath(),
		OutputsResolvedKey: uuid + "/outputs.resolved.json",
		ObjectPrefix:       uuid,
		KeyPrefix:          uuid,
	}

	executionDir, err := pathsafe.RealPath(executionDir)
	if err != nil {
		return result, fmt.Errorf("invalid execution directory: %w", err)
	}

	// 1. Archive workflow.log
	logPath := filepath.Join(executionDir, "workflow.log")
	if err := a.archiveFile(ctx, uuid, executionDir, logPath); err != nil {
		log.Printf("Warning: failed to archive workflow.log for %s: %v", uuid, err)
		archiveErrs = append(archiveErrs, fmt.Errorf("archive workflow.log: %w", err))
	} else {
		archived++
	}

	// 2. Archive original outputs.json
	outputsPath := filepath.Join(executionDir, "outputs.json")
	if err := a.archiveFile(ctx, uuid, executionDir, outputsPath); err != nil {
		log.Printf("Warning: failed to archive outputs.json for %s: %v", uuid, err)
		archiveErrs = append(archiveErrs, fmt.Errorf("archive outputs.json: %w", err))
	} else {
		archived++
	}

	// 3. Archive inputs.json
	inputsPath := filepath.Join(executionDir, "inputs.json")
	if err := a.archiveFile(ctx, uuid, executionDir, inputsPath); err != nil {
		log.Printf("Warning: failed to archive inputs.json for %s: %v", uuid, err)
		archiveErrs = append(archiveErrs, fmt.Errorf("archive inputs.json: %w", err))
	} else {
		archived++
	}

	// 4. Resolve outputs.json (recursive tmp file resolution only, preserve original paths)
	resolvedJSON, pathMap, err := resolveOutputs(outputsPath, executionDir)
	if err != nil {
		result.ArchivedCount = archived
		return result, fmt.Errorf("resolve outputs.json: %w", err)
	}

	log.Printf("Archive: resolved %d files for UUID %s, execDir=%s", len(pathMap), uuid, executionDir)

	// Collect text files (before modifying pathMap)
	var textFiles []string
	for origPath := range pathMap {
		if isTextFile(origPath) {
			textFiles = append(textFiles, origPath)
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

		realPath, err := pathsafe.ResolveExistingWithin(executionDir, origPath)
		if err != nil {
			log.Printf("Warning: failed to resolve symlink %s: %v", origPath, err)
			continue
		}
		log.Printf("Archive: uploading %s (real: %s) -> key=%s", origPath, realPath, archiveKey)
		if err := a.archiveFileByKey(ctx, archiveKey, realPath); err != nil {
			log.Printf("Warning: failed to archive %s for %s: %v", origPath, uuid, err)
			archiveErrs = append(archiveErrs, fmt.Errorf("archive output %s: %w", origPath, err))
			continue
		}
		archived++
	}

	// 6. Convert each text file to individual Parquet file with dynamic schema.
	// If conversion cannot safely produce Parquet (for example header-only or
	// malformed tabular output), archive the original text file and keep the
	// rewritten manifest pointing to that uploaded object. This avoids marking an
	// archive complete while outputs.resolved.json references a missing parquet.
	for _, textFile := range textFiles {
		originalKey := pathMap[textFile]
		ext := filepath.Ext(originalKey)
		parquetKey := strings.TrimSuffix(originalKey, ext) + ".parquet"
		realPath, err := pathsafe.ResolveExistingWithin(executionDir, textFile)
		if err != nil {
			log.Printf("Warning: failed to resolve text file %s: %v", textFile, err)
			archiveErrs = append(archiveErrs, fmt.Errorf("resolve text output %s: %w", textFile, err))
			continue
		}
		if err := a.archiveSingleParquet(ctx, realPath, parquetKey); err != nil {
			log.Printf("Warning: failed to convert %s to parquet, archiving original text instead: %v", textFile, err)
			if uploadErr := a.archiveFileByKey(ctx, originalKey, realPath); uploadErr != nil {
				archiveErrs = append(archiveErrs, fmt.Errorf("archive text output %s: parquet conversion failed (%v), fallback upload failed: %w", textFile, err, uploadErr))
				continue
			}
			archived++
			continue
		}
		pathMap[textFile] = parquetKey
		archived++
		log.Printf("Converted %s to parquet -> key=%s", filepath.Base(textFile), parquetKey)
	}

	if err := errors.Join(archiveErrs...); err != nil {
		result.ArchivedCount = archived
		return result, err
	}

	// 7. Upload rewritten outputs.json with archive paths
	if err := a.uploadRewrittenOutputs(ctx, uuid, resolvedJSON, pathMap); err != nil {
		result.ArchivedCount = archived
		return result, fmt.Errorf("upload rewritten outputs.json: %w", err)
	}
	archived++

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
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", filePath)
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
func resolveOutputs(outputsPath string, executionDir string) (interface{}, map[string]string, error) {
	outputsRealPath, err := pathsafe.ResolveExistingWithin(executionDir, outputsPath)
	if err != nil {
		return nil, nil, err
	}

	data, err := readRegularFile(outputsRealPath, maxArchiveManifestBytes)
	if err != nil {
		return nil, nil, err
	}

	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}

	// Only resolve JSON tmp files, preserve original symlink paths
	resolved := resolveJSONFiles(raw, executionDir)

	// Collect file paths and compute flattened archive keys (basename only)
	pathMap := make(map[string]string)
	collectPathsFlat(resolved, executionDir, pathMap)

	return resolved, pathMap, nil
}

// resolveJSONFiles recursively resolves a JSON value: if a string is a file path
// whose content is valid JSON, read and replace with the parsed content.
// Does NOT resolve symlinks — original paths are preserved.
func resolveJSONFiles(v interface{}, executionDir string) interface{} {
	return resolveJSONFilesWithState(v, executionDir, 0, make(map[string]struct{}))
}

func resolveJSONFilesWithState(v interface{}, executionDir string, depth int, seen map[string]struct{}) interface{} {
	if depth > maxJSONResolveDepth {
		return v
	}
	switch val := v.(type) {
	case string:
		return resolveJSONFile(val, executionDir, depth, seen)
	case map[string]interface{}:
		for k, child := range val {
			val[k] = resolveJSONFilesWithState(child, executionDir, depth+1, seen)
		}
		return val
	case []interface{}:
		for i, child := range val {
			val[i] = resolveJSONFilesWithState(child, executionDir, depth+1, seen)
		}
		return val
	default:
		return v
	}
}

// resolveJSONFile checks if a string is a path to a JSON file and resolves it.
// Symlinks are resolved only to read the file content; the original path string
// is returned if the file is not JSON.
func resolveJSONFile(s string, executionDir string, depth int, seen map[string]struct{}) interface{} {
	if !pathsafe.IsAbsoluteLocalPath(s) {
		return s
	}

	realPath, err := pathsafe.ResolveExistingWithin(executionDir, s)
	if err != nil {
		return s
	}

	info, err := os.Stat(realPath)
	if err != nil || info.IsDir() {
		return s
	}

	data, err := readRegularFile(realPath, maxNestedJSONBytes)
	if err != nil {
		return s
	}

	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return s // Not JSON — return original path
	}

	// It's a JSON file — recursively resolve its contents
	if _, ok := seen[realPath]; ok {
		return s
	}
	seen[realPath] = struct{}{}
	defer delete(seen, realPath)

	log.Printf("Resolved outputs reference: %s", s)
	return resolveJSONFilesWithState(parsed, executionDir, depth+1, seen)
}

// collectPathsFlat collects all file paths from the resolved JSON structure,
// then generates flattened archive keys (using basename only) with conflict resolution.
// Text files (txt/csv/tsv) are included in pathMap but will not be uploaded individually.
func collectPathsFlat(v interface{}, execDir string, pathMap map[string]string) {
	// Step 1: Collect all valid file paths
	var allPaths []string
	collectAllPaths(v, execDir, &allPaths)
	allPaths = uniqueStringsPreserveOrder(allPaths)

	// Step 2: Generate flattened archive keys with conflict resolution
	reservedBasenames := make(map[string]struct{}, len(allPaths))
	for _, path := range allPaths {
		reservedBasenames[filepath.Base(path)] = struct{}{}
	}

	usedKeys := make(map[string]struct{})
	nextSuffix := make(map[string]int)
	for _, path := range allPaths {
		filename := filepath.Base(path)
		archiveKey := filename

		// Handle filename conflicts by adding sequence numbers. Track the
		// generated archive keys separately so three files with the same basename
		// do not all collapse to *_2.ext, and avoid colliding with an existing
		// real basename such as result_2.bam.
		if _, exists := usedKeys[archiveKey]; exists {
			ext := filepath.Ext(archiveKey)
			base := strings.TrimSuffix(archiveKey, ext)
			suffix := nextSuffix[filename]
			if suffix < 2 {
				suffix = 2
			}
			for {
				candidate := fmt.Sprintf("%s_%d%s", base, suffix, ext)
				suffix++
				_, used := usedKeys[candidate]
				_, reserved := reservedBasenames[candidate]
				if !used && !reserved {
					archiveKey = candidate
					break
				}
			}
			nextSuffix[filename] = suffix
		} else {
			nextSuffix[filename] = 2
		}
		usedKeys[archiveKey] = struct{}{}

		pathMap[path] = archiveKey
	}
}

func uniqueStringsPreserveOrder(values []string) []string {
	if len(values) == 0 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

// collectAllPaths recursively collects all valid file paths from the JSON structure.
func collectAllPaths(v interface{}, execDir string, paths *[]string) {
	switch val := v.(type) {
	case string:
		if !pathsafe.IsAbsoluteLocalPath(val) {
			return
		}
		realPath, err := pathsafe.ResolveExistingWithin(execDir, val)
		if err != nil {
			return
		}
		info, err := os.Stat(realPath)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		*paths = append(*paths, val)
	case map[string]interface{}:
		for _, child := range val {
			collectAllPaths(child, execDir, paths)
		}
	case []interface{}:
		for _, child := range val {
			collectAllPaths(child, execDir, paths)
		}
	}
}

// rewritePaths recursively rewrites file path strings in the JSON structure
// to point to the archive location. The pathMap uses original paths as keys.
func rewritePaths(v interface{}, pathMap map[string]string, basePath string) interface{} {
	switch val := v.(type) {
	case string:
		if !pathsafe.IsAbsoluteLocalPath(val) {
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
	realPath, err := pathsafe.ResolveExistingWithin(executionDir, filePath)
	if err != nil {
		return fmt.Errorf("invalid archive path: %w", err)
	}

	info, err := os.Stat(realPath)
	if err != nil {
		return fmt.Errorf("file not found: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", realPath)
	}

	f, err := os.Open(realPath)
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

func readRegularFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, fmt.Errorf("file too large: %s", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	limit := maxBytes
	if limit <= 0 {
		limit = info.Size()
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file too large: %s", path)
	}
	return data, nil
}

// Close releases resources held by the archiver.
func (a *Archiver) Close() error {
	return a.backend.Close()
}
