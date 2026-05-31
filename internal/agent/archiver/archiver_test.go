package archiver

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryBackend struct {
	basePath string
	uploads  map[string]string
}

func (b *memoryBackend) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	b.uploads[key] = string(data)
	return nil
}

func (b *memoryBackend) BasePath() string { return b.basePath }
func (b *memoryBackend) Close() error     { return nil }

func TestArchiveReturnsManifest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	uuid := "sample-uuid"
	workflowID := "run-1"
	outputFile := filepath.Join(dir, "result.bam")

	mustWrite(t, filepath.Join(dir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(dir, "inputs.json"), `{"input":"value"}`)
	mustWrite(t, outputFile, "bam data")
	mustWriteJSON(t, filepath.Join(dir, "outputs.json"), map[string]string{"bam": outputFile})

	backend := &memoryBackend{
		basePath: "https://storage.example/archive",
		uploads:  make(map[string]string),
	}
	archiver := NewArchiver(backend)

	result, err := archiver.ArchiveWorkflow(ctx, uuid, workflowID, dir)
	if err != nil {
		t.Fatalf("Archive returned error: %v", err)
	}

	if result.UUID != uuid {
		t.Fatalf("unexpected uuid: %+v", result)
	}
	if result.WorkflowID != workflowID {
		t.Fatalf("unexpected workflow id: %+v", result)
	}
	if result.ArchiveBase != backend.basePath || result.BasePath != backend.basePath {
		t.Fatalf("unexpected archive base aliases: %+v", result)
	}
	if result.ObjectPrefix != uuid || result.KeyPrefix != uuid {
		t.Fatalf("unexpected key prefix aliases: %+v", result)
	}
	if result.OutputsResolvedKey != uuid+"/outputs.resolved.json" {
		t.Fatalf("unexpected outputs resolved key: %+v", result)
	}
	if result.ArchivedCount != 5 {
		t.Fatalf("unexpected archived count: got %d uploads=%v", result.ArchivedCount, backend.uploads)
	}

	rewritten, ok := backend.uploads[result.OutputsResolvedKey]
	if !ok {
		t.Fatalf("missing rewritten outputs upload: uploads=%v", backend.uploads)
	}
	if !strings.Contains(rewritten, backend.basePath+"/"+uuid+"/result.bam") {
		t.Fatalf("rewritten outputs did not include archived object path: %s", rewritten)
	}
}

func TestNewFromPathSupportsLocalArchive(t *testing.T) {
	ctx := context.Background()
	executionDir := t.TempDir()
	archiveDir := filepath.Join(t.TempDir(), "archive")
	uuid := "sample-uuid"
	outputFile := filepath.Join(executionDir, "result.bam")

	mustWrite(t, filepath.Join(executionDir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(executionDir, "inputs.json"), `{"input":"value"}`)
	mustWrite(t, outputFile, "bam data")
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string]string{"bam": outputFile})

	archiver, err := NewFromPath(archiveDir, "", "")
	if err != nil {
		t.Fatalf("NewFromPath returned error: %v", err)
	}

	result, err := archiver.ArchiveWorkflow(ctx, uuid, "run-1", executionDir)
	if err != nil {
		t.Fatalf("ArchiveWorkflow returned error: %v", err)
	}
	if result.ArchiveBase != archiveDir {
		t.Fatalf("unexpected local archive base: %+v", result)
	}

	for _, relPath := range []string{
		filepath.Join(uuid, "workflow.log"),
		filepath.Join(uuid, "inputs.json"),
		filepath.Join(uuid, "outputs.json"),
		filepath.Join(uuid, "result.bam"),
		filepath.Join(uuid, "outputs.resolved.json"),
	} {
		if _, err := os.Stat(filepath.Join(archiveDir, relPath)); err != nil {
			t.Fatalf("missing archived file %s: %v", relPath, err)
		}
	}
}

func TestLocalBackendRejectsEscapingKeys(t *testing.T) {
	backend, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBackend returned error: %v", err)
	}
	if err := backend.Upload(context.Background(), "../escape.txt", strings.NewReader("bad"), 3); err == nil {
		t.Fatal("expected escaping key to be rejected")
	}
}

func TestArchiveIgnoresOutputPathsOutsideExecutionDir(t *testing.T) {
	ctx := context.Background()
	executionDir := t.TempDir()
	outsideDir := t.TempDir()
	uuid := "sample-uuid"
	insideFile := filepath.Join(executionDir, "result.bam")
	outsideFile := filepath.Join(outsideDir, "secret.bam")

	mustWrite(t, filepath.Join(executionDir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(executionDir, "inputs.json"), `{"input":"value"}`)
	mustWrite(t, insideFile, "bam data")
	mustWrite(t, outsideFile, "secret data")
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string]string{
		"inside":  insideFile,
		"outside": outsideFile,
	})

	backend := &memoryBackend{
		basePath: "https://storage.example/archive",
		uploads:  make(map[string]string),
	}
	archiver := NewArchiver(backend)

	result, err := archiver.ArchiveWorkflow(ctx, uuid, "run-1", executionDir)
	if err != nil {
		t.Fatalf("ArchiveWorkflow returned error: %v", err)
	}
	if result.ArchivedCount != 5 {
		t.Fatalf("unexpected archived count: got %d uploads=%v", result.ArchivedCount, backend.uploads)
	}
	if _, ok := backend.uploads[uuid+"/secret.bam"]; ok {
		t.Fatalf("outside output file was archived: uploads=%v", backend.uploads)
	}

	rewritten := backend.uploads[result.OutputsResolvedKey]
	if strings.Contains(rewritten, backend.basePath+"/"+uuid+"/secret.bam") {
		t.Fatalf("outside output path was rewritten to archive path: %s", rewritten)
	}
	var rewrittenJSON map[string]string
	if err := json.Unmarshal([]byte(rewritten), &rewrittenJSON); err != nil {
		t.Fatalf("rewritten outputs is not JSON: %v", err)
	}
	if rewrittenJSON["outside"] != outsideFile {
		t.Fatalf("outside output path should remain unchanged: got %q want %q", rewrittenJSON["outside"], outsideFile)
	}
}

func TestResolveOutputsDoesNotReadJSONOutsideExecutionDir(t *testing.T) {
	executionDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideJSON := filepath.Join(outsideDir, "secret.json")

	mustWrite(t, outsideJSON, `{"secret":"leaked"}`)
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string]string{
		"manifest": outsideJSON,
	})

	resolved, pathMap, err := resolveOutputs(filepath.Join(executionDir, "outputs.json"), executionDir)
	if err != nil {
		t.Fatalf("resolveOutputs returned error: %v", err)
	}
	if len(pathMap) != 0 {
		t.Fatalf("outside JSON should not be collected, got pathMap=%v", pathMap)
	}

	data, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("failed to marshal resolved outputs: %v", err)
	}
	if strings.Contains(string(data), "leaked") {
		t.Fatalf("outside JSON content was resolved: %s", data)
	}
}

func mustWriteJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal %s: %v", path, err)
	}
	mustWrite(t, path, string(data))
}

func mustWrite(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
