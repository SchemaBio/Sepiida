package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SchemaBio/Sepiida/internal/common/model"
	"github.com/SchemaBio/Sepiida/internal/server/service"
)

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

type listLimitDB struct {
	lastListLimit int
}

func (f *listLimitDB) Initialize(ctx context.Context) error { return nil }
func (f *listLimitDB) Close() error                         { return nil }
func (f *listLimitDB) CreateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	return nil
}
func (f *listLimitDB) UpdateWorkflow(ctx context.Context, workflow *model.Workflow) error {
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
