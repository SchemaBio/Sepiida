package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/model"
)

const StateFileName = ".sepiida.json"

// WorkflowState represents the monitoring state of a workflow
type WorkflowState struct {
	UUID            string              `json:"uuid"`
	WorkflowID      string              `json:"workflow_id"`
	WorkflowStatus  model.WorkflowStatus `json:"workflow_status"`
	LastPushedAt    time.Time           `json:"last_pushed_at"`
	OutputsPushed   bool                `json:"outputs_pushed"`
	Archived        bool                `json:"archived"`
	TaskStates      map[string]TaskState `json:"task_states"`
	LogFileSize     int64               `json:"log_file_size"`
	LogFileModTime  time.Time           `json:"log_file_mod_time"`
	ExecutionDir    string              `json:"execution_dir"` // Current execution directory (from _LAST)
}

// TaskState represents the state snapshot of a task
type TaskState struct {
	Name     string          `json:"name"`
	Status   model.TaskStatus `json:"status"`
	ExitCode int             `json:"exit_code"`
}

// StateManager manages workflow monitoring states
type StateManager struct {
	mu sync.RWMutex
}

// NewStateManager creates a new state manager
func NewStateManager() *StateManager {
	return &StateManager{}
}

// LoadState loads state from UUID directory (parent of execution directory)
func (s *StateManager) LoadState(uuidDir string) (*WorkflowState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stateFile := filepath.Join(uuidDir, StateFileName)
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No previous state
		}
		return nil, err
	}

	state := &WorkflowState{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}

	return state, nil
}

// SaveState saves state to UUID directory
func (s *StateManager) SaveState(uuidDir string, state *WorkflowState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stateFile := filepath.Join(uuidDir, StateFileName)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(stateFile, data, 0644)
}

// HasStateChanged checks if workflow state has changed from previous
func (s *StateManager) HasStateChanged(uuidDir string, uuid string, executionDir string, workflow *model.Workflow, tasks []model.Task, logFileInfo os.FileInfo) (bool, *WorkflowState) {
	prevState, err := s.LoadState(uuidDir)
	if err != nil || prevState == nil {
		// No previous state, need to push
		return true, s.newState(nil, uuid, executionDir, workflow, tasks, logFileInfo)
	}

	// Check if execution directory changed (new run started)
	if prevState.ExecutionDir != executionDir {
		// New execution, need to push
		return true, s.newState(prevState, uuid, executionDir, workflow, tasks, logFileInfo)
	}

	// Check if log file has been modified
	if logFileInfo != nil {
		if logFileInfo.Size() != prevState.LogFileSize ||
			logFileInfo.ModTime() != prevState.LogFileModTime {
			// Log file changed, need to push
			return true, s.newState(prevState, uuid, executionDir, workflow, tasks, logFileInfo)
		}
	}

	// Check workflow status change
	if workflow.Status != prevState.WorkflowStatus {
		return true, s.newState(prevState, uuid, executionDir, workflow, tasks, logFileInfo)
	}

	// Check if outputs.json needs to be pushed
	if workflow.Status == model.WorkflowStatusSuccess && !prevState.OutputsPushed {
		return true, s.newState(prevState, uuid, executionDir, workflow, tasks, logFileInfo)
	}

	// Check if outputs need to be archived
	if workflow.Status == model.WorkflowStatusSuccess && !prevState.Archived {
		return true, s.newState(prevState, uuid, executionDir, workflow, tasks, logFileInfo)
	}

	// Check task states
	currentTaskStates := make(map[string]TaskState)
	for _, task := range tasks {
		exitCode := -1
		if task.ExitCode != nil {
			exitCode = *task.ExitCode
		}
		currentTaskStates[task.JobName] = TaskState{
			Name:     task.Name,
			Status:   task.Status,
			ExitCode: exitCode,
		}
	}

	// Compare task counts
	if len(currentTaskStates) != len(prevState.TaskStates) {
		return true, s.newState(prevState, uuid, executionDir, workflow, tasks, logFileInfo)
	}

	// Compare each task state
	for jobName, currentState := range currentTaskStates {
		prevTaskState, exists := prevState.TaskStates[jobName]
		if !exists {
			return true, s.newState(prevState, uuid, executionDir, workflow, tasks, logFileInfo)
		}
		if currentState.Status != prevTaskState.Status ||
			currentState.ExitCode != prevTaskState.ExitCode {
			return true, s.newState(prevState, uuid, executionDir, workflow, tasks, logFileInfo)
		}
	}

	// No changes detected
	return false, prevState
}

// MarkOutputsPushed marks that outputs.json has been pushed
func (s *StateManager) MarkOutputsPushed(uuidDir string) error {
	state, err := s.LoadState(uuidDir)
	if err != nil || state == nil {
		return err
	}

	state.OutputsPushed = true
	state.LastPushedAt = time.Now()
	return s.SaveState(uuidDir, state)
}

// MarkArchived marks that workflow outputs have been archived
func (s *StateManager) MarkArchived(uuidDir string) error {
	state, err := s.LoadState(uuidDir)
	if err != nil || state == nil {
		return err
	}

	state.Archived = true
	return s.SaveState(uuidDir, state)
}

// newState creates a new workflow state, preserving the Archived flag from prevState if applicable
func (s *StateManager) newState(prevState *WorkflowState, uuid string, executionDir string, workflow *model.Workflow, tasks []model.Task, logFileInfo os.FileInfo) *WorkflowState {
	newState := s.createNewState(uuid, executionDir, workflow, tasks, logFileInfo)
	if prevState != nil {
		newState.Archived = prevState.Archived
	}
	return newState
}

// createNewState creates a new workflow state from current data
func (s *StateManager) createNewState(uuid string, executionDir string, workflow *model.Workflow, tasks []model.Task, logFileInfo os.FileInfo) *WorkflowState {
	taskStates := make(map[string]TaskState)
	for _, task := range tasks {
		exitCode := -1
		if task.ExitCode != nil {
			exitCode = *task.ExitCode
		}
		taskStates[task.JobName] = TaskState{
			Name:     task.Name,
			Status:   task.Status,
			ExitCode: exitCode,
		}
	}

	state := &WorkflowState{
		UUID:           uuid,
		WorkflowID:     workflow.ID,
		WorkflowStatus: workflow.Status,
		LastPushedAt:   time.Now(),
		OutputsPushed:  workflow.OutputsJSON != "",
		TaskStates:     taskStates,
		ExecutionDir:   executionDir,
	}

	if logFileInfo != nil {
		state.LogFileSize = logFileInfo.Size()
		state.LogFileModTime = logFileInfo.ModTime()
	}

	return state
}