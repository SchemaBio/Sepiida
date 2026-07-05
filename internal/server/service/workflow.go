package service

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/common/db"
	"github.com/SchemaBio/Sepiida/internal/common/model"
)

const workflowStorageIDSeparator = ":"

// ErrWorkflowNotFound is returned when a write targets a workflow execution
// that has not been reported yet. Returning a typed error lets HTTP handlers
// avoid false-positive "ok" responses for dropped output/archive updates.
var ErrWorkflowNotFound = errors.New("workflow not found")

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
	rawWorkflowID := progress.Workflow.ID
	storageWorkflowID, existing, err := s.resolveWorkflowForWrite(ctx, progress.UUID, rawWorkflowID)
	if err != nil {
		return err
	}
	progress.Workflow.ID = storageWorkflowID

	// Workflow.ID from MiniWDL is only unique inside a sample UUID. Store new
	// executions under a UUID-qualified key to avoid collisions when two samples
	// produce the same run directory name. Existing legacy rows that used the raw
	// run ID are still updated in-place when they belong to the same UUID.

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
		preserveOutputsJSON(&progress.Workflow, existing)
		preserveArchiveFields(&progress.Workflow, existing)
		if err := s.db.UpdateWorkflow(ctx, &progress.Workflow); err != nil {
			return err
		}
	}

	// Process tasks
	for _, task := range progress.Tasks {
		task.UUID = progress.UUID
		task.WorkflowID = storageWorkflowID

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
	var existing *model.Workflow
	var err error
	if req.WorkflowID != "" {
		// Prefer workflow ID because a UUID can have multiple executions. Do not
		// fall back to "latest by UUID" when a concrete workflow was requested:
		// that can attach outputs to the wrong execution.
		req.WorkflowID, existing, err = s.resolveWorkflowForWrite(ctx, req.UUID, req.WorkflowID)
		if err != nil {
			return err
		}
	} else {
		existing, err = s.db.GetWorkflowByUUID(ctx, req.UUID)
		if err != nil {
			return err
		}
	}

	if existing == nil {
		log.Printf("Workflow not found for output: UUID=%s, ID=%s", req.UUID, req.WorkflowID)
		return ErrWorkflowNotFound
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
	if result.WorkflowID != "" {
		workflowID, existing, err := s.resolveWorkflowForWrite(ctx, result.UUID, result.WorkflowID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrWorkflowNotFound
		}
		result.WorkflowID = workflowID
		return s.db.MarkArchived(ctx, result)
	}

	existing, err := s.db.GetWorkflowByUUID(ctx, result.UUID)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrWorkflowNotFound
	}
	result.WorkflowID = existing.ID
	return s.db.MarkArchived(ctx, result)
}

func (s *WorkflowService) resolveWorkflowForWrite(ctx context.Context, uuid string, workflowID string) (string, *model.Workflow, error) {
	storageID := storageWorkflowID(uuid, workflowID)

	existing, err := s.db.GetWorkflow(ctx, storageID)
	if err != nil {
		return storageID, nil, err
	}
	if existing != nil {
		return storageID, existing, nil
	}

	if storageID != workflowID {
		legacy, err := s.db.GetWorkflow(ctx, workflowID)
		if err != nil {
			return storageID, nil, err
		}
		if legacy != nil && legacy.UUID == uuid {
			return workflowID, legacy, nil
		}
	}

	return storageID, nil, nil
}

func storageWorkflowID(uuid string, workflowID string) string {
	uuid = strings.TrimSpace(uuid)
	workflowID = strings.TrimSpace(workflowID)
	if uuid == "" || workflowID == "" || strings.HasPrefix(workflowID, uuid+workflowStorageIDSeparator) {
		return workflowID
	}
	return uuid + workflowStorageIDSeparator + workflowID
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

func preserveOutputsJSON(next *model.Workflow, existing *model.Workflow) {
	if strings.TrimSpace(next.OutputsJSON) == "" {
		next.OutputsJSON = existing.OutputsJSON
	}
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
