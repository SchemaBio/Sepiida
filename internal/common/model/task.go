package model

import "time"

// TaskStatus represents the status of a task
type TaskStatus string

const (
	TaskStatusPending TaskStatus = "pending"
	TaskStatusRunning TaskStatus = "running"
	TaskStatusSuccess TaskStatus = "success"
	TaskStatusFailed  TaskStatus = "failed"
)

// Task represents a MiniWDL task execution
type Task struct {
	ID         string     `json:"id"`           // Task ID (workflow_id + task_name)
	WorkflowID string     `json:"workflow_id"`  // Associated workflow ID
	UUID       string     `json:"uuid"`         // Sample UUID (from parent directory)
	Name       string     `json:"name"`         // Task name (callee name)
	JobName    string     `json:"job_name"`     // Job name (call-XXX)
	Status     TaskStatus `json:"status"`       // Current status
	StartTime  *time.Time `json:"start_time"`   // Start timestamp (docker task running)
	EndTime    *time.Time `json:"end_time"`     // End timestamp (docker task exit)
	ExitCode   *int       `json:"exit_code"`    // Exit code
	OutputDir  string     `json:"output_dir"`   // Task output directory
	Stdout     string     `json:"stdout"`       // stdout log content
	Stderr     string     `json:"stderr"`       // stderr log content
	CreatedAt  time.Time  `json:"created_at"`   // Record creation time
	UpdatedAt  time.Time  `json:"updated_at"`   // Record update time
}

// GenerateID generates a unique task ID from workflow ID and job name
func (t *Task) GenerateID() string {
	return t.WorkflowID + "_" + t.JobName
}