package archiver

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// TextRecord represents a single row in the consolidated Parquet file.
type TextRecord struct {
	FilePath string `parquet:"file_path"`
	Content  string `parquet:"content"`
}

// textExtensions defines file extensions that are treated as text and
// consolidated into a single Parquet file.
var textExtensions = map[string]bool{
	".txt": true,
	".csv": true,
}

// isTextFile checks if a file path has a text extension.
func isTextFile(path string) bool {
	ext := strings.ToLower(path[strings.LastIndex(path, "."):])
	return textExtensions[ext]
}

// buildParquetData reads all text files and returns the Parquet binary content.
// Each file becomes one row with its relative path and full text content.
func buildParquetData(executionDir string, textFiles []string) ([]byte, error) {
	records := make([]TextRecord, 0, len(textFiles))

	for _, filePath := range textFiles {
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue // skip unreadable files
		}

		relPath, err := relativePath(executionDir, filePath)
		if err != nil {
			relPath = filePath
		}

		records = append(records, TextRecord{
			FilePath: relPath,
			Content:  string(data),
		})
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("no text records to write")
	}

	var buf bytes.Buffer
	writer := parquet.NewWriter(&buf, parquet.SchemaOf(TextRecord{}))

	for _, rec := range records {
		if err := writer.Write(&rec); err != nil {
			return nil, fmt.Errorf("failed to write parquet record: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close parquet writer: %w", err)
	}

	return buf.Bytes(), nil
}

// relativePath returns the relative path from base to target, using forward slashes.
func relativePath(base, target string) (string, error) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
