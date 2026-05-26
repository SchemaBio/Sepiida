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

	result, err := archiver.Archive(ctx, uuid, dir)
	if err != nil {
		t.Fatalf("Archive returned error: %v", err)
	}

	if result.UUID != uuid {
		t.Fatalf("unexpected uuid: %+v", result)
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

func mustWrite(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
