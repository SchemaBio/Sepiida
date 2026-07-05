package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SchemaBio/Sepiida/internal/common/model"
)

func TestHasStateChangedResetsIdempotencyFlagsForNewExecution(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "workflow.log")
	mustWriteStateTestFile(t, logPath, "workflow log")
	info := mustStatStateTestFile(t, logPath)

	manager := NewStateManager()
	prev := &WorkflowState{
		UUID:           "sample-uuid",
		WorkflowID:     "run-1",
		WorkflowStatus: model.WorkflowStatusSuccess,
		OutputsPushed:  true,
		Archived:       true,
		ExecutionDir:   filepath.Join(dir, "run-1"),
	}
	if err := manager.SaveState(dir, prev); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	workflow := &model.Workflow{
		ID:     "run-2",
		Status: model.WorkflowStatusSuccess,
	}

	changed, next := manager.HasStateChanged(dir, "sample-uuid", filepath.Join(dir, "run-2"), workflow, nil, info, true)
	if !changed {
		t.Fatal("expected new execution to be pushed")
	}
	if next.OutputsPushed {
		t.Fatalf("new execution inherited OutputsPushed: %+v", next)
	}
	if next.Archived {
		t.Fatalf("new execution inherited Archived: %+v", next)
	}
}

func TestHasStateChangedPreservesIdempotencyFlagsForSameExecution(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "workflow.log")
	mustWriteStateTestFile(t, logPath, "workflow log")
	info := mustStatStateTestFile(t, logPath)

	executionDir := filepath.Join(dir, "run-1")
	manager := NewStateManager()
	prev := &WorkflowState{
		UUID:           "sample-uuid",
		WorkflowID:     "run-1",
		WorkflowStatus: model.WorkflowStatusSuccess,
		OutputsPushed:  true,
		Archived:       true,
		LogFileSize:    info.Size() + 1,
		LogFileModTime: info.ModTime(),
		ExecutionDir:   executionDir,
	}
	if err := manager.SaveState(dir, prev); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	workflow := &model.Workflow{
		ID:     "run-1",
		Status: model.WorkflowStatusSuccess,
	}

	changed, next := manager.HasStateChanged(dir, "sample-uuid", executionDir, workflow, nil, info, true)
	if !changed {
		t.Fatal("expected log size change to trigger push")
	}
	if !next.OutputsPushed {
		t.Fatalf("same execution did not preserve OutputsPushed: %+v", next)
	}
	if !next.Archived {
		t.Fatalf("same execution did not preserve Archived: %+v", next)
	}
}

func TestHasStateChangedDoesNotMarkOutputsPushedBeforeSendSuccess(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "workflow.log")
	mustWriteStateTestFile(t, logPath, "workflow log")
	info := mustStatStateTestFile(t, logPath)

	manager := NewStateManager()
	workflow := &model.Workflow{
		ID:          "run-1",
		Status:      model.WorkflowStatusSuccess,
		OutputsJSON: `{"result":"ok"}`,
	}

	changed, next := manager.HasStateChanged(dir, "sample-uuid", filepath.Join(dir, "run-1"), workflow, nil, info, false)
	if !changed {
		t.Fatal("expected completed workflow to be pushed")
	}
	if next.OutputsPushed {
		t.Fatalf("outputs were marked pushed before send success: %+v", next)
	}
}

func TestStateManagerRefusesSymlinkStateDirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("failed to create real state dir: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	manager := NewStateManager()
	state := &WorkflowState{
		UUID:           "sample-uuid",
		WorkflowID:     "run-1",
		WorkflowStatus: model.WorkflowStatusRunning,
		ExecutionDir:   filepath.Join(realDir, "run-1"),
	}
	if err := manager.SaveState(linkDir, state); err == nil {
		t.Fatal("expected SaveState to reject symlink state directory")
	}
	if _, err := manager.LoadState(linkDir); err == nil {
		t.Fatal("expected LoadState to reject symlink state directory")
	}
}

func mustWriteStateTestFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func mustStatStateTestFile(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", path, err)
	}
	return info
}
