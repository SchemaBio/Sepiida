package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/common/model"
	"github.com/SchemaBio/Sepiida/internal/server/middleware"
	"github.com/SchemaBio/Sepiida/internal/server/service"
)

// ProgressHandler handles progress HTTP requests
type ProgressHandler struct {
	service *service.WorkflowService
}

const (
	defaultWorkflowListLimit = 100
	maxWorkflowListLimit     = 500
	maxJSONBodyBytes         = 10 << 20
	maxIdentifierBytes       = 255
	maxPathBytes             = 4096
	maxEmbeddedTaskLogBytes  = 1 << 20
	maxTasksPerProgress      = 2000
	maxArchivedCount         = 1000000
)

var safeIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var standardUUIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

// NewProgressHandler creates a new progress handler
func NewProgressHandler(service *service.WorkflowService) *ProgressHandler {
	return &ProgressHandler{service: service}
}

// HandleProgress handles POST /api/v1/progress
func (h *ProgressHandler) HandleProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var progress model.WorkflowProgress
	if !decodeJSONBody(w, r, &progress) {
		return
	}

	if progress.UUID == "" {
		http.Error(w, "missing uuid", http.StatusBadRequest)
		return
	}
	if progress.Workflow.ID == "" {
		http.Error(w, "missing workflow id", http.StatusBadRequest)
		return
	}
	if !validateUUID(w, "uuid", progress.UUID) ||
		!validateIdentifier(w, "agent_id", progress.AgentID) ||
		!validateIdentifier(w, "workflow.id", progress.Workflow.ID) {
		return
	}
	if !authorizeTaskToken(w, r, progress.UUID, &progress.AgentID, &progress.Workflow.ID) {
		return
	}
	if progress.AgentID == "" {
		http.Error(w, "missing agent_id", http.StatusBadRequest)
		return
	}
	if !validateWorkflowProgress(w, &progress) {
		return
	}

	if err := h.service.ProcessProgress(r.Context(), &progress); err != nil {
		http.Error(w, "failed to process progress", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "uuid": progress.UUID})
}

// HandleOutput handles POST /api/v1/workflow/output
func (h *ProgressHandler) HandleOutput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req model.WorkflowOutputRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if req.UUID == "" {
		http.Error(w, "missing uuid", http.StatusBadRequest)
		return
	}
	if !validateUUID(w, "uuid", req.UUID) ||
		!validateIdentifier(w, "agent_id", req.AgentID) ||
		(req.WorkflowID != "" && !validateIdentifier(w, "workflow_id", req.WorkflowID)) {
		return
	}
	if !authorizeTaskToken(w, r, req.UUID, &req.AgentID, &req.WorkflowID) {
		return
	}
	if !validateJSONString(w, "outputs_json", req.OutputsJSON) {
		return
	}

	if err := h.service.ProcessOutput(r.Context(), &req); err != nil {
		if errors.Is(err, service.ErrWorkflowNotFound) {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to process output", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "uuid": req.UUID})
}

// HandleGetWorkflow handles GET /api/v1/workflow?id=xxx
func (h *ProgressHandler) HandleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	uuid := r.URL.Query().Get("uuid")

	var workflow *model.Workflow
	var err error

	if uuid != "" {
		if !validateUUID(w, "uuid", uuid) {
			return
		}
		// Prefer UUID lookup
		workflow, err = h.service.GetWorkflowByUUID(r.Context(), uuid)
	} else if id != "" {
		if !validateIdentifier(w, "id", id) {
			return
		}
		workflow, err = h.service.GetWorkflow(r.Context(), id)
	} else {
		http.Error(w, "missing workflow id or uuid", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "failed to get workflow", http.StatusInternalServerError)
		return
	}

	if !authorizeQueryWorkflowLookup(w, r, workflow, id, uuid) {
		return
	}
	if workflow == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workflow not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"workflow": workflow})
}

// HandleGetWorkflowTasks handles GET /api/v1/workflow/tasks?id=xxx
func (h *ProgressHandler) HandleGetWorkflowTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing workflow id", http.StatusBadRequest)
		return
	}
	if !validateIdentifier(w, "id", id) {
		return
	}
	if !h.authorizeQueryWorkflowID(w, r, id) {
		return
	}

	tasks, err := h.service.GetWorkflowTasks(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to get tasks", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tasks": tasks,
		"total": len(tasks),
	})
}

// HandleArchive handles POST /api/v1/workflow/archive
func (h *ProgressHandler) HandleArchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req model.ArchiveResult
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.UUID == "" {
		http.Error(w, "missing uuid", http.StatusBadRequest)
		return
	}
	if !validateUUID(w, "uuid", req.UUID) ||
		!validateIdentifier(w, "agent_id", req.AgentID) ||
		(req.WorkflowID != "" && !validateIdentifier(w, "workflow_id", req.WorkflowID)) {
		return
	}
	if !authorizeTaskToken(w, r, req.UUID, &req.AgentID, &req.WorkflowID) {
		return
	}
	if !validateArchiveResult(w, &req) {
		return
	}

	if err := h.service.MarkArchived(r.Context(), &req); err != nil {
		if errors.Is(err, service.ErrWorkflowNotFound) {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		log.Printf("Failed to mark archived for UUID %s: %v", req.UUID, err)
		http.Error(w, "failed to mark archived", http.StatusInternalServerError)
		return
	}

	log.Printf("Workflow archived: UUID=%s, outputs_resolved_key=%s, archived_count=%d", req.UUID, req.OutputsResolvedKey, req.ArchivedCount)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "uuid": req.UUID})
}

func authorizeTaskToken(w http.ResponseWriter, r *http.Request, uuid string, agentID *string, workflowID *string) bool {
	claims, ok := middleware.TaskTokenClaims(r.Context())
	if !ok {
		return true
	}
	if claims.UUID != uuid {
		http.Error(w, "task token uuid mismatch", http.StatusForbidden)
		return false
	}
	if agentID != nil && *agentID == "" {
		*agentID = claims.AgentID
	}
	if agentID != nil && *agentID != claims.AgentID {
		http.Error(w, "task token agent_id mismatch", http.StatusForbidden)
		return false
	}
	if agentID != nil && !validateIdentifier(w, "agent_id", *agentID) {
		return false
	}
	if workflowID != nil && claims.WorkflowID != "" {
		if *workflowID == "" {
			*workflowID = claims.WorkflowID
		}
		if *workflowID != claims.WorkflowID {
			http.Error(w, "task token workflow_id mismatch", http.StatusForbidden)
			return false
		}
		if !validateIdentifier(w, "workflow_id", *workflowID) {
			return false
		}
	}
	return true
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			http.Error(w, "request body must contain a single JSON value", http.StatusBadRequest)
			return false
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid trailing request body", http.StatusBadRequest)
		return false
	}
	return true
}

func validateIdentifier(w http.ResponseWriter, field, value string) bool {
	if value == "" {
		return true
	}
	if len(value) > maxIdentifierBytes || !safeIdentifierPattern.MatchString(value) {
		http.Error(w, "invalid "+field, http.StatusBadRequest)
		return false
	}
	return true
}

func validateUUID(w http.ResponseWriter, field, value string) bool {
	if value == "" {
		return true
	}
	if len(value) > maxIdentifierBytes || !standardUUIDPattern.MatchString(value) {
		http.Error(w, "invalid "+field, http.StatusBadRequest)
		return false
	}
	return true
}

func validateWorkflowProgress(w http.ResponseWriter, progress *model.WorkflowProgress) bool {
	if !validWorkflowStatus(progress.Workflow.Status) {
		http.Error(w, "invalid workflow.status", http.StatusBadRequest)
		return false
	}
	if !validateIdentifier(w, "workflow.name", progress.Workflow.Name) {
		return false
	}
	if !validatePathString(w, "workflow.output_dir", progress.Workflow.OutputDir) {
		return false
	}
	if !validateJSONString(w, "workflow.outputs_json", progress.Workflow.OutputsJSON) {
		return false
	}
	if len(progress.Tasks) > maxTasksPerProgress {
		http.Error(w, "too many tasks", http.StatusBadRequest)
		return false
	}
	for i := range progress.Tasks {
		task := &progress.Tasks[i]
		if task.JobName == "" {
			http.Error(w, "missing task.job_name", http.StatusBadRequest)
			return false
		}
		if !validateIdentifier(w, "task.job_name", task.JobName) ||
			!validateIdentifier(w, "task.name", task.Name) {
			return false
		}
		if !validTaskStatus(task.Status) {
			http.Error(w, "invalid task.status", http.StatusBadRequest)
			return false
		}
		if !validatePathString(w, "task.output_dir", task.OutputDir) {
			return false
		}
		if len(task.Stdout) > maxEmbeddedTaskLogBytes || len(task.Stderr) > maxEmbeddedTaskLogBytes {
			http.Error(w, "task log too large", http.StatusBadRequest)
			return false
		}
	}
	return true
}

func validateArchiveResult(w http.ResponseWriter, result *model.ArchiveResult) bool {
	if result.ArchivedCount < 0 || result.ArchivedCount > maxArchivedCount {
		http.Error(w, "invalid archived_count", http.StatusBadRequest)
		return false
	}
	return validatePathString(w, "archive_base", result.ArchiveBase) &&
		validatePathString(w, "base_path", result.BasePath) &&
		validatePathString(w, "outputs_resolved_key", result.OutputsResolvedKey) &&
		validatePathString(w, "object_prefix", result.ObjectPrefix) &&
		validatePathString(w, "key_prefix", result.KeyPrefix)
}

func validWorkflowStatus(status model.WorkflowStatus) bool {
	switch status {
	case model.WorkflowStatusRunning, model.WorkflowStatusSuccess, model.WorkflowStatusFailed:
		return true
	default:
		return false
	}
}

func validTaskStatus(status model.TaskStatus) bool {
	switch status {
	case model.TaskStatusPending, model.TaskStatusRunning, model.TaskStatusSuccess, model.TaskStatusFailed:
		return true
	default:
		return false
	}
}

func validateJSONString(w http.ResponseWriter, field string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if len(value) > maxJSONBodyBytes || !json.Valid([]byte(value)) {
		http.Error(w, "invalid "+field, http.StatusBadRequest)
		return false
	}
	return true
}

func validatePathString(w http.ResponseWriter, field string, value string) bool {
	if value == "" {
		return true
	}
	if len(value) > maxPathBytes || strings.ContainsFunc(value, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) {
		http.Error(w, "invalid "+field, http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func authorizeQueryWorkflowLookup(w http.ResponseWriter, r *http.Request, workflow *model.Workflow, requestedID string, requestedUUID string) bool {
	scope, ok := middleware.QueryKeyScope(r.Context())
	if !ok || !scope.Restricted() {
		return true
	}
	if workflow != nil && scope.AllowsWorkflow(workflow.ID, workflow.UUID) {
		return true
	}
	// When a restricted key requests an unscoped identifier, return the same
	// authorization error whether the workflow exists or not. Otherwise callers
	// could enumerate other tenants' workflow IDs by distinguishing 403/404.
	if workflow == nil && scope.AllowsWorkflow(requestedID, requestedUUID) {
		return true
	}
	http.Error(w, "query key scope does not allow this workflow", http.StatusForbidden)
	return false
}

func (h *ProgressHandler) authorizeQueryList(w http.ResponseWriter, r *http.Request) bool {
	scope, ok := middleware.QueryKeyScope(r.Context())
	if !ok || !scope.Restricted() {
		return true
	}
	http.Error(w, "query key scope does not allow listing workflows", http.StatusForbidden)
	return false
}

func (h *ProgressHandler) authorizeQueryWorkflowID(w http.ResponseWriter, r *http.Request, workflowID string) bool {
	scope, ok := middleware.QueryKeyScope(r.Context())
	if !ok || !scope.Restricted() || scope.AllowsWorkflow(workflowID, "") {
		return true
	}

	workflow, err := h.service.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		http.Error(w, "failed to get workflow", http.StatusInternalServerError)
		return false
	}
	if workflow != nil && scope.AllowsWorkflow(workflow.ID, workflow.UUID) {
		return true
	}

	http.Error(w, "query key scope does not allow this workflow", http.StatusForbidden)
	return false
}

// HandleListWorkflows handles GET /api/v1/workflows
func (h *ProgressHandler) HandleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorizeQueryList(w, r) {
		return
	}

	limit := defaultWorkflowListLimit
	offset := 0

	// Parse query parameters
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > maxWorkflowListLimit {
		limit = maxWorkflowListLimit
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	workflows, err := h.service.ListWorkflows(r.Context(), limit, offset)
	if err != nil {
		http.Error(w, "failed to list workflows", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workflows": workflows,
		"total":     len(workflows),
	})
}
