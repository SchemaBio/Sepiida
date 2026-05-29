package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SchemaBio/Sepiida/internal/agent/parser"
)

func TestCollectReadsWorkflowOutputsAndTaskLogs(t *testing.T) {
	watchDir := t.TempDir()
	uuid := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	execDir := setupCollectorWorkflow(t, watchDir, uuid, "20260428_094955_SingleWES")

	collector := NewProgressCollector(parser.NewLogParser(), []string{watchDir}, "agent-1")
	results, err := collector.Collect()
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one changed workflow, got %d", len(results))
	}

	result := results[0]
	if result.Progress.AgentID != "agent-1" || result.Progress.UUID != uuid {
		t.Fatalf("unexpected progress identity: %+v", result.Progress)
	}
	if result.ExecutionDir != execDir {
		t.Fatalf("unexpected execution dir: %s", result.ExecutionDir)
	}
	if result.Progress.Workflow.OutputsJSON == "" || !strings.Contains(result.Progress.Workflow.OutputsJSON, "result.bam") {
		t.Fatalf("outputs were not read: %+v", result.Progress.Workflow)
	}
	if len(result.Progress.Tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(result.Progress.Tasks))
	}
	if result.Progress.Tasks[0].Stdout != "stdout\n" || result.Progress.Tasks[0].Stderr != "stderr\n" {
		t.Fatalf("task logs were not read: %+v", result.Progress.Tasks[0])
	}

	state, err := collector.LoadState(result.UUIDDir)
	if err != nil {
		t.Fatalf("LoadState returned error: %v", err)
	}
	if state == nil {
		t.Fatal("expected state file to be saved")
	}
	if state.OutputsPushed {
		t.Fatalf("outputs should not be marked pushed before SendOutput succeeds: %+v", state)
	}
}

func TestCollectRetriesOutputsUntilMarkedPushed(t *testing.T) {
	watchDir := t.TempDir()
	uuid := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	setupCollectorWorkflow(t, watchDir, uuid, "20260428_094955_SingleWES")

	collector := NewProgressCollector(parser.NewLogParser(), []string{watchDir}, "agent-1")
	results, err := collector.Collect()
	if err != nil {
		t.Fatalf("first Collect returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected first collect to push, got %d", len(results))
	}

	results, err = collector.Collect()
	if err != nil {
		t.Fatalf("second Collect returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected second collect to retry outputs before MarkOutputsPushed, got %d", len(results))
	}
	if results[0].Progress.Workflow.OutputsJSON == "" {
		t.Fatal("expected outputs json on retry")
	}

	if err := collector.MarkOutputsPushed(results[0].UUIDDir); err != nil {
		t.Fatalf("MarkOutputsPushed returned error: %v", err)
	}
	results, err = collector.Collect()
	if err != nil {
		t.Fatalf("third Collect returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected archive retry to still push until archived, got %d", len(results))
	}
	if results[0].Progress.Workflow.OutputsJSON != "" {
		t.Fatalf("outputs should not be reread after MarkOutputsPushed: %+v", results[0].Progress.Workflow)
	}
}

func setupCollectorWorkflow(t *testing.T, watchDir, uuid, runName string) string {
	t.Helper()

	uuidDir := filepath.Join(watchDir, uuid)
	execDir := filepath.Join(uuidDir, runName)
	taskDir := filepath.Join(execDir, "call-CreateMitoBed")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create workflow dirs: %v", err)
	}

	writeCollectorTestFile(t, filepath.Join(taskDir, "stdout.txt"), "stdout\n")
	writeCollectorTestFile(t, filepath.Join(taskDir, "stderr.txt"), "stderr\n")
	writeCollectorTestFile(t, filepath.Join(execDir, "outputs.json"), `{"bam":"`+filepath.Join(execDir, "result.bam")+`"}`)
	writeCollectorTestFile(t, filepath.Join(execDir, "result.bam"), "bam")
	writeCollectorTestFile(t, filepath.Join(execDir, "workflow.log"),
		`2026-04-28 09:49:55.697 wdl.w:SingleWES NOTICE workflow start :: name: "SingleWES", source: "workflow.wdl", dir: "`+execDir+`"`+"\n"+
			`2026-04-28 09:49:55.708 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE task setup :: name: "CreateMitoBed", source: "workflow.wdl", dir: "`+taskDir+`"`+"\n"+
			`2026-04-28 09:49:59.280 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE docker task running :: service: "svc", task: "task", message: "started"`+"\n"+
			`2026-04-28 09:50:00.334 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE docker task exit :: state: "complete", exit_code: 0`+"\n"+
			`2026-04-28 10:20:05.417 wdl.w:SingleWES NOTICE done`+"\n")

	if err := os.Symlink(runName, filepath.Join(uuidDir, "_LAST")); err != nil {
		t.Fatalf("failed to create _LAST symlink: %v", err)
	}

	return execDir
}

func writeCollectorTestFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
