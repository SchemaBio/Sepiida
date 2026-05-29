package archiver

import (
	"context"
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
	mustWrite(t, filepath.Join(dir, "outputs.json"), `{"bam":"`+outputFile+`"}`)

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
	mustWrite(t, filepath.Join(executionDir, "outputs.json"), `{"bam":"`+outputFile+`"}`)

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

func mustWrite(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
