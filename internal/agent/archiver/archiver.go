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
// Text files (.txt, .csv) from outputs.json are consolidated into a single Parquet file.
// Other files (bam, bai, vcf.gz, etc.) are archived individually.
// workflow.log and outputs.json are always archived as-is.
func (a *Archiver) Archive(ctx context.Context, uuid string, executionDir string) (int, error) {
	archived := 0

	// 1. Archive workflow.log
	logPath := filepath.Join(executionDir, "workflow.log")
	if err := a.archiveFile(ctx, uuid, executionDir, logPath); err != nil {
		log.Printf("Warning: failed to archive workflow.log for %s: %v", uuid, err)
	} else {
		archived++
	}

	// 2. Archive outputs.json
	outputsPath := filepath.Join(executionDir, "outputs.json")
	if err := a.archiveFile(ctx, uuid, executionDir, outputsPath); err != nil {
		log.Printf("Warning: failed to archive outputs.json for %s: %v", uuid, err)
	} else {
		archived++
	}

	// 3. Parse outputs.json, classify files, and archive
	referencedFiles, err := ExtractFilePaths(outputsPath, executionDir)
	if err != nil {
		log.Printf("Warning: failed to extract file paths from outputs.json for %s: %v", uuid, err)
		return archived, nil
	}

	var textFiles []string
	var binaryFiles []string
	for _, f := range referencedFiles {
		if isTextFile(f) {
			textFiles = append(textFiles, f)
		} else {
			binaryFiles = append(binaryFiles, f)
		}
	}

	// 4. Consolidate text files into a single Parquet file
	if len(textFiles) > 0 {
		if err := a.archiveTextParquet(ctx, uuid, executionDir, textFiles); err != nil {
			log.Printf("Warning: failed to archive text parquet for %s: %v", uuid, err)
		} else {
			archived++
			log.Printf("Consolidated %d text files into parquet for UUID %s", len(textFiles), uuid)
		}
	}

	// 5. Archive binary files individually
	for _, filePath := range binaryFiles {
		if err := a.archiveFile(ctx, uuid, executionDir, filePath); err != nil {
			log.Printf("Warning: failed to archive %s for %s: %v", filePath, uuid, err)
			continue
		}
		archived++
	}

	log.Printf("Archived %d items for UUID %s", archived, uuid)
	return archived, nil
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

// ExtractFilePaths parses outputs.json and returns all string values that
// look like absolute file paths existing on disk under executionDir.
func ExtractFilePaths(outputsPath string, executionDir string) ([]string, error) {
	data, err := os.ReadFile(outputsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read outputs.json: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse outputs.json: %w", err)
	}

	seen := make(map[string]bool)
	var paths []string

	execDirClean := filepath.Clean(executionDir)

	walkValue(raw, func(s string) {
		if !isFilePath(s) {
			return
		}
		clean := filepath.Clean(s)
		if !strings.HasPrefix(clean, execDirClean) {
			return
		}
		if _, err := os.Stat(clean); err != nil {
			return
		}
		if !seen[clean] {
			seen[clean] = true
			paths = append(paths, clean)
		}
	})

	return paths, nil
}

// walkValue recursively walks a JSON value, calling fn for every string found.
func walkValue(v interface{}, fn func(string)) {
	switch val := v.(type) {
	case string:
		fn(val)
	case map[string]interface{}:
		for _, v := range val {
			walkValue(v, fn)
		}
	case []interface{}:
		for _, v := range val {
			walkValue(v, fn)
		}
	}
}

// isFilePath checks if a string looks like an absolute file path.
func isFilePath(s string) bool {
	if len(s) == 0 || s[0] != '/' {
		return false
	}
	if strings.Contains(s, "://") {
		return false
	}
	if len(s) > 4096 {
		return false
	}
	return true
}
