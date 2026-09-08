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
	storageWorkflowID, existing, err := s.resolveWorkflowForWrite(ctx, progress.UUID, rawWorkflowID, progress.AgentID)
	if err != nil {
		return err
	}
	progress.Workflow.ID = storageWorkflowID
	if existing != nil && existing.AgentID != "" && existing.AgentID != progress.AgentID {
		return errors.New("workflow belongs to another execution agent")
	}

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
		req.WorkflowID, existing, err = s.resolveWorkflowForWrite(ctx, req.UUID, req.WorkflowID, req.AgentID)
		if err != nil {
			return err
		}
		if existing != nil && existing.AgentID != "" && strings.TrimSpace(req.AgentID) != "" && existing.AgentID != req.AgentID {
			return errors.New("workflow belongs to another execution agent")
		}
		if existing != nil && existing.AgentID == "" && strings.TrimSpace(req.AgentID) != "" {
			existing.AgentID = req.AgentID
		}
	} else if strings.TrimSpace(req.AgentID) != "" {
		// A task token always binds the writer to one execution agent.  Do not
		// fall back to the newest workflow for the UUID when a legacy sender
		// omitted workflow_id; another attempt may have become newer while this
		// callback was in flight.
		existing, err = s.GetWorkflowByAttempt(ctx, req.UUID, req.AgentID)
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
		workflowID, existing, err := s.resolveWorkflowForWrite(ctx, result.UUID, result.WorkflowID, result.AgentID)
		if err != nil {
			return err
		}
		if existing == nil {
			return ErrWorkflowNotFound
		}
		if existing.AgentID != "" && strings.TrimSpace(result.AgentID) != "" && existing.AgentID != result.AgentID {
			return errors.New("workflow belongs to another execution agent")
		}
		result.WorkflowID = workflowID
		return s.db.MarkArchived(ctx, result)
	}

	var existing *model.Workflow
	var err error
	if strings.TrimSpace(result.AgentID) != "" {
		existing, err = s.GetWorkflowByAttempt(ctx, result.UUID, result.AgentID)
	} else {
		existing, err = s.db.GetWorkflowByUUID(ctx, result.UUID)
	}
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrWorkflowNotFound
	}
	result.WorkflowID = existing.ID
	return s.db.MarkArchived(ctx, result)
}

func (s *WorkflowService) resolveWorkflowForWrite(ctx context.Context, uuid string, workflowID string, agentIDs ...string) (string, *model.Workflow, error) {
	agentID := ""
	if len(agentIDs) > 0 {
		agentID = strings.TrimSpace(agentIDs[0])
	}
	storageID := storageWorkflowID(uuid, workflowID)

	existing, err := s.db.GetWorkflow(ctx, storageID)
	if err != nil {
		return storageID, nil, err
	}
	if existing != nil {
		if agentID != "" && existing.AgentID != "" && existing.AgentID != agentID {
			// MiniWDL run IDs normally contain a timestamp, but two attempts can
			// still collide when they start in the same clock tick. Keep the
			// historical UUID-qualified key for the first execution and isolate a
			// later agent under an attempt-qualified key.
			scopedID := scopedStorageWorkflowID(uuid, workflowID, agentID)
			scoped, scopedErr := s.db.GetWorkflow(ctx, scopedID)
			if scopedErr != nil {
				return scopedID, nil, scopedErr
			}
			return scopedID, scoped, nil
		}
		return storageID, existing, nil
	}

	if storageID != workflowID {
		legacy, err := s.db.GetWorkflow(ctx, workflowID)
		if err != nil {
			return storageID, nil, err
		}
		if legacy != nil && legacy.UUID == uuid {
			if agentID != "" && legacy.AgentID != "" && legacy.AgentID != agentID {
				scopedID := scopedStorageWorkflowID(uuid, workflowID, agentID)
				scoped, scopedErr := s.db.GetWorkflow(ctx, scopedID)
				if scopedErr != nil {
					return scopedID, nil, scopedErr
				}
				return scopedID, scoped, nil
			}
			// A legacy row has no durable execution identity.  Only bind the
			// first agent callback when the database can prove that this UUID has
			// exactly one legacy candidate.  If several attempts share the UUID,
			// guessing would attach progress or an archive to the wrong run.
			if legacy.AgentID == "" {
				if lister, ok := s.db.(interface {
					ListWorkflowsByUUID(context.Context, string) ([]*model.Workflow, error)
				}); ok {
					candidates, listErr := lister.ListWorkflowsByUUID(ctx, uuid)
					if listErr != nil {
						return storageID, nil, listErr
					}
					if len(candidates) != 1 || candidates[0] == nil || candidates[0].ID != legacy.ID {
						return storageID, nil, errors.New("legacy workflow execution identity is ambiguous; manual reconciliation required")
					}
				} else {
					return storageID, nil, errors.New("legacy workflow execution identity cannot be verified; manual reconciliation required")
				}
			}
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

func scopedStorageWorkflowID(uuid, workflowID, agentID string) string {
	base := storageWorkflowID(uuid, workflowID)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || strings.HasSuffix(base, workflowStorageIDSeparator+agentID) {
		return base
	}
	return base + workflowStorageIDSeparator + agentID
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

// GetWorkflowByAttempt never falls back to another execution of a task.
func (s *WorkflowService) GetWorkflowByAttempt(ctx context.Context, uuid, agentID string) (*model.Workflow, error) {
	if store, ok := s.db.(interface {
		GetWorkflowByAttempt(context.Context, string, string) (*model.Workflow, error)
	}); ok {
		return store.GetWorkflowByAttempt(ctx, uuid, agentID)
	}
	workflows, err := s.db.GetWorkflowsByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	var latest *model.Workflow
	for _, workflow := range workflows {
		if workflow.UUID == uuid && workflow.AgentID == agentID && (latest == nil || workflow.CreatedAt.After(latest.CreatedAt)) {
			latest = workflow
		}
	}
	return latest, nil
}
