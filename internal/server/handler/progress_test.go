package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/apikey"
	"github.com/SchemaBio/Sepiida/internal/common/model"
	"github.com/SchemaBio/Sepiida/internal/common/tasktoken"
	"github.com/SchemaBio/Sepiida/internal/server/middleware"
	"github.com/SchemaBio/Sepiida/internal/server/service"
)

const testTaskTokenSecret = "0123456789abcdef0123456789abcdef"
const testUUID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

func TestHandleListWorkflowsCapsLimit(t *testing.T) {
	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	handler := NewProgressHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows?limit=100000", nil)
	rec := httptest.NewRecorder()

	handler.HandleListWorkflows(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastListLimit != maxWorkflowListLimit {
		t.Fatalf("expected limit to be capped at %d, got %d", maxWorkflowListLimit, fake.lastListLimit)
	}
}

func TestHandleListWorkflowsRejectsScopedQueryKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("scoped-key uuid=workflow-uuid\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
	keyMgr := apikey.NewKeyManager(keyFile)
	if err := keyMgr.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	handler := NewProgressHandler(svc)
	authenticated := middleware.NewQueryAuthMiddleware(keyMgr).Middleware(http.HandlerFunc(handler.HandleListWorkflows))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	req.Header.Set("Authorization", "Bearer scoped-key")
	rec := httptest.NewRecorder()

	authenticated.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden for scoped list, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleProgressRejectsInvalidIdentifier(t *testing.T) {
	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	handler := NewProgressHandler(svc)

	body := `{"agent_id":"agent-1","uuid":"../escape","workflow":{"id":"run-1","name":"Workflow","status":"running"},"tasks":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/progress", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleProgress(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for invalid uuid, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleProgressRejectsNonStandardUUID(t *testing.T) {
	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	handler := NewProgressHandler(svc)

	body := `{"agent_id":"agent-1","uuid":"sample-uuid","workflow":{"id":"run-1","name":"Workflow","status":"running"},"tasks":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/progress", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleProgress(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for non-standard uuid, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleProgressRejectsTrailingJSON(t *testing.T) {
	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	handler := NewProgressHandler(svc)

	body := `{"agent_id":"agent-1","uuid":"sample-uuid","workflow":{"id":"run-1","name":"Workflow","status":"running"},"tasks":[]}{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/progress", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleProgress(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for trailing JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleProgressRejectsInvalidStatusAndTaskIdentifier(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "workflow status",
			body: `{"agent_id":"agent-1","uuid":"` + testUUID + `","workflow":{"id":"run-1","name":"Workflow","status":"done"},"tasks":[]}`,
		},
		{
			name: "task job name",
			body: `{"agent_id":"agent-1","uuid":"` + testUUID + `","workflow":{"id":"run-1","name":"Workflow","status":"running"},"tasks":[{"job_name":"../escape","name":"Task","status":"running"}]}`,
		},
		{
			name: "workflow output dir control char",
			body: `{"agent_id":"agent-1","uuid":"` + testUUID + `","workflow":{"id":"run-1","name":"Workflow","status":"running","output_dir":"bad\u0000path"},"tasks":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &listLimitDB{}
			svc := service.NewWorkflowService(fake)
			handler := NewProgressHandler(svc)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/progress", bytes.NewBufferString(tt.body))
			rec := httptest.NewRecorder()

			handler.HandleProgress(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected bad request, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleArchiveRejectsOversizedMetadata(t *testing.T) {
	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	handler := NewProgressHandler(svc)

	body := `{"agent_id":"agent-1","uuid":"` + testUUID + `","workflow_id":"run-1","archived_count":-1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow/archive", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleArchive(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for invalid archive metadata, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleOutputRejectsInvalidOutputsJSON(t *testing.T) {
	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	handler := NewProgressHandler(svc)

	body := `{"agent_id":"agent-1","uuid":"` + testUUID + `","workflow_id":"run-1","outputs_json":"not-json"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow/output", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.HandleOutput(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for invalid outputs_json, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetWorkflowWithScopedKeyDoesNotLeakUnscopedMisses(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("scoped-key workflow=allowed-run\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
	keyMgr := apikey.NewKeyManager(keyFile)
	if err := keyMgr.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	progressHandler := NewProgressHandler(svc)
	authenticated := middleware.NewQueryAuthMiddleware(keyMgr).Middleware(http.HandlerFunc(progressHandler.HandleGetWorkflow))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflow?id=other-run", nil)
	req.Header.Set("Authorization", "Bearer scoped-key")
	rec := httptest.NewRecorder()

	authenticated.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden for unscoped miss, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetWorkflowWithScopedKeyReturnsNotFoundForScopedMiss(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("scoped-key workflow=allowed-run\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
	keyMgr := apikey.NewKeyManager(keyFile)
	if err := keyMgr.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	progressHandler := NewProgressHandler(svc)
	authenticated := middleware.NewQueryAuthMiddleware(keyMgr).Middleware(http.HandlerFunc(progressHandler.HandleGetWorkflow))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflow?id=allowed-run", nil)
	req.Header.Set("Authorization", "Bearer scoped-key")
	rec := httptest.NewRecorder()

	authenticated.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected not found for scoped miss, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleProgressPopulatesAgentIDFromTaskToken(t *testing.T) {
	token, err := tasktoken.GenerateForWorkflow(testTaskTokenSecret, testUUID, "agent-1", "run-1", time.Hour)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	progressHandler := NewProgressHandler(svc)
	authenticated := middleware.NewAgentAuthMiddleware(apikey.NewKeyManager(filepath.Join(t.TempDir(), "missing.txt")), testTaskTokenSecret, false).
		Middleware(http.HandlerFunc(progressHandler.HandleProgress))

	payload := model.WorkflowProgress{
		UUID: testUUID,
		Workflow: model.Workflow{
			ID:     "run-1",
			Name:   "Workflow",
			Status: model.WorkflowStatusRunning,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/progress", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	authenticated.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastWorkflow == nil || fake.lastWorkflow.AgentID != "agent-1" {
		t.Fatalf("expected agent_id to be populated from task token, got %+v", fake.lastWorkflow)
	}
}

func TestHandleProgressRejectsTaskTokenWorkflowMismatch(t *testing.T) {
	token, err := tasktoken.GenerateForWorkflow(testTaskTokenSecret, testUUID, "agent-1", "run-1", time.Hour)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	fake := &listLimitDB{}
	svc := service.NewWorkflowService(fake)
	progressHandler := NewProgressHandler(svc)
	authenticated := middleware.NewAgentAuthMiddleware(apikey.NewKeyManager(filepath.Join(t.TempDir(), "missing.txt")), testTaskTokenSecret, false).
		Middleware(http.HandlerFunc(progressHandler.HandleProgress))

	payload := model.WorkflowProgress{
		UUID:    testUUID,
		AgentID: "agent-1",
		Workflow: model.Workflow{
			ID:     "run-2",
			Name:   "Workflow",
			Status: model.WorkflowStatusRunning,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/progress", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	authenticated.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected workflow mismatch to be forbidden, got %d body=%s", rec.Code, rec.Body.String())
	}
}

type listLimitDB struct {
	lastListLimit int
	lastWorkflow  *model.Workflow
}

func (f *listLimitDB) Initialize(ctx context.Context) error { return nil }
func (f *listLimitDB) Close() error                         { return nil }
func (f *listLimitDB) CreateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	cp := *workflow
	f.lastWorkflow = &cp
	return nil
}
func (f *listLimitDB) UpdateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	cp := *workflow
	f.lastWorkflow = &cp
	return nil
}
func (f *listLimitDB) MarkArchived(ctx context.Context, result *model.ArchiveResult) error {
	return nil
}
func (f *listLimitDB) GetWorkflow(ctx context.Context, id string) (*model.Workflow, error) {
	return nil, nil
}
func (f *listLimitDB) GetWorkflowByUUID(ctx context.Context, uuid string) (*model.Workflow, error) {
	return nil, nil
}
func (f *listLimitDB) GetWorkflowsByAgent(ctx context.Context, agentID string) ([]*model.Workflow, error) {
	return nil, nil
}
func (f *listLimitDB) ListWorkflows(ctx context.Context, limit, offset int) ([]*model.Workflow, error) {
	f.lastListLimit = limit
	return nil, nil
}
func (f *listLimitDB) CreateTask(ctx context.Context, task *model.Task) error {
	return nil
}
func (f *listLimitDB) UpdateTask(ctx context.Context, task *model.Task) error {
	return nil
}
func (f *listLimitDB) GetTask(ctx context.Context, id string) (*model.Task, error) {
	return nil, nil
}
func (f *listLimitDB) GetTasksByWorkflow(ctx context.Context, workflowID string) ([]*model.Task, error) {
	return nil, nil
}
