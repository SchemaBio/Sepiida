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
	ID          string         `json:"id"`           // Workflow run ID (from directory name: YYYYMMDD_HHMMSS_WorkflowName)
	UUID        string         `json:"uuid"`         // Sample UUID (parent directory name)
	Name        string         `json:"name"`         // Workflow name
	Status      WorkflowStatus `json:"status"`       // Current status
	StartTime   *time.Time     `json:"start_time"`   // Start timestamp
	EndTime     *time.Time     `json:"end_time"`     // End timestamp
	OutputDir   string         `json:"output_dir"`   // Output directory path (full path to execution dir)
	OutputsJSON string         `json:"outputs_json"` // outputs.json content
	AgentID     string         `json:"agent_id"`     // Agent identifier
	CreatedAt   time.Time      `json:"created_at"`   // Record creation time
	UpdatedAt   time.Time      `json:"updated_at"`   // Record update time
}

// WorkflowProgress represents the progress data sent by Agent
type WorkflowProgress struct {
	AgentID  string   `json:"agent_id"`
	UUID     string   `json:"uuid"`           // Sample UUID
	Workflow Workflow `json:"workflow"`
	Tasks    []Task   `json:"tasks"`
}

// WorkflowOutputRequest represents the output data sent by Agent
type WorkflowOutputRequest struct {
	WorkflowID  string `json:"workflow_id"`
	UUID        string `json:"uuid"`           // Sample UUID
	OutputsJSON string `json:"outputs_json"`
}