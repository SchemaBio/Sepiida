package collector

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/SchemaBio/Sepiida/internal/agent/parser"
	"github.com/SchemaBio/Sepiida/internal/agent/state"
	"github.com/SchemaBio/Sepiida/internal/common/model"
)

// uuidPattern matches standard UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
var uuidPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

// ProgressCollector collects workflow progress from output directories
type ProgressCollector struct {
	parser    *parser.LogParser
	watchDirs []string
	agentID   string
	stateMgr  *state.StateManager
}

// CollectResult represents the result of collection
type CollectResult struct {
	Progress     model.WorkflowProgress
	UUIDDir      string // UUID directory path (where state file is stored)
	ExecutionDir string // Execution directory path (where workflow.log is)
	NeedPush     bool
}

// NewProgressCollector creates a new progress collector
func NewProgressCollector(parser *parser.LogParser, watchDirs []string, agentID string) *ProgressCollector {
	return &ProgressCollector{
		parser:    parser,
		watchDirs: watchDirs,
		agentID:   agentID,
		stateMgr:  state.NewStateManager(),
	}
}

// Collect collects progress from all watched directories
// Returns only workflows that need to be pushed (state changed)
func (c *ProgressCollector) Collect() ([]CollectResult, error) {
	var results []CollectResult
	var err error

	for _, dir := range c.watchDirs {
		results, err = c.collectFromDir(dir, results)
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

// collectFromDir collects progress from a specific directory
// The directory is expected to contain UUID directories
func (c *ProgressCollector) collectFromDir(dir string, results []CollectResult) ([]CollectResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return results, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Validate UUID format
		uuid := entry.Name()
		if !isValidUUID(uuid) {
			continue // Skip non-UUID directories
		}

		uuidDir := filepath.Join(dir, uuid)

		// Check if _LAST symlink exists
		lastSymlink := filepath.Join(uuidDir, "_LAST")
		executionDir, err := resolveSymlink(lastSymlink)
		if err != nil {
			// No _LAST symlink, skip this UUID
			continue
		}

		// Find the workflow.log in the execution directory
		logFile := filepath.Join(executionDir, "workflow.log")
		logFileInfo, err := os.Stat(logFile)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			continue
		}

		// Parse log file
		workflow, tasks, err := c.parser.ParseLogFile(logFile)
		if err != nil || workflow == nil {
			continue
		}

		// Set UUID for workflow
		workflow.UUID = uuid

		// Read stdout/stderr for each task
		for i, task := range tasks {
			task.UUID = uuid
			if task.OutputDir != "" {
				stdout, stderr := readTaskLogs(task.OutputDir)
				tasks[i].Stdout = stdout
				tasks[i].Stderr = stderr
			}
		}

		// Read outputs.json if workflow is done
		if workflow.Status == model.WorkflowStatusSuccess {
			outputsFile := filepath.Join(executionDir, "outputs.json")
			if data, err := os.ReadFile(outputsFile); err == nil {
				workflow.OutputsJSON = string(data)
			}
		}

		// Check if state has changed
		needPush, newState := c.stateMgr.HasStateChanged(uuidDir, uuid, executionDir, workflow, tasks, logFileInfo)

		progress := model.WorkflowProgress{
			AgentID:  c.agentID,
			UUID:     uuid,
			Workflow: *workflow,
			Tasks:    tasks,
		}

		result := CollectResult{
			Progress:     progress,
			UUIDDir:      uuidDir,
			ExecutionDir: executionDir,
			NeedPush:     needPush,
		}

		// Always save current state (even if no push needed)
		if newState != nil {
			if err := c.stateMgr.SaveState(uuidDir, newState); err != nil {
				// Log error but continue
			}
		}

		// Only add to results if needs push
		if needPush {
			results = append(results, result)
		}
	}

	return results, nil
}

// isValidUUID checks if the string is a valid UUID format
func isValidUUID(s string) bool {
	return uuidPattern.MatchString(s)
}

// resolveSymlink resolves a symlink to its target path
func resolveSymlink(symlinkPath string) (string, error) {
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		return "", err
	}

	if info.Mode()&os.ModeSymlink == 0 {
		// Not a symlink, return the path itself
		return symlinkPath, nil
	}

	target, err := os.Readlink(symlinkPath)
	if err != nil {
		return "", err
	}

	// If target is relative, make it absolute
	if !filepath.IsAbs(target) {
		base := filepath.Dir(symlinkPath)
		target = filepath.Join(base, target)
	}

	return target, nil
}

// MarkOutputsPushed marks that outputs.json has been successfully pushed
func (c *ProgressCollector) MarkOutputsPushed(uuidDir string) error {
	return c.stateMgr.MarkOutputsPushed(uuidDir)
}

// readTaskLogs reads stdout and stderr from task directory
func readTaskLogs(dir string) (string, string) {
	var stdout, stderr string

	stdoutFile := filepath.Join(dir, "stdout.txt")
	if data, err := os.ReadFile(stdoutFile); err == nil {
		stdout = string(data)
	}

	stderrFile := filepath.Join(dir, "stderr.txt")
	if data, err := os.ReadFile(stderrFile); err == nil {
		stderr = string(data)
	}

	return stdout, stderr
}