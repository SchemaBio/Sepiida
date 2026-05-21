package service

import (
	"context"
	"log"

	"github.com/SchemaBio/Sepiida/internal/common/db"
	"github.com/SchemaBio/Sepiida/internal/common/model"
)

// WorkflowService handles workflow business logic
type WorkflowService struct {
	db db.Database
}

// NewWorkflowService creates a new workflow service
func NewWorkflowService(db db.Database) *WorkflowService {
	return &WorkflowService{db: db}
}

// ProcessProgress processes progress data from agent
func (s *WorkflowService) ProcessProgress(ctx context.Context, progress *model.WorkflowProgress) error {
	// Check if workflow exists by UUID (prefer UUID lookup)
	existing, err := s.db.GetWorkflowByUUID(ctx, progress.UUID)
	if err != nil {
		return err
	}

	// If not found by UUID, try by ID
	if existing == nil {
		existing, err = s.db.GetWorkflow(ctx, progress.Workflow.ID)
		if err != nil {
			return err
		}
	}

	if existing == nil {
		// Create new workflow
		log.Printf("Creating new workflow: UUID=%s, ID=%s", progress.UUID, progress.Workflow.ID)
		if err := s.db.CreateWorkflow(ctx, &progress.Workflow); err != nil {
			return err
		}
	} else {
		// Update existing workflow
		log.Printf("Updating workflow: UUID=%s, ID=%s", progress.UUID, progress.Workflow.ID)
		progress.Workflow.CreatedAt = existing.CreatedAt
		// Preserve the existing ID if UUID matches but ID changed (new run)
		if existing.UUID == progress.UUID && existing.ID != progress.Workflow.ID {
			// This is a new run for the same UUID, keep using the new ID
			log.Printf("New execution detected for UUID %s: old ID=%s, new ID=%s", progress.UUID, existing.ID, progress.Workflow.ID)
		}
		if err := s.db.UpdateWorkflow(ctx, &progress.Workflow); err != nil {
			return err
		}
	}

	// Process tasks
	for _, task := range progress.Tasks {
		task.UUID = progress.UUID
		task.WorkflowID = progress.Workflow.ID

		existingTask, err := s.db.GetTask(ctx, task.GenerateID())
		if err != nil {
			return err
		}

		if existingTask == nil {
			log.Printf("Creating new task: %s", task.GenerateID())
			if err := s.db.CreateTask(ctx, &task); err != nil {
				return err
			}
		} else {
			log.Printf("Updating task: %s", task.GenerateID())
			task.CreatedAt = existingTask.CreatedAt
			if err := s.db.UpdateTask(ctx, &task); err != nil {
				return err
			}
		}
	}

	return nil
}

// ProcessOutput processes workflow output
func (s *WorkflowService) ProcessOutput(ctx context.Context, req *model.WorkflowOutputRequest) error {
	// Find workflow by UUID
	existing, err := s.db.GetWorkflowByUUID(ctx, req.UUID)
	if err != nil {
		return err
	}

	if existing == nil {
		// Try by ID
		existing, err = s.db.GetWorkflow(ctx, req.WorkflowID)
		if err != nil {
			return err
		}
	}

	if existing == nil {
		log.Printf("Workflow not found for output: UUID=%s, ID=%s", req.UUID, req.WorkflowID)
		return nil
	}

	existing.OutputsJSON = req.OutputsJSON
	return s.db.UpdateWorkflow(ctx, existing)
}

// GetWorkflow retrieves a workflow by ID
func (s *WorkflowService) GetWorkflow(ctx context.Context, id string) (*model.Workflow, error) {
	return s.db.GetWorkflow(ctx, id)
}

// GetWorkflowByUUID retrieves a workflow by UUID
func (s *WorkflowService) GetWorkflowByUUID(ctx context.Context, uuid string) (*model.Workflow, error) {
	return s.db.GetWorkflowByUUID(ctx, uuid)
}

// GetWorkflowTasks retrieves tasks for a workflow
func (s *WorkflowService) GetWorkflowTasks(ctx context.Context, workflowID string) ([]*model.Task, error) {
	return s.db.GetTasksByWorkflow(ctx, workflowID)
}

// ListWorkflows lists workflows with pagination
func (s *WorkflowService) ListWorkflows(ctx context.Context, limit, offset int) ([]*model.Workflow, error) {
	return s.db.ListWorkflows(ctx, limit, offset)
}

// MarkArchived marks a workflow's outputs as archived by UUID
func (s *WorkflowService) MarkArchived(ctx context.Context, uuid string) error {
	return s.db.MarkArchived(ctx, uuid)
}