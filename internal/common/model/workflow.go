package model

import "time"

// WorkflowStatus represents the status of a workflow
type WorkflowStatus string

const (
	WorkflowStatusRunning WorkflowStatus = "running"
	WorkflowStatusSuccess WorkflowStatus = "success"
	WorkflowStatusFailed  WorkflowStatus = "failed"
)

// Workflow represents a MiniWDL workflow execution
type Workflow struct {
	ID                 string         `json:"id"`                             // Workflow run ID (from directory name: YYYYMMDD_HHMMSS_WorkflowName)
	UUID               string         `json:"uuid"`                           // Sample UUID (parent directory name)
	Name               string         `json:"name"`                           // Workflow name
	Status             WorkflowStatus `json:"status"`                         // Current status
	StartTime          *time.Time     `json:"start_time"`                     // Start timestamp
	EndTime            *time.Time     `json:"end_time"`                       // End timestamp
	OutputDir          string         `json:"output_dir"`                     // Output directory path (full path to execution dir)
	OutputsJSON        string         `json:"outputs_json"`                   // outputs.json content
	AgentID            string         `json:"agent_id"`                       // Agent identifier
	Archived           bool           `json:"archived"`                       // Whether outputs have been archived to remote storage
	ArchivedAt         *time.Time     `json:"archived_at,omitempty"`          // When the archive was completed
	ArchiveBase        string         `json:"archive_base,omitempty"`         // Storage base URL/path before object keys
	BasePath           string         `json:"base_path,omitempty"`            // Alias for archive_base
	OutputsResolvedKey string         `json:"outputs_resolved_key,omitempty"` // Key for rewritten outputs.resolved.json
	ObjectPrefix       string         `json:"object_prefix,omitempty"`        // Object key prefix for this workflow
	KeyPrefix          string         `json:"key_prefix,omitempty"`           // Alias for object_prefix
	ArchivedCount      int            `json:"archived_count,omitempty"`       // Number of uploaded archived objects
	CreatedAt          time.Time      `json:"created_at"`                     // Record creation time
	UpdatedAt          time.Time      `json:"updated_at"`                     // Record update time
}

// WorkflowProgress represents the progress data sent by Agent
type WorkflowProgress struct {
	AgentID  string   `json:"agent_id"`
	UUID     string   `json:"uuid"` // Sample UUID
	Workflow Workflow `json:"workflow"`
	Tasks    []Task   `json:"tasks"`
}

// WorkflowOutputRequest represents the output data sent by Agent
type WorkflowOutputRequest struct {
	WorkflowID  string `json:"workflow_id"`
	UUID        string `json:"uuid"` // Sample UUID
	AgentID     string `json:"agent_id,omitempty"`
	OutputsJSON string `json:"outputs_json"`
}

// ArchiveResult is sent by Agent after successfully archiving workflow outputs.
// The alias fields keep the wire format compatible with adjacent services that
// use either archive_base/base_path or object_prefix/key_prefix naming.
type ArchiveResult struct {
	UUID               string `json:"uuid"`
	WorkflowID         string `json:"workflow_id,omitempty"`
	AgentID            string `json:"agent_id,omitempty"`
	ArchiveBase        string `json:"archive_base,omitempty"`
	BasePath           string `json:"base_path,omitempty"`
	OutputsResolvedKey string `json:"outputs_resolved_key,omitempty"`
	ObjectPrefix       string `json:"object_prefix,omitempty"`
	KeyPrefix          string `json:"key_prefix,omitempty"`
	ArchivedCount      int    `json:"archived_count,omitempty"`
}
