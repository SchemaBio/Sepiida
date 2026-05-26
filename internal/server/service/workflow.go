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
	progress.Workflow.UUID = progress.UUID
	progress.Workflow.AgentID = progress.AgentID

	// Workflow ID identifies a concrete MiniWDL execution. A UUID can have
	// multiple executions, so UUID lookup must not decide update vs create.
	existing, err := s.db.GetWorkflow(ctx, progress.Workflow.ID)
	if err != nil {
		return err
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
		preserveArchiveFields(&progress.Workflow, existing)
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
	// Prefer workflow ID because a UUID can have multiple executions.
	existing, err := s.db.GetWorkflow(ctx, req.WorkflowID)
	if err != nil {
		return err
	}

	if existing == nil {
		existing, err = s.db.GetWorkflowByUUID(ctx, req.UUID)
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
func (s *WorkflowService) MarkArchived(ctx context.Context, result *model.ArchiveResult) error {
	normalizeArchiveResult(result)
	return s.db.MarkArchived(ctx, result)
}

func preserveArchiveFields(next *model.Workflow, existing *model.Workflow) {
	next.Archived = existing.Archived
	next.ArchivedAt = existing.ArchivedAt
	next.ArchiveBase = existing.ArchiveBase
	next.BasePath = existing.BasePath
	next.OutputsResolvedKey = existing.OutputsResolvedKey
	next.ObjectPrefix = existing.ObjectPrefix
	next.KeyPrefix = existing.KeyPrefix
	next.ArchivedCount = existing.ArchivedCount
}

func normalizeArchiveResult(result *model.ArchiveResult) {
	if result.ArchiveBase == "" {
		result.ArchiveBase = result.BasePath
	}
	if result.BasePath == "" {
		result.BasePath = result.ArchiveBase
	}
	if result.ObjectPrefix == "" {
		result.ObjectPrefix = result.KeyPrefix
	}
	if result.KeyPrefix == "" {
		result.KeyPrefix = result.ObjectPrefix
	}
}
