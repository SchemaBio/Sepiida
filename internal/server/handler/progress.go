package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

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
)

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
	if !authorizeTaskToken(w, r, progress.UUID, progress.AgentID) {
		return
	}

	if err := h.service.ProcessProgress(r.Context(), &progress); err != nil {
		http.Error(w, "failed to process progress", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "uuid": progress.UUID})
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
	if !authorizeTaskToken(w, r, req.UUID, req.AgentID) {
		return
	}

	if err := h.service.ProcessOutput(r.Context(), &req); err != nil {
		http.Error(w, "failed to process output", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "uuid": req.UUID})
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
		// Prefer UUID lookup
		workflow, err = h.service.GetWorkflowByUUID(r.Context(), uuid)
	} else if id != "" {
		workflow, err = h.service.GetWorkflow(r.Context(), id)
	} else {
		http.Error(w, "missing workflow id or uuid", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "failed to get workflow", http.StatusInternalServerError)
		return
	}

	if workflow == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workflow not found"})
		return
	}
	if !authorizeQueryWorkflow(w, r, workflow) {
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"workflow": workflow})
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
	if !h.authorizeQueryWorkflowID(w, r, id) {
		return
	}

	tasks, err := h.service.GetWorkflowTasks(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to get tasks", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
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
	if !authorizeTaskToken(w, r, req.UUID, req.AgentID) {
		return
	}

	if err := h.service.MarkArchived(r.Context(), &req); err != nil {
		log.Printf("Failed to mark archived for UUID %s: %v", req.UUID, err)
		http.Error(w, "failed to mark archived", http.StatusInternalServerError)
		return
	}

	log.Printf("Workflow archived: UUID=%s, outputs_resolved_key=%s, archived_count=%d", req.UUID, req.OutputsResolvedKey, req.ArchivedCount)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "uuid": req.UUID})
}

func authorizeTaskToken(w http.ResponseWriter, r *http.Request, uuid, agentID string) bool {
	claims, ok := middleware.TaskTokenClaims(r.Context())
	if !ok {
		return true
	}
	if claims.UUID != uuid {
		http.Error(w, "task token uuid mismatch", http.StatusForbidden)
		return false
	}
	if agentID != "" && claims.AgentID != agentID {
		http.Error(w, "task token agent_id mismatch", http.StatusForbidden)
		return false
	}
	return true
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

func authorizeQueryWorkflow(w http.ResponseWriter, r *http.Request, workflow *model.Workflow) bool {
	scope, ok := middleware.QueryKeyScope(r.Context())
	if !ok || !scope.Restricted() || workflow == nil {
		return true
	}
	if scope.AllowsWorkflow(workflow.ID, workflow.UUID) {
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

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"workflows": workflows,
		"total":     len(workflows),
	})
}
