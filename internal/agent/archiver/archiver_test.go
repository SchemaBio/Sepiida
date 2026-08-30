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

func TestArchiveWorkflowWithPrefixSeparatesWorkflowAndAttemptIdentity(t *testing.T) {
	ctx := context.Background()
	executionDir := t.TempDir()
	taskUUID := "9e906aba-75f0-4b68-a355-5138b0f07c42"
	attemptID := "2897aa33-054d-4a00-88c6-40701d0cf491"
	outputFile := filepath.Join(executionDir, "result.bam")

	mustWrite(t, filepath.Join(executionDir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(executionDir, "inputs.json"), `{"input":"value"}`)
	mustWrite(t, outputFile, "bam data")
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string]string{"bam": outputFile})

	backend := &memoryBackend{
		basePath: "https://storage.example/archive",
		uploads:  make(map[string]string),
	}
	archiver := NewArchiver(backend)

	result, err := archiver.ArchiveWorkflowWithPrefix(ctx, taskUUID, "run-1", attemptID, executionDir)
	if err != nil {
		t.Fatalf("ArchiveWorkflowWithPrefix returned error: %v", err)
	}
	if result.UUID != taskUUID || result.WorkflowID != "run-1" {
		t.Fatalf("workflow identity was changed: %+v", result)
	}
	if result.ObjectPrefix != attemptID || result.KeyPrefix != attemptID || result.OutputsResolvedKey != attemptID+"/outputs.resolved.json" {
		t.Fatalf("attempt prefix was not applied: %+v", result)
	}
	if _, ok := backend.uploads[attemptID+"/result.bam"]; !ok {
		t.Fatalf("output was not stored below attempt prefix: %v", backend.uploads)
	}
	if _, ok := backend.uploads[taskUUID+"/result.bam"]; ok {
		t.Fatalf("output was incorrectly stored below task UUID: %v", backend.uploads)
	}
	rewritten := backend.uploads[result.OutputsResolvedKey]
	if !strings.Contains(rewritten, backend.basePath+"/"+attemptID+"/result.bam") {
		t.Fatalf("rewritten outputs did not use attempt prefix: %s", rewritten)
	}
}

func TestValidateArchivePrefix(t *testing.T) {
	valid := "2897aa33-054d-4a00-88c6-40701d0cf491"
	for _, prefix := range []string{"", valid} {
		if err := ValidateArchivePrefix(prefix); err != nil {
			t.Fatalf("expected archive prefix %q to be valid: %v", prefix, err)
		}
	}
	for _, prefix := range []string{"attempt-1", "../escape", valid + "/nested", "2897aa33-054d-4a00-88c6-40701d0cf49"} {
		if err := ValidateArchivePrefix(prefix); err == nil {
			t.Fatalf("expected archive prefix %q to be rejected", prefix)
		}
	}
}

func TestArchiveWorkflowWithPrefixRejectsInvalidPrefix(t *testing.T) {
	backend := &memoryBackend{basePath: "https://storage.example/archive", uploads: make(map[string]string)}
	archiver := NewArchiver(backend)
	result, err := archiver.ArchiveWorkflowWithPrefix(context.Background(), "task-uuid", "run-1", "../escape", t.TempDir())
	if err == nil {
		t.Fatal("expected invalid archive prefix to be rejected")
	}
	if result == nil || result.UUID != "task-uuid" || len(backend.uploads) != 0 {
		t.Fatalf("invalid prefix caused unexpected archive side effects: result=%+v uploads=%v", result, backend.uploads)
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

func TestNewFromPathRejectsCredentialedOrDecoratedArchiveURLs(t *testing.T) {
	for _, rawURL := range []string{
		"https://user:pass@storage.example/bucket/prefix",
		"https://storage.example/bucket/prefix?token=secret",
		"https://storage.example/bucket/prefix#fragment",
		"https://storage.example/bucket/prefix\nextra",
	} {
		if _, err := NewFromPath(rawURL, "", ""); err == nil {
			t.Fatalf("expected archive URL %q to be rejected", rawURL)
		}
	}
}

func TestNewFromPathRejectsAmbiguousArchiveURLs(t *testing.T) {
	for _, rawURL := range []string{
		"ftp://storage.example/bucket/prefix",
		"file://tmp/archive",
		"//storage.example/bucket/prefix",
		`\\storage.example\share\prefix`,
	} {
		if _, err := NewFromPath(rawURL, "", ""); err == nil {
			t.Fatalf("expected ambiguous archive path %q to be rejected", rawURL)
		}
	}
}

func TestArchiveURLDetectionIsCaseInsensitive(t *testing.T) {
	if !isS3URL("S3://bucket/prefix") {
		t.Fatal("expected uppercase S3 scheme to be detected")
	}
	if _, err := NewFromPath("S3://bucket/prefix", "access", "secret"); err != nil {
		t.Fatalf("expected uppercase S3 scheme to initialize: %v", err)
	}
	if !isOSSURL("HTTPS://bucket.oss-cn-hangzhou.aliyuncs.com/prefix") {
		t.Fatal("expected uppercase HTTPS OSS URL to be detected")
	}
	if !isCOSURL("HTTPS://bucket.cos.ap-guangzhou.myqcloud.com/prefix") {
		t.Fatal("expected uppercase HTTPS COS URL to be detected")
	}
}

func TestLocalBackendRejectsEscapingKeys(t *testing.T) {
	backend, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBackend returned error: %v", err)
	}
	for _, key := range []string{
		"../escape.txt",
		"a/../escape.txt",
		`nested\windows.txt`,
		"nested//empty.txt",
	} {
		if err := backend.Upload(context.Background(), key, strings.NewReader("bad"), 3); err == nil {
			t.Fatalf("expected escaping/unsafe key %q to be rejected", key)
		}
	}
}

func TestLocalBackendRejectsSymlinkedArchiveDirectories(t *testing.T) {
	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(baseDir, "linked")); err != nil {
		t.Skipf("symlinks are not available in this environment: %v", err)
	}

	backend, err := NewLocalBackend(baseDir)
	if err != nil {
		t.Fatalf("NewLocalBackend returned error: %v", err)
	}

	err = backend.Upload(context.Background(), "linked/escape.txt", strings.NewReader("bad"), 3)
	if err == nil {
		t.Fatal("expected upload through symlinked archive directory to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("archive write escaped base dir, stat err=%v", statErr)
	}
}

func TestObjectBackendsRejectUnsafeKeysBeforeUpload(t *testing.T) {
	for _, key := range []string{
		"../escape.txt",
		"/absolute.txt",
		`nested\windows.txt`,
		"nested//empty.txt",
		"http://example.test/object",
		"ok/\x00bad.txt",
	} {
		if _, err := joinObjectKey("prefix", key); err == nil {
			t.Fatalf("expected unsafe object key %q to be rejected", key)
		}
	}
}

func TestParseS3URLOSSShortForm(t *testing.T) {
	endpoint, bucket, prefix, useSSL, err := ParseS3URL("oss://cn-hangzhou/my-bucket/results")
	if err != nil {
		t.Fatalf("ParseS3URL returned error: %v", err)
	}
	if endpoint != "oss-cn-hangzhou.aliyuncs.com" || bucket != "my-bucket" || prefix != "results" || !useSSL {
		t.Fatalf("unexpected OSS parse result endpoint=%q bucket=%q prefix=%q useSSL=%t", endpoint, bucket, prefix, useSSL)
	}
}

func TestObjectBackendsRejectUnsafePrefixes(t *testing.T) {
	for _, rawURL := range []string{
		"s3://bucket/../escape",
		"s3://bucket/prefix//double",
		"cos://ap-guangzhou/bucket/./dot",
		"oss://cn-hangzhou/bucket/nested\\windows",
	} {
		if _, err := NewFromPath(rawURL, "access", "secret"); err == nil {
			t.Fatalf("expected unsafe object prefix %q to be rejected", rawURL)
		}
	}
}

func TestObjectBackendsRejectUnsafeLocationComponents(t *testing.T) {
	for _, rawURL := range []string{
		"s3://bad bucket/prefix",
		"s3://bad%0abucket/prefix",
		"oss://cn-hangzhou/bad%2fbucket/prefix",
		"cos://ap-guangzhou/bad@bucket/prefix",
	} {
		if _, err := NewFromPath(rawURL, "access", "secret"); err == nil {
			t.Fatalf("expected unsafe archive location %q to be rejected", rawURL)
		}
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

func TestArchiveGeneratesUniqueKeysForRepeatedBasenames(t *testing.T) {
	ctx := context.Background()
	executionDir := t.TempDir()
	uuid := "sample-uuid"

	paths := []string{
		filepath.Join(executionDir, "a", "result.bam"),
		filepath.Join(executionDir, "b", "result.bam"),
		filepath.Join(executionDir, "c", "result.bam"),
		filepath.Join(executionDir, "d", "result_2.bam"),
	}
	for i, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create output dir: %v", err)
		}
		mustWrite(t, path, string(rune('a'+i)))
	}
	mustWrite(t, filepath.Join(executionDir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(executionDir, "inputs.json"), `{"input":"value"}`)
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string][]string{
		"files": paths,
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

	for _, key := range []string{
		uuid + "/result.bam",
		uuid + "/result_3.bam",
		uuid + "/result_4.bam",
		uuid + "/result_2.bam",
	} {
		if _, ok := backend.uploads[key]; !ok {
			t.Fatalf("missing unique archive key %s: uploads=%v", key, backend.uploads)
		}
	}

	rewritten := backend.uploads[result.OutputsResolvedKey]
	for _, archivedPath := range []string{
		backend.basePath + "/" + uuid + "/result.bam",
		backend.basePath + "/" + uuid + "/result_3.bam",
		backend.basePath + "/" + uuid + "/result_4.bam",
		backend.basePath + "/" + uuid + "/result_2.bam",
	} {
		if !strings.Contains(rewritten, archivedPath) {
			t.Fatalf("rewritten outputs missing %s: %s", archivedPath, rewritten)
		}
	}
}

func TestArchiveDeduplicatesRepeatedSameOutputPath(t *testing.T) {
	ctx := context.Background()
	executionDir := t.TempDir()
	uuid := "sample-uuid"
	outputFile := filepath.Join(executionDir, "result.bam")

	mustWrite(t, filepath.Join(executionDir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(executionDir, "inputs.json"), `{"input":"value"}`)
	mustWrite(t, outputFile, "bam data")
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string][]string{
		"files": {outputFile, outputFile},
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
		t.Fatalf("expected repeated path to upload once plus manifests, got %d uploads=%v", result.ArchivedCount, backend.uploads)
	}
	if _, ok := backend.uploads[uuid+"/result.bam"]; !ok {
		t.Fatalf("missing canonical output key: uploads=%v", backend.uploads)
	}
	if _, ok := backend.uploads[uuid+"/result_2.bam"]; ok {
		t.Fatalf("same output path should not be renamed as a conflict: uploads=%v", backend.uploads)
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

func TestResolveOutputsRejectsLargeManifest(t *testing.T) {
	executionDir := t.TempDir()
	outputsPath := filepath.Join(executionDir, "outputs.json")
	mustWrite(t, outputsPath, `{"padding":"`+strings.Repeat("x", maxArchiveManifestBytes)+`"}`)

	if _, _, err := resolveOutputs(outputsPath, executionDir); err == nil {
		t.Fatal("expected oversized outputs.json to be rejected")
	}
}

func TestResolveOutputsDoesNotExpandOversizedNestedJSON(t *testing.T) {
	executionDir := t.TempDir()
	nestedJSON := filepath.Join(executionDir, "nested.json")
	mustWrite(t, nestedJSON, `{"secret":"`+strings.Repeat("x", maxNestedJSONBytes)+`"}`)
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string]string{
		"manifest": nestedJSON,
	})

	resolved, pathMap, err := resolveOutputs(filepath.Join(executionDir, "outputs.json"), executionDir)
	if err != nil {
		t.Fatalf("resolveOutputs returned error: %v", err)
	}
	data, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("failed to marshal resolved outputs: %v", err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatalf("oversized nested JSON content was expanded: %s", string(data[:min(len(data), 200)]))
	}
	if _, ok := pathMap[nestedJSON]; !ok {
		t.Fatalf("oversized nested JSON should remain as a file path to archive/preserve, pathMap=%v", pathMap)
	}
}

func TestResolveOutputsStopsSelfReferentialNestedJSON(t *testing.T) {
	executionDir := t.TempDir()
	nestedJSON := filepath.Join(executionDir, "nested.json")
	mustWriteJSON(t, nestedJSON, map[string]string{"self": nestedJSON})
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string]string{
		"manifest": nestedJSON,
	})

	resolved, pathMap, err := resolveOutputs(filepath.Join(executionDir, "outputs.json"), executionDir)
	if err != nil {
		t.Fatalf("resolveOutputs returned error: %v", err)
	}
	data, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("failed to marshal resolved outputs: %v", err)
	}
	if !strings.Contains(string(data), "nested.json") {
		t.Fatalf("cycle path should be preserved once recursion stops: %s", data)
	}
	if len(data) > 4096 {
		t.Fatalf("self-referential JSON expanded unexpectedly, len=%d", len(data))
	}
	if _, ok := pathMap[nestedJSON]; !ok {
		t.Fatalf("cycle path should remain collectable for archive/preserve, pathMap=%v", pathMap)
	}
}

func TestParquetColumnNamesAreUniqueAndStable(t *testing.T) {
	executionDir := t.TempDir()
	table := filepath.Join(executionDir, "table.csv")
	mustWrite(t, table, "\n,gene-id,gene_id,1score\n,BRCA1,BRCA2,42\n")

	_, columns, err := buildSingleFileParquet(table)
	if err != nil {
		t.Fatalf("buildSingleFileParquet returned error: %v", err)
	}
	want := []string{"column_1", "gene_id", "gene_id_2", "col_1score"}
	if len(columns) != len(want) {
		t.Fatalf("unexpected columns: got %v want %v", columns, want)
	}
	for i := range want {
		if columns[i] != want[i] {
			t.Fatalf("unexpected columns: got %v want %v", columns, want)
		}
	}
}

func TestBuildSingleFileParquetIgnoresOverflowColumns(t *testing.T) {
	executionDir := t.TempDir()
	table := filepath.Join(executionDir, "table.csv")
	mustWrite(t, table, "gene,value\nBRCA1,42,unexpected\nBRCA2,7\n")

	data, columns, err := buildSingleFileParquet(table)
	if err != nil {
		t.Fatalf("buildSingleFileParquet returned error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected parquet data to be written")
	}
	want := []string{"gene", "value"}
	if len(columns) != len(want) {
		t.Fatalf("unexpected columns: got %v want %v", columns, want)
	}
	for i := range want {
		if columns[i] != want[i] {
			t.Fatalf("unexpected columns: got %v want %v", columns, want)
		}
	}
}

func TestArchiveWorkflowHandlesNonRegularOutputsManifest(t *testing.T) {
	ctx := context.Background()
	executionDir := t.TempDir()
	uuid := "sample-uuid"

	mustWrite(t, filepath.Join(executionDir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(executionDir, "inputs.json"), `{"input":"value"}`)
	if err := os.Mkdir(filepath.Join(executionDir, "outputs.json"), 0o755); err != nil {
		t.Fatalf("failed to create outputs.json directory: %v", err)
	}

	backend := &memoryBackend{
		basePath: "https://storage.example/archive",
		uploads:  make(map[string]string),
	}
	archiver := NewArchiver(backend)

	result, err := archiver.ArchiveWorkflow(ctx, uuid, "run-1", executionDir)
	if err == nil {
		t.Fatal("expected ArchiveWorkflow to fail for non-regular outputs manifest")
	}
	if result.ArchivedCount != 2 {
		t.Fatalf("expected only regular workflow.log and inputs.json to be archived, got %d uploads=%v", result.ArchivedCount, backend.uploads)
	}
	if _, ok := backend.uploads[result.OutputsResolvedKey]; ok {
		t.Fatalf("outputs.resolved.json should not be uploaded when outputs.json is non-regular: uploads=%v", backend.uploads)
	}
}

func TestArchiveTextFallbackKeepsResolvedManifestValid(t *testing.T) {
	ctx := context.Background()
	executionDir := t.TempDir()
	uuid := "sample-uuid"
	textFile := filepath.Join(executionDir, "header-only.txt")

	mustWrite(t, filepath.Join(executionDir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(executionDir, "inputs.json"), `{"input":"value"}`)
	mustWrite(t, textFile, "gene\tvalue\n")
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string]string{
		"table": textFile,
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
	if _, ok := backend.uploads[uuid+"/header-only.txt"]; !ok {
		t.Fatalf("text fallback upload missing: uploads=%v", backend.uploads)
	}
	if _, ok := backend.uploads[uuid+"/header-only.parquet"]; ok {
		t.Fatalf("parquet should not be uploaded when conversion fails: uploads=%v", backend.uploads)
	}
	rewritten := backend.uploads[result.OutputsResolvedKey]
	if !strings.Contains(rewritten, backend.basePath+"/"+uuid+"/header-only.txt") {
		t.Fatalf("rewritten outputs should reference fallback text object: %s", rewritten)
	}
}

func TestArchiveIgnoresNonRegularOutputPaths(t *testing.T) {
	ctx := context.Background()
	executionDir := t.TempDir()
	uuid := "sample-uuid"
	outputDir := filepath.Join(executionDir, "not-a-file")

	mustWrite(t, filepath.Join(executionDir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(executionDir, "inputs.json"), `{"input":"value"}`)
	if err := os.Mkdir(outputDir, 0o755); err != nil {
		t.Fatalf("failed to create output directory: %v", err)
	}
	mustWriteJSON(t, filepath.Join(executionDir, "outputs.json"), map[string]string{
		"bad": outputDir,
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
	if result.ArchivedCount != 4 {
		t.Fatalf("expected manifests and rewritten outputs only, got %d uploads=%v", result.ArchivedCount, backend.uploads)
	}
	if _, ok := backend.uploads[uuid+"/not-a-file"]; ok {
		t.Fatalf("non-regular output path was archived: uploads=%v", backend.uploads)
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
