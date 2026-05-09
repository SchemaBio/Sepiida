package archiver

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// textExtensions defines file extensions that are treated as text and
// converted to Parquet files (not uploaded individually).
var textExtensions = map[string]bool{
	".txt": true,
	".csv": true,
	".tsv": true,
}

// isTextFile checks if a file path has a text extension.
func isTextFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return textExtensions[ext]
}

// parquetFileName generates the Parquet file name from the original text file name.
// E.g., "cnv.txt" -> "cnv.parquet", "vcfanno.csv" -> "vcfanno.parquet"
func parquetFileName(originalName string) string {
	base := strings.TrimSuffix(filepath.Base(originalName), filepath.Ext(originalName))
	return base + ".parquet"
}

// buildSingleFileParquet reads a single text file, parses it with dynamic schema
// based on the header row, and returns the Parquet binary content.
// Returns the Parquet data and the column names extracted from header.
func buildSingleFileParquet(filePath string) ([]byte, []string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read file: %w", err)
	}

	if len(data) == 0 {
		return nil, nil, fmt.Errorf("empty file content")
	}

	// Parse CSV/TSV content to get headers and rows
	delimiter := detectDelimiter(data)
	headers, rows := parseCSVContentWithHeaders(data, delimiter)

	if len(headers) == 0 {
		return nil, nil, fmt.Errorf("no header found")
	}

	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("no data rows found")
	}

	// Build dynamic Parquet schema from headers
	schema := buildDynamicSchema(headers)

	// Write rows to Parquet
	var buf bytes.Buffer
	writer := parquet.NewWriter(&buf, schema)

	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			log.Printf("Warning: failed to write row: %v", err)
			continue
		}
	}

	if err := writer.Close(); err != nil {
		return nil, nil, fmt.Errorf("failed to close parquet writer: %w", err)
	}

	return buf.Bytes(), headers, nil
}

// buildDynamicSchema creates a Parquet schema from column names.
// All columns are defined as optional string type.
func buildDynamicSchema(columns []string) *parquet.Schema {
	// Create a Group (map of column name -> Node)
	group := make(parquet.Group)
	for _, col := range columns {
		// Sanitize column name for Parquet (replace special characters)
		safeName := sanitizeColumnName(col)
		// Each column is an optional string
		group[safeName] = parquet.Optional(parquet.String())
	}

	return parquet.NewSchema("text_file", group)
}

// sanitizeColumnName makes column names safe for Parquet.
// Replaces spaces and special characters with underscores.
func sanitizeColumnName(name string) string {
	// Replace common problematic characters
	result := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '.' || r == '/' || r == '\\' {
			return '_'
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, name)
	return result
}

// detectDelimiter analyzes the first few lines to determine the delimiter.
// Returns ',' for CSV, '\t' for TSV, or ',' as default.
func detectDelimiter(content []byte) rune {
	lines := strings.Split(string(content), "\n")
	if len(lines) < 2 {
		return ',' // Default to CSV for single-line files
	}

	firstLine := lines[0]

	// Count potential delimiters in header line
	tabCount := strings.Count(firstLine, "\t")
	commaCount := strings.Count(firstLine, ",")

	// If more tabs than commas, likely TSV
	if tabCount > commaCount {
		return '\t'
	}

	return ',' // Default CSV
}

// parseCSVContentWithHeaders parses CSV/TSV content and returns headers and data rows.
// First line is treated as header, subsequent lines as data.
// Returns headers (column names) and rows (map of column -> value).
func parseCSVContentWithHeaders(content []byte, delimiter rune) ([]string, []map[string]string) {
	reader := csv.NewReader(bytes.NewReader(content))
	reader.Comma = delimiter
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	var headers []string
	var rows []map[string]string
	lineNum := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // Skip malformed lines
		}

		lineNum++

		// Skip empty lines
		if len(record) == 0 || (len(record) == 1 && strings.TrimSpace(record[0]) == "") {
			continue
		}

		if lineNum == 1 {
			// First line is header
			headers = record
			continue
		}

		// Build row map with sanitized column names matching the schema
		row := make(map[string]string)
		for i, val := range record {
			if i < len(headers) {
				safeCol := sanitizeColumnName(headers[i])
				row[safeCol] = val
			} else {
				// Extra columns beyond header
				row[fmt.Sprintf("col_%d", i)] = val
			}
		}

		rows = append(rows, row)
	}

	return headers, rows
}

// relativePath returns the relative path from base to target, using forward slashes.
func relativePath(base, target string) (string, error) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}