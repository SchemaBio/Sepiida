package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SchemaBio/Sepiida/internal/common/model"
)

func TestParseLogFileCompletedWorkflow(t *testing.T) {
	dir := t.TempDir()
	execDir := filepath.Join(dir, "20260428_094955_SingleWES")
	taskDir := filepath.Join(execDir, "call-CreateMitoBed")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task dir: %v", err)
	}

	logPath := filepath.Join(execDir, "workflow.log")
	writeParserTestFile(t, logPath,
		`2026-04-28 09:49:55.697 wdl.w:SingleWES NOTICE workflow start :: name: "SingleWES", source: "workflow.wdl", dir: "`+execDir+`"`+"\n"+
			`2026-04-28 09:49:55.708 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE task setup :: name: "CreateMitoBed", source: "workflow.wdl", dir: "`+taskDir+`"`+"\n"+
			`2026-04-28 09:49:59.280 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE docker task running :: service: "svc", task: "task", message: "started"`+"\n"+
			`2026-04-28 09:50:00.334 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE docker task exit :: state: "complete", exit_code: 0`+"\n"+
			`2026-04-28 10:20:05.417 wdl.w:SingleWES NOTICE done`+"\n")

	workflow, tasks, err := NewLogParser().ParseLogFile(logPath)
	if err != nil {
		t.Fatalf("ParseLogFile returned error: %v", err)
	}
	if workflow == nil {
		t.Fatal("expected workflow")
	}
	if workflow.ID != filepath.Base(execDir) || workflow.Name != "SingleWES" || workflow.Status != model.WorkflowStatusSuccess {
		t.Fatalf("unexpected workflow: %+v", workflow)
	}
	if workflow.StartTime == nil || workflow.EndTime == nil {
		t.Fatalf("expected workflow timestamps: %+v", workflow)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d: %+v", len(tasks), tasks)
	}
	task := tasks[0]
	if task.WorkflowID != workflow.ID || task.JobName != "call-CreateMitoBed" || task.Name != "CreateMitoBed" {
		t.Fatalf("unexpected task identity: %+v", task)
	}
	if task.Status != model.TaskStatusSuccess || task.ExitCode == nil || *task.ExitCode != 0 {
		t.Fatalf("unexpected task status: %+v", task)
	}
	if task.StartTime == nil || task.EndTime == nil {
		t.Fatalf("expected task timestamps: %+v", task)
	}
}

func TestParseLogFileFailedTask(t *testing.T) {
	dir := t.TempDir()
	execDir := filepath.Join(dir, "20260428_094955_SingleWES")
	taskDir := filepath.Join(execDir, "call-FailingTask")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("failed to create task dir: %v", err)
	}

	logPath := filepath.Join(execDir, "workflow.log")
	writeParserTestFile(t, logPath,
		`2026-04-28 09:49:55.697 wdl.w:SingleWES NOTICE workflow start :: name: "SingleWES", source: "workflow.wdl", dir: "`+execDir+`"`+"\n"+
			`2026-04-28 09:49:55.708 wdl.w:SingleWES.t:call-FailingTask NOTICE task setup :: name: "FailingTask", source: "workflow.wdl", dir: "`+taskDir+`"`+"\n"+
			`2026-04-28 09:50:00.334 wdl.w:SingleWES.t:call-FailingTask NOTICE docker task exit :: state: "failed", exit_code: 1`+"\n")

	workflow, tasks, err := NewLogParser().ParseLogFile(logPath)
	if err != nil {
		t.Fatalf("ParseLogFile returned error: %v", err)
	}
	if workflow == nil || workflow.Status != model.WorkflowStatusFailed {
		t.Fatalf("workflow should be marked failed when a task has final failed status: %+v", workflow)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(tasks))
	}
	if tasks[0].Status != model.TaskStatusFailed || tasks[0].ExitCode == nil || *tasks[0].ExitCode != 1 {
		t.Fatalf("unexpected failed task: %+v", tasks[0])
	}
}

func TestParseLogFileAllowsLongLinesWithinLimit(t *testing.T) {
	dir := t.TempDir()
	execDir := filepath.Join(dir, "20260428_094955_SingleWES")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatalf("failed to create execution dir: %v", err)
	}

	logPath := filepath.Join(execDir, "workflow.log")
	longSource := strings.Repeat("a", initialScannerBuffer+1024)
	writeParserTestFile(t, logPath,
		`2026-04-28 09:49:55.697 wdl.w:SingleWES NOTICE workflow start :: name: "SingleWES", source: "`+longSource+`", dir: "`+execDir+`"`+"\n"+
			`2026-04-28 10:20:05.417 wdl.w:SingleWES NOTICE done`+"\n")

	workflow, _, err := NewLogParser().ParseLogFile(logPath)
	if err != nil {
		t.Fatalf("ParseLogFile should accept lines above the scanner default buffer: %v", err)
	}
	if workflow == nil || workflow.Status != model.WorkflowStatusSuccess {
		t.Fatalf("unexpected workflow from long line: %+v", workflow)
	}
}

func TestParseLogFileRejectsLinesAboveLimit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "workflow.log")
	writeParserTestFile(t, logPath, strings.Repeat("x", maxLogLineBytes+1)+"\n")

	_, _, err := NewLogParser().ParseLogFile(logPath)
	if err == nil {
		t.Fatal("expected ParseLogFile to reject a line above maxLogLineBytes")
	}
}

func TestParseLogFileResetsWorkflowIDBetweenFiles(t *testing.T) {
	dir := t.TempDir()
	firstExecDir := filepath.Join(dir, "20260428_094955_SingleWES")
	secondExecDir := filepath.Join(dir, "20260429_094955_SingleWES")
	if err := os.MkdirAll(firstExecDir, 0o755); err != nil {
		t.Fatalf("failed to create first execution dir: %v", err)
	}
	if err := os.MkdirAll(secondExecDir, 0o755); err != nil {
		t.Fatalf("failed to create second execution dir: %v", err)
	}

	parser := NewLogParser()
	firstLog := filepath.Join(firstExecDir, "workflow.log")
	writeParserTestFile(t, firstLog,
		`2026-04-28 09:49:55.697 wdl.w:SingleWES NOTICE workflow start :: name: "SingleWES", source: "workflow.wdl", dir: "`+firstExecDir+`"`+"\n")
	if _, _, err := parser.ParseLogFile(firstLog); err != nil {
		t.Fatalf("first ParseLogFile returned error: %v", err)
	}

	secondLog := filepath.Join(secondExecDir, "workflow.log")
	writeParserTestFile(t, secondLog,
		`2026-04-29 09:49:55.708 wdl.w:SingleWES.t:call-Orphan NOTICE task setup :: name: "Orphan", source: "workflow.wdl", dir: "`+secondExecDir+`/call-Orphan"`+"\n")
	workflow, tasks, err := parser.ParseLogFile(secondLog)
	if err != nil {
		t.Fatalf("second ParseLogFile returned error: %v", err)
	}
	if workflow != nil {
		t.Fatalf("expected no workflow without workflow start, got %+v", workflow)
	}
	if len(tasks) != 1 || tasks[0].WorkflowID != "" {
		t.Fatalf("task inherited stale workflow ID: %+v", tasks)
	}
}

func writeParserTestFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
