package handler

import (
	"net/http"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/common/model"
	"github.com/SchemaBio/Sepiida/internal/server/middleware"
	"github.com/SchemaBio/Sepiida/internal/server/service"
)

type AgentHandler struct{ service *service.WorkflowService }

func NewAgentHandler(workflows *service.WorkflowService) *AgentHandler {
	return &AgentHandler{service: workflows}
}

func (h *AgentHandler) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var heartbeat model.AgentHeartbeat
	if !decodeJSONBody(w, r, &heartbeat) {
		return
	}
	if !validateUUID(w, "uuid", heartbeat.UUID) || !validateIdentifier(w, "agent_id", heartbeat.AgentID) {
		return
	}
	if heartbeat.WorkflowID != "" && !validateIdentifier(w, "workflow_id", heartbeat.WorkflowID) {
		return
	}
	claims, ok := middleware.TaskTokenClaims(r.Context())
	if !ok {
		http.Error(w, "task token required", http.StatusUnauthorized)
		return
	}
	if claims.UUID != heartbeat.UUID || claims.AgentID != heartbeat.AgentID || claims.AttemptID != heartbeat.AgentID {
		http.Error(w, "task token attempt mismatch", http.StatusForbidden)
		return
	}
	if heartbeat.CollectionIntervalSeconds < 1 || heartbeat.CollectionIntervalSeconds > 3600 {
		http.Error(w, "invalid collection interval", http.StatusBadRequest)
		return
	}
	if heartbeat.CollectionStatus != "ok" && heartbeat.CollectionStatus != "error" {
		http.Error(w, "invalid collection status", http.StatusBadRequest)
		return
	}
	if heartbeat.CollectionStatus == "error" {
		if heartbeat.ErrorCode != "collection_failed" {
			http.Error(w, "invalid collection error code", http.StatusBadRequest)
			return
		}
	} else if heartbeat.ErrorCode != "" {
		http.Error(w, "error_code is only allowed when collection failed", http.StatusBadRequest)
		return
	}
	if len(heartbeat.AgentVersion) > 100 {
		http.Error(w, "invalid agent version", http.StatusBadRequest)
		return
	}
	if err := h.service.RecordAgentHeartbeat(r.Context(), &heartbeat); err != nil {
		http.Error(w, "failed to record agent heartbeat", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AgentHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uuid := strings.TrimSpace(r.URL.Query().Get("uuid"))
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if !validateUUID(w, "uuid", uuid) || !validateIdentifier(w, "agent_id", agentID) {
		return
	}
	if scope, ok := middleware.QueryKeyScope(r.Context()); ok && scope.Restricted() && !scope.AllowsWorkflow("", uuid) {
		http.Error(w, "query key scope does not allow this workflow", http.StatusForbidden)
		return
	}
	session, err := h.service.GetAgentSession(r.Context(), uuid, agentID)
	if err != nil {
		http.Error(w, "failed to get agent status", http.StatusInternalServerError)
		return
	}
	if session == nil {
		writeJSON(w, http.StatusOK, map[string]any{"state": "legacy/unknown"})
		return
	}
	writeJSON(w, http.StatusOK, session)
}
