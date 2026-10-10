package archiver

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestBuildSingleFileParquetPreservesEmptyTSVFields(t *testing.T) {
	type reportRow struct {
		Gene     string `parquet:"Gene"`
		Location string `parquet:"Location"`
		Effect   string `parquet:"Effect"`
	}
	input := "Gene\tLocation\tEffect\r\n" +
		"GENE1\t\tmissense\r\n" +
		"\texon\tsynonymous\r\n" +
		"GENE3\t\t\r\n" +
		"GENE4\t  intron\t  effect\r\n" +
		"\t\t\r\n"
	file := filepath.Join(t.TempDir(), "report.tsv")
	mustWrite(t, file, input)
	data, _, err := buildSingleFileParquet(file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parquet.Read[reportRow](bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	want := []reportRow{
		{Gene: "GENE1", Effect: "missense"},
		{Location: "exon", Effect: "synonymous"},
		{Gene: "GENE3"},
		{Gene: "GENE4", Location: "  intron", Effect: "  effect"},
		{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parquet rows lost or shifted: got %#v want %#v", got, want)
	}
}

func TestBuildSingleFileParquetRejectsMalformedRecords(t *testing.T) {
	for _, input := range []string{
		"gene\tvalue\nBRCA1\t42\nBRCA2\nBRCA3\t7\n",
		"gene\tvalue\nBRCA1\t42\nBRCA2\t7\textra\n",
		"gene,value\nBRCA1,42\nBRCA2\n",
	} {
		t.Run(input, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "report.txt")
			mustWrite(t, file, input)
			data, _, err := buildSingleFileParquet(file)
			if err == nil || len(data) != 0 {
				t.Fatalf("malformed report published partial data: bytes=%d err=%v", len(data), err)
			}
		})
	}
}

func TestArchiveMalformedTablePreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "report.txt")
	input := "gene\tvalue\nBRCA1\t42\nBRCA2\nBRCA3\t7\n"
	mustWrite(t, file, input)
	mustWrite(t, filepath.Join(dir, "workflow.log"), "workflow log")
	mustWrite(t, filepath.Join(dir, "inputs.json"), "{}")
	mustWriteJSON(t, filepath.Join(dir, "outputs.json"), map[string]string{"report": file})
	backend := &memoryBackend{basePath: "https://storage.example/archive", uploads: map[string]string{}}
	result, err := NewArchiver(backend).ArchiveWorkflow(context.Background(), "task", "attempt", dir)
	if err != nil {
		t.Fatal(err)
	}
	if backend.uploads["task/report.txt"] != input {
		t.Fatal("fallback did not preserve the complete original report")
	}
	if _, exists := backend.uploads["task/report.parquet"]; exists {
		t.Fatal("malformed report must not publish a partial Parquet object")
	}
	if !strings.Contains(backend.uploads[result.OutputsResolvedKey], backend.basePath+"/task/report.txt") {
		t.Fatal("manifest must reference the preserved original report")
	}
}
