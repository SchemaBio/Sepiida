package service

import (
	"context"
	"testing"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/model"
)

type fakeDB struct {
	workflows     map[string]*model.Workflow
	tasks         map[string]*model.Task
	archived      *model.ArchiveResult
	lastListLimit int
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		workflows: make(map[string]*model.Workflow),
		tasks:     make(map[string]*model.Task),
	}
}

func (f *fakeDB) Initialize(ctx context.Context) error { return nil }
func (f *fakeDB) Close() error                         { return nil }

func (f *fakeDB) CreateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	cp := *workflow
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	f.workflows[cp.ID] = &cp
	return nil
}

func (f *fakeDB) UpdateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	cp := *workflow
	f.workflows[cp.ID] = &cp
	return nil
}

func (f *fakeDB) MarkArchived(ctx context.Context, result *model.ArchiveResult) error {
	cp := *result
	f.archived = &cp
	workflowID := cp.WorkflowID
	if workflowID == "" {
		workflow, err := f.GetWorkflowByUUID(ctx, cp.UUID)
		if err != nil {
			return err
		}
		if workflow != nil {
			workflowID = workflow.ID
		}
	}
	if workflowID != "" {
		if workflow, ok := f.workflows[workflowID]; ok {
			updated := *workflow
			updated.Archived = true
			updated.ArchiveBase = cp.ArchiveBase
			updated.BasePath = cp.BasePath
			updated.OutputsResolvedKey = cp.OutputsResolvedKey
			updated.ObjectPrefix = cp.ObjectPrefix
			updated.KeyPrefix = cp.KeyPrefix
			updated.ArchivedCount = cp.ArchivedCount
			f.workflows[workflowID] = &updated
			return nil
		}
	}
	return ErrWorkflowNotFound
}

func (f *fakeDB) GetWorkflow(ctx context.Context, id string) (*model.Workflow, error) {
	if workflow, ok := f.workflows[id]; ok {
		cp := *workflow
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeDB) GetWorkflowByUUID(ctx context.Context, uuid string) (*model.Workflow, error) {
	var latest *model.Workflow
	for _, workflow := range f.workflows {
		if workflow.UUID != uuid {
			continue
		}
		if latest == nil || workflow.CreatedAt.After(latest.CreatedAt) {
			cp := *workflow
			latest = &cp
		}
	}
	return latest, nil
}

// ListWorkflowsByUUID mirrors the production database capability used to
// verify that a legacy row is the only execution that can claim an agent
// callback.  Keeping this method on the fake makes the legacy compatibility
// tests exercise the same identity contract as PostgreSQL.
func (f *fakeDB) ListWorkflowsByUUID(ctx context.Context, uuid string) ([]*model.Workflow, error) {
	var result []*model.Workflow
	for _, workflow := range f.workflows {
		if workflow.UUID != uuid {
			continue
		}
		cp := *workflow
		result = append(result, &cp)
	}
	return result, nil
}

func (f *fakeDB) GetWorkflowsByAgent(ctx context.Context, agentID string) ([]*model.Workflow, error) {
	var result []*model.Workflow
	for _, workflow := range f.workflows {
		if workflow.AgentID == agentID {
			result = append(result, workflow)
		}
	}
	return result, nil
}

func (f *fakeDB) ListWorkflows(ctx context.Context, limit, offset int) ([]*model.Workflow, error) {
	f.lastListLimit = limit
	return nil, nil
}

func (f *fakeDB) CreateTask(ctx context.Context, task *model.Task) error {
	if _, ok := f.workflows[task.WorkflowID]; !ok {
		return errMissingWorkflow
	}
	cp := *task
	cp.ID = cp.GenerateID()
	f.tasks[cp.ID] = &cp
	return nil
}

func (f *fakeDB) UpdateTask(ctx context.Context, task *model.Task) error {
	cp := *task
	cp.ID = cp.GenerateID()
	f.tasks[cp.ID] = &cp
	return nil
}

func (f *fakeDB) GetTask(ctx context.Context, id string) (*model.Task, error) {
	if task, ok := f.tasks[id]; ok {
		cp := *task
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeDB) GetTasksByWorkflow(ctx context.Context, workflowID string) ([]*model.Task, error) {
	return nil, nil
}

var errMissingWorkflow = &missingWorkflowError{}

type missingWorkflowError struct{}

func (e *missingWorkflowError) Error() string { return "missing workflow" }

func TestProcessProgressCreatesNewExecutionForSameUUID(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)
	oldCreated := time.Now().Add(-time.Hour)
	db.workflows["run-1"] = &model.Workflow{
		ID:        "run-1",
		UUID:      "sample-uuid",
		Name:      "Workflow",
		Status:    model.WorkflowStatusSuccess,
		CreatedAt: oldCreated,
	}

	progress := &model.WorkflowProgress{
		AgentID: "agent-1",
		UUID:    "sample-uuid",
		Workflow: model.Workflow{
			ID:     "run-2",
			Name:   "Workflow",
			Status: model.WorkflowStatusRunning,
		},
		Tasks: []model.Task{{
			Name:    "Task",
			JobName: "call-Task",
			Status:  model.TaskStatusRunning,
		}},
	}

	if err := service.ProcessProgress(ctx, progress); err != nil {
		t.Fatalf("ProcessProgress returned error: %v", err)
	}
	if _, ok := db.workflows["run-1"]; !ok {
		t.Fatal("old execution was removed")
	}
	storedRun2ID := "sample-uuid:run-2"
	if workflow, ok := db.workflows[storedRun2ID]; !ok {
		t.Fatal("new execution was not created")
	} else if workflow.UUID != "sample-uuid" || workflow.AgentID != "agent-1" {
		t.Fatalf("new execution not normalized: %+v", workflow)
	}
	if task, ok := db.tasks[storedRun2ID+"_call-Task"]; !ok {
		t.Fatal("task for new execution was not created")
	} else if task.WorkflowID != storedRun2ID {
		t.Fatalf("task points at wrong workflow: %+v", task)
	}
}

func TestProcessProgressRejectsAmbiguousLegacyExecutionIdentity(t *testing.T) {
	ctx := context.Background()
	base := newFakeDB()
	base.workflows["run-1"] = &model.Workflow{ID: "run-1", UUID: "sample-uuid", CreatedAt: time.Now().Add(-time.Hour)}
	store := &legacyCandidatesDB{
		fakeDB: base,
		candidates: []*model.Workflow{
			base.workflows["run-1"],
			{ID: "run-2", UUID: "sample-uuid", CreatedAt: time.Now()},
		},
	}
	service := NewWorkflowService(store)
	err := service.ProcessProgress(ctx, &model.WorkflowProgress{
		AgentID: "agent-1", UUID: "sample-uuid",
		Workflow: model.Workflow{ID: "run-1", Status: model.WorkflowStatusRunning},
	})
	if err == nil {
		t.Fatal("ambiguous legacy execution was silently bound to an agent")
	}
	if store.workflows["run-1"].AgentID != "" {
		t.Fatalf("ambiguous legacy row was modified: %+v", store.workflows["run-1"])
	}
}

type legacyCandidatesDB struct {
	*fakeDB
	candidates []*model.Workflow
}

func (f *legacyCandidatesDB) ListWorkflowsByUUID(context.Context, string) ([]*model.Workflow, error) {
	return f.candidates, nil
}

func TestProcessProgressSeparatesSameRunIDAcrossUUIDs(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)

	makeProgress := func(uuid string) *model.WorkflowProgress {
		return &model.WorkflowProgress{
			AgentID: "agent-1",
			UUID:    uuid,
			Workflow: model.Workflow{
				ID:     "20260705_120000_SingleWES",
				Name:   "SingleWES",
				Status: model.WorkflowStatusRunning,
			},
			Tasks: []model.Task{{
				Name:    "Task",
				JobName: "call-Task",
				Status:  model.TaskStatusRunning,
			}},
		}
	}

	if err := service.ProcessProgress(ctx, makeProgress("sample-a")); err != nil {
		t.Fatalf("first ProcessProgress returned error: %v", err)
	}
	if err := service.ProcessProgress(ctx, makeProgress("sample-b")); err != nil {
		t.Fatalf("second ProcessProgress returned error: %v", err)
	}

	if _, ok := db.workflows["sample-a:20260705_120000_SingleWES"]; !ok {
		t.Fatalf("sample-a workflow missing: keys=%v", mapKeys(db.workflows))
	}
	if _, ok := db.workflows["sample-b:20260705_120000_SingleWES"]; !ok {
		t.Fatalf("sample-b workflow missing: keys=%v", mapKeys(db.workflows))
	}
	if len(db.workflows) != 2 {
		t.Fatalf("same run ID should create two stored executions, got %d", len(db.workflows))
	}
	if _, ok := db.tasks["sample-a:20260705_120000_SingleWES_call-Task"]; !ok {
		t.Fatalf("sample-a task missing: keys=%v", mapKeys(db.tasks))
	}
	if _, ok := db.tasks["sample-b:20260705_120000_SingleWES_call-Task"]; !ok {
		t.Fatalf("sample-b task missing: keys=%v", mapKeys(db.tasks))
	}
}

func TestProcessProgressSeparatesSameWorkflowIDAcrossAttempts(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)

	makeProgress := func(agentID string) *model.WorkflowProgress {
		return &model.WorkflowProgress{
			AgentID: agentID,
			UUID:    "sample-uuid",
			Workflow: model.Workflow{
				ID:     "same-run-id",
				Name:   "Workflow",
				Status: model.WorkflowStatusRunning,
			},
		}
	}

	if err := service.ProcessProgress(ctx, makeProgress("agent-1")); err != nil {
		t.Fatalf("first ProcessProgress returned error: %v", err)
	}
	if err := service.ProcessProgress(ctx, makeProgress("agent-2")); err != nil {
		t.Fatalf("second ProcessProgress returned error: %v", err)
	}

	firstID := "sample-uuid:same-run-id"
	secondID := "sample-uuid:same-run-id:agent-2"
	if _, ok := db.workflows[firstID]; !ok {
		t.Fatalf("first attempt was not stored under the UUID-qualified key: keys=%v", mapKeys(db.workflows))
	}
	if _, ok := db.workflows[secondID]; !ok {
		t.Fatalf("second attempt was not isolated under the agent-qualified key: keys=%v", mapKeys(db.workflows))
	}
	if len(db.workflows) != 2 {
		t.Fatalf("same workflow ID across attempts should create two records, got %d", len(db.workflows))
	}

	if err := service.ProcessOutput(ctx, &model.WorkflowOutputRequest{
		UUID:        "sample-uuid",
		WorkflowID:  "same-run-id",
		AgentID:     "agent-2",
		OutputsJSON: `{"attempt":"second"}`,
	}); err != nil {
		t.Fatalf("second attempt output returned error: %v", err)
	}
	if got := db.workflows[secondID].OutputsJSON; got != `{"attempt":"second"}` {
		t.Fatalf("second attempt output was not written to its own record: %q", got)
	}
	if got := db.workflows[firstID].OutputsJSON; got != "" {
		t.Fatalf("second attempt output contaminated first attempt: %q", got)
	}

	if err := service.MarkArchived(ctx, &model.ArchiveResult{
		UUID:        "sample-uuid",
		WorkflowID:  "same-run-id",
		AgentID:     "agent-2",
		ArchiveBase: "archive-second",
	}); err != nil {
		t.Fatalf("second attempt archive returned error: %v", err)
	}
	if !db.workflows[secondID].Archived {
		t.Fatalf("second attempt was not archived")
	}
	if db.workflows[firstID].Archived {
		t.Fatalf("second attempt archive contaminated first attempt")
	}

	if err := service.ProcessProgress(ctx, makeProgress("agent-2")); err != nil {
		t.Fatalf("follow-up progress for second attempt returned error: %v", err)
	}
	if db.workflows[firstID].Archived {
		t.Fatalf("follow-up progress changed first attempt archive state")
	}
	if !db.workflows[secondID].Archived || db.workflows[secondID].ArchiveBase != "archive-second" {
		t.Fatalf("follow-up progress did not preserve second attempt archive state: %+v", db.workflows[secondID])
	}
}

func TestProcessProgressPreservesArchiveFieldsOnUpdate(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)
	archivedAt := time.Now().Add(-time.Minute)
	db.workflows["run-1"] = &model.Workflow{
		ID:                 "run-1",
		UUID:               "sample-uuid",
		Name:               "Workflow",
		Status:             model.WorkflowStatusSuccess,
		Archived:           true,
		ArchivedAt:         &archivedAt,
		ArchiveBase:        "https://bucket.cos.ap-guangzhou.myqcloud.com/prefix",
		BasePath:           "https://bucket.cos.ap-guangzhou.myqcloud.com/prefix",
		OutputsResolvedKey: "sample-uuid/outputs.resolved.json",
		ObjectPrefix:       "sample-uuid",
		KeyPrefix:          "sample-uuid",
		ArchivedCount:      7,
	}

	progress := &model.WorkflowProgress{
		AgentID: "agent-1",
		UUID:    "sample-uuid",
		Workflow: model.Workflow{
			ID:     "run-1",
			Name:   "Workflow",
			Status: model.WorkflowStatusSuccess,
		},
	}

	if err := service.ProcessProgress(ctx, progress); err != nil {
		t.Fatalf("ProcessProgress returned error: %v", err)
	}
	workflow := db.workflows["run-1"]
	if !workflow.Archived || workflow.ArchivedAt == nil {
		t.Fatalf("archive marker was not preserved: %+v", workflow)
	}
	if workflow.OutputsResolvedKey != "sample-uuid/outputs.resolved.json" || workflow.ArchivedCount != 7 {
		t.Fatalf("archive manifest was not preserved: %+v", workflow)
	}
}

func TestProcessProgressPreservesExistingOutputsJSONWhenProgressOmitsIt(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)
	db.workflows["run-1"] = &model.Workflow{
		ID:          "run-1",
		UUID:        "sample-uuid",
		Name:        "Workflow",
		Status:      model.WorkflowStatusSuccess,
		OutputsJSON: `{"bam":"/data/output/sample-uuid/run-1/result.bam"}`,
		CreatedAt:   time.Now().Add(-time.Minute),
	}

	progress := &model.WorkflowProgress{
		AgentID: "agent-1",
		UUID:    "sample-uuid",
		Workflow: model.Workflow{
			ID:     "run-1",
			Name:   "Workflow",
			Status: model.WorkflowStatusSuccess,
		},
	}

	if err := service.ProcessProgress(ctx, progress); err != nil {
		t.Fatalf("ProcessProgress returned error: %v", err)
	}
	if got := db.workflows["run-1"].OutputsJSON; got != `{"bam":"/data/output/sample-uuid/run-1/result.bam"}` {
		t.Fatalf("outputs_json was not preserved: %q", got)
	}
}

func TestMarkArchivedNormalizesAliases(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)
	db.workflows["run-1"] = &model.Workflow{
		ID:        "run-1",
		UUID:      "sample-uuid",
		Name:      "Workflow",
		Status:    model.WorkflowStatusSuccess,
		CreatedAt: time.Now(),
	}

	err := service.MarkArchived(ctx, &model.ArchiveResult{
		UUID:      "sample-uuid",
		BasePath:  "cos-base",
		KeyPrefix: "sample-uuid",
	})
	if err != nil {
		t.Fatalf("MarkArchived returned error: %v", err)
	}
	if db.archived.ArchiveBase != "cos-base" || db.archived.BasePath != "cos-base" {
		t.Fatalf("archive base aliases were not normalized: %+v", db.archived)
	}
	if db.archived.ObjectPrefix != "sample-uuid" || db.archived.KeyPrefix != "sample-uuid" {
		t.Fatalf("key prefix aliases were not normalized: %+v", db.archived)
	}
}

func TestMarkArchivedTargetsWorkflowID(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)

	db.workflows["run-1"] = &model.Workflow{
		ID:        "run-1",
		UUID:      "sample-uuid",
		AgentID:   "agent-old",
		Name:      "Workflow",
		Status:    model.WorkflowStatusSuccess,
		CreatedAt: time.Now().Add(-time.Hour),
	}
	db.workflows["run-2"] = &model.Workflow{
		ID:        "run-2",
		UUID:      "sample-uuid",
		AgentID:   "agent-current",
		Name:      "Workflow",
		Status:    model.WorkflowStatusRunning,
		CreatedAt: time.Now(),
	}

	err := service.MarkArchived(ctx, &model.ArchiveResult{
		UUID:               "sample-uuid",
		WorkflowID:         "run-1",
		ArchiveBase:        "archive-base",
		OutputsResolvedKey: "sample-uuid/outputs.resolved.json",
		ObjectPrefix:       "sample-uuid",
		ArchivedCount:      5,
	})
	if err != nil {
		t.Fatalf("MarkArchived returned error: %v", err)
	}

	if !db.workflows["run-1"].Archived {
		t.Fatalf("target workflow was not marked archived: %+v", db.workflows["run-1"])
	}
	if db.workflows["run-2"].Archived {
		t.Fatalf("latest workflow was incorrectly marked archived: %+v", db.workflows["run-2"])
	}
}

func TestProcessOutputRejectsUnknownWorkflow(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)

	err := service.ProcessOutput(ctx, &model.WorkflowOutputRequest{
		UUID:        "sample-uuid",
		WorkflowID:  "run-1",
		OutputsJSON: `{"ok":true}`,
	})
	if err != ErrWorkflowNotFound {
		t.Fatalf("expected ErrWorkflowNotFound, got %v", err)
	}
}

func TestProcessOutputUsesAgentWhenWorkflowIDIsOmitted(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)
	old := time.Now().Add(-time.Hour)
	newer := time.Now()
	db.workflows["old-run"] = &model.Workflow{ID: "old-run", UUID: "sample-uuid", AgentID: "agent-old", CreatedAt: old}
	db.workflows["new-run"] = &model.Workflow{ID: "new-run", UUID: "sample-uuid", AgentID: "agent-new", CreatedAt: newer}

	err := service.ProcessOutput(ctx, &model.WorkflowOutputRequest{
		UUID: "sample-uuid", AgentID: "agent-old", OutputsJSON: `{"result":"old"}`,
	})
	if err != nil {
		t.Fatalf("ProcessOutput returned error: %v", err)
	}
	if got := db.workflows["old-run"].OutputsJSON; got != `{"result":"old"}` {
		t.Fatalf("agent-bound output was written to the wrong execution: %q", got)
	}
	if got := db.workflows["new-run"].OutputsJSON; got != "" {
		t.Fatalf("newer execution was modified by an old agent: %q", got)
	}
}

func TestProcessOutputWithWorkflowIDRejectsAnotherAgent(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)
	db.workflows["run-1"] = &model.Workflow{ID: "run-1", UUID: "sample-uuid", AgentID: "agent-current", CreatedAt: time.Now()}

	err := service.ProcessOutput(ctx, &model.WorkflowOutputRequest{
		UUID: "sample-uuid", WorkflowID: "run-1", AgentID: "agent-old", OutputsJSON: `{"result":"old"}`,
	})
	if err == nil {
		t.Fatal("accepted output from another execution agent")
	}
	if db.workflows["run-1"].OutputsJSON != "" {
		t.Fatal("mismatched agent modified the workflow output")
	}
}

func TestMarkArchivedRejectsUnknownWorkflow(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)

	err := service.MarkArchived(ctx, &model.ArchiveResult{
		UUID:       "sample-uuid",
		WorkflowID: "run-1",
	})
	if err != ErrWorkflowNotFound {
		t.Fatalf("expected ErrWorkflowNotFound, got %v", err)
	}
}

func TestMarkArchivedUsesAgentWhenWorkflowIDIsOmitted(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)
	db.workflows["old-run"] = &model.Workflow{ID: "old-run", UUID: "sample-uuid", AgentID: "agent-old", CreatedAt: time.Now().Add(-time.Hour)}
	db.workflows["new-run"] = &model.Workflow{ID: "new-run", UUID: "sample-uuid", AgentID: "agent-new", CreatedAt: time.Now()}

	err := service.MarkArchived(ctx, &model.ArchiveResult{
		UUID: "sample-uuid", AgentID: "agent-old", ArchiveBase: "archive-base", ObjectPrefix: "old-run",
	})
	if err != nil {
		t.Fatalf("MarkArchived returned error: %v", err)
	}
	if !db.workflows["old-run"].Archived {
		t.Fatal("agent-bound archive did not target the matching execution")
	}
	if db.workflows["new-run"].Archived {
		t.Fatal("newer execution was incorrectly marked archived")
	}
}

func TestMarkArchivedWithWorkflowIDRejectsAnotherAgent(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	service := NewWorkflowService(db)
	db.workflows["run-1"] = &model.Workflow{ID: "run-1", UUID: "sample-uuid", AgentID: "agent-current", CreatedAt: time.Now()}

	err := service.MarkArchived(ctx, &model.ArchiveResult{
		UUID: "sample-uuid", WorkflowID: "run-1", AgentID: "agent-old", ArchiveBase: "archive-base", ObjectPrefix: "old-run",
	})
	if err == nil {
		t.Fatal("accepted archive from another execution agent")
	}
	if db.workflows["run-1"].Archived {
		t.Fatal("mismatched agent archived the workflow")
	}
}

func mapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func TestGetWorkflowByAttemptNeverFallsBack(t *testing.T) {
	store := newFakeDB()
	service := NewWorkflowService(store)
	ctx := context.Background()
	store.workflows["old"] = &model.Workflow{ID: "old", UUID: "task", AgentID: "old-agent", CreatedAt: time.Now()}
	store.workflows["current"] = &model.Workflow{ID: "current", UUID: "task", AgentID: "current-agent", CreatedAt: time.Now().Add(-time.Hour)}
	got, err := service.GetWorkflowByAttempt(ctx, "task", "current-agent")
	if err != nil || got == nil || got.ID != "current" {
		t.Fatalf("wrong execution selected: %+v %v", got, err)
	}
	got, err = service.GetWorkflowByAttempt(ctx, "different-task", "current-agent")
	if err != nil || got != nil {
		t.Fatal("cross-task fallback")
	}
	got, err = service.GetWorkflowByAttempt(ctx, "task", "absent-agent")
	if err != nil || got != nil {
		t.Fatal("cross-attempt fallback")
	}
}
