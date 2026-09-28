package model

import "time"

// AgentHeartbeat contains only bounded diagnostic metadata. It must never
// include task tokens, signed object URLs, read data, or raw error messages.
type AgentHeartbeat struct {
	UUID                      string `json:"uuid"`
	AgentID                   string `json:"agent_id"`
	WorkflowID                string `json:"workflow_id,omitempty"`
	AgentVersion              string `json:"agent_version"`
	CollectionIntervalSeconds int    `json:"collection_interval_seconds"`
	CollectionStatus          string `json:"collection_status"`
	ErrorCode                 string `json:"error_code,omitempty"`
}

// AgentSession is the server's view of a single (task UUID, attempt) pair.
type AgentSession struct {
	UUID                      string     `json:"uuid"`
	AgentID                   string     `json:"agent_id"`
	WorkflowID                string     `json:"workflow_id,omitempty"`
	AgentVersion              string     `json:"agent_version,omitempty"`
	CollectionIntervalSeconds int        `json:"collection_interval_seconds,omitempty"`
	LastCollectedAt           *time.Time `json:"last_collected_at,omitempty"`
	LastProgressPushAt        *time.Time `json:"last_progress_push_at,omitempty"`
	LastCollectionStatus      string     `json:"last_collection_status"`
	LastErrorCode             string     `json:"last_error_code,omitempty"`
	CreatedAt                 time.Time  `json:"created_at,omitempty"`
	UpdatedAt                 time.Time  `json:"updated_at,omitempty"`
}
