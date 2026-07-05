package collector

import (
	"encoding/json"
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
	if state != nil {
		t.Fatalf("state should not be saved before SendProgress succeeds: %+v", state)
	}
	if err := collector.SaveState(result.UUIDDir, result.State); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}
	state, err = collector.LoadState(result.UUIDDir)
	if err != nil {
		t.Fatalf("LoadState after SaveState returned error: %v", err)
	}
	if state == nil {
		t.Fatal("expected state file to be saved after successful push")
	}
	if state.OutputsPushed {
		t.Fatalf("outputs should not be marked pushed before SendOutput succeeds: %+v", state)
	}
}

func TestCollectDoesNotPersistStateBeforeSuccessfulPush(t *testing.T) {
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

	// Simulate a network/server failure by not calling SaveState. The next poll
	// must retry the same update instead of treating it as already pushed.
	results, err = collector.Collect()
	if err != nil {
		t.Fatalf("second Collect returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected retry when previous push was not persisted, got %d", len(results))
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
	if err := collector.SaveState(results[0].UUIDDir, results[0].State); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
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
	if len(results) != 0 {
		t.Fatalf("expected no retry when archiving is disabled, got %d", len(results))
	}
}

func TestCollectRetriesArchiveWhenArchiveEnabled(t *testing.T) {
	watchDir := t.TempDir()
	uuid := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	setupCollectorWorkflow(t, watchDir, uuid, "20260428_094955_SingleWES")

	collector := NewProgressCollector(parser.NewLogParser(), []string{watchDir}, "agent-1")
	collector.SetArchiveEnabled(true)

	results, err := collector.Collect()
	if err != nil {
		t.Fatalf("first Collect returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected first collect to push, got %d", len(results))
	}
	if err := collector.SaveState(results[0].UUIDDir, results[0].State); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}
	if err := collector.MarkOutputsPushed(results[0].UUIDDir); err != nil {
		t.Fatalf("MarkOutputsPushed returned error: %v", err)
	}

	results, err = collector.Collect()
	if err != nil {
		t.Fatalf("second Collect returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected archive retry while archive is enabled, got %d", len(results))
	}
	if results[0].Progress.Workflow.OutputsJSON != "" {
		t.Fatalf("outputs should not be reread after MarkOutputsPushed: %+v", results[0].Progress.Workflow)
	}

	if err := collector.MarkArchived(results[0].UUIDDir); err != nil {
		t.Fatalf("MarkArchived returned error: %v", err)
	}
	results, err = collector.Collect()
	if err != nil {
		t.Fatalf("third Collect returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no retry after MarkArchived, got %d", len(results))
	}
}

func TestResolveOutputsJSONDoesNotReadOutsideExecutionDir(t *testing.T) {
	execDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideJSON := filepath.Join(outsideDir, "secret.json")
	writeCollectorTestFile(t, outsideJSON, `{"secret":"leaked"}`)

	outputsPath := filepath.Join(execDir, "outputs.json")
	writeCollectorTestJSON(t, outputsPath, map[string]string{"manifest": outsideJSON})

	resolved, err := resolveOutputsJSON(outputsPath, execDir)
	if err != nil {
		t.Fatalf("resolveOutputsJSON returned error: %v", err)
	}
	if strings.Contains(resolved, "leaked") {
		t.Fatalf("outside JSON content was resolved: %s", resolved)
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(resolved), &parsed); err != nil {
		t.Fatalf("resolved outputs is not JSON: %v", err)
	}
	if parsed["manifest"] != outsideJSON {
		t.Fatalf("outside path should be preserved, got %q want %q", parsed["manifest"], outsideJSON)
	}
}

func TestResolveOutputsJSONRejectsLargeOutputs(t *testing.T) {
	execDir := t.TempDir()
	outputsPath := filepath.Join(execDir, "outputs.json")
	writeCollectorTestFile(t, outputsPath, `{"padding":"`+strings.Repeat("x", maxOutputsJSONBytes)+`"}`)

	if _, err := resolveOutputsJSON(outputsPath, execDir); err == nil {
		t.Fatal("expected oversized outputs.json to be rejected")
	}
}

func TestResolveOutputsJSONDoesNotResolveOversizedNestedJSON(t *testing.T) {
	execDir := t.TempDir()
	nestedJSON := filepath.Join(execDir, "nested.json")
	writeCollectorTestFile(t, nestedJSON, `{"secret":"`+strings.Repeat("x", maxResolvedJSONFileBytes)+`"}`)

	outputsPath := filepath.Join(execDir, "outputs.json")
	writeCollectorTestJSON(t, outputsPath, map[string]string{"manifest": nestedJSON})

	resolved, err := resolveOutputsJSON(outputsPath, execDir)
	if err != nil {
		t.Fatalf("resolveOutputsJSON returned error: %v", err)
	}
	if strings.Contains(resolved, "secret") {
		t.Fatalf("oversized nested JSON should not be expanded: %s", resolved[:min(len(resolved), 200)])
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(resolved), &parsed); err != nil {
		t.Fatalf("resolved outputs is not JSON: %v", err)
	}
	if parsed["manifest"] != nestedJSON {
		t.Fatalf("oversized nested JSON path should be preserved, got %q want %q", parsed["manifest"], nestedJSON)
	}
}

func TestResolveOutputsJSONStopsSelfReferentialNestedJSON(t *testing.T) {
	execDir := t.TempDir()
	nestedJSON := filepath.Join(execDir, "nested.json")
	writeCollectorTestJSON(t, nestedJSON, map[string]string{"self": nestedJSON})
	outputsPath := filepath.Join(execDir, "outputs.json")
	writeCollectorTestJSON(t, outputsPath, map[string]string{"manifest": nestedJSON})

	resolved, err := resolveOutputsJSON(outputsPath, execDir)
	if err != nil {
		t.Fatalf("resolveOutputsJSON returned error: %v", err)
	}
	if !strings.Contains(resolved, "nested.json") {
		t.Fatalf("cycle path should be preserved once recursion stops: %s", resolved)
	}
	if len(resolved) > 4096 {
		t.Fatalf("self-referential JSON expanded unexpectedly, len=%d", len(resolved))
	}
}

func TestResolveLastFileRejectsLargePointer(t *testing.T) {
	uuidDir := t.TempDir()
	lastPath := filepath.Join(uuidDir, "_LAST")
	writeCollectorTestFile(t, lastPath, strings.Repeat("x", maxLastPointerBytes+1))

	if _, err := resolveSymlink(lastPath, uuidDir); err == nil {
		t.Fatal("expected oversized _LAST file pointer to be rejected")
	}
}

func TestReadTaskLogsRejectsOutsideExecutionDir(t *testing.T) {
	execDir := t.TempDir()
	outsideTaskDir := filepath.Join(t.TempDir(), "call-Outside")
	if err := os.MkdirAll(outsideTaskDir, 0o755); err != nil {
		t.Fatalf("failed to create outside task dir: %v", err)
	}
	writeCollectorTestFile(t, filepath.Join(outsideTaskDir, "stdout.txt"), "outside stdout\n")
	writeCollectorTestFile(t, filepath.Join(outsideTaskDir, "stderr.txt"), "outside stderr\n")

	stdout, stderr := readTaskLogs(outsideTaskDir, execDir)
	if stdout != "" || stderr != "" {
		t.Fatalf("outside task logs should not be read, got stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestReadTaskLogsRejectsLargeAndNonRegularFiles(t *testing.T) {
	execDir := t.TempDir()
	taskDir := filepath.Join(execDir, "call-LargeLogs")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task dir: %v", err)
	}

	writeCollectorTestFile(t, filepath.Join(taskDir, "stdout.txt"), strings.Repeat("x", maxTaskLogBytes+1))
	if err := os.Mkdir(filepath.Join(taskDir, "stderr.txt"), 0o755); err != nil {
		t.Fatalf("failed to create stderr directory: %v", err)
	}

	stdout, stderr := readTaskLogs(taskDir, execDir)
	if stdout != "" || stderr != "" {
		t.Fatalf("large/non-regular task logs should not be read, got stdout len=%d stderr=%q", len(stdout), stderr)
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
	writeCollectorTestJSON(t, filepath.Join(execDir, "outputs.json"), map[string]string{
		"bam": filepath.Join(execDir, "result.bam"),
	})
	writeCollectorTestFile(t, filepath.Join(execDir, "result.bam"), "bam")
	writeCollectorTestFile(t, filepath.Join(execDir, "workflow.log"),
		`2026-04-28 09:49:55.697 wdl.w:SingleWES NOTICE workflow start :: name: "SingleWES", source: "workflow.wdl", dir: "`+execDir+`"`+"\n"+
			`2026-04-28 09:49:55.708 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE task setup :: name: "CreateMitoBed", source: "workflow.wdl", dir: "`+taskDir+`"`+"\n"+
			`2026-04-28 09:49:59.280 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE docker task running :: service: "svc", task: "task", message: "started"`+"\n"+
			`2026-04-28 09:50:00.334 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE docker task exit :: state: "complete", exit_code: 0`+"\n"+
			`2026-04-28 10:20:05.417 wdl.w:SingleWES NOTICE done`+"\n")

	lastPath := filepath.Join(uuidDir, "_LAST")
	if err := os.Symlink(runName, lastPath); err != nil {
		writeCollectorTestFile(t, lastPath, runName)
	}

	return execDir
}

func writeCollectorTestJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal %s: %v", path, err)
	}
	writeCollectorTestFile(t, path, string(data))
}

func writeCollectorTestFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
