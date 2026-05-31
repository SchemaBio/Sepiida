package collector

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/agent/parser"
	"github.com/SchemaBio/Sepiida/internal/agent/pathsafe"
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
		executionDir, err := resolveSymlink(lastSymlink, uuidDir)
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
				stdout, stderr := readTaskLogs(task.OutputDir, executionDir)
				tasks[i].Stdout = stdout
				tasks[i].Stderr = stderr
			}
		}

		// Load previous state to check if outputs need resolving
		prevState, _ := c.stateMgr.LoadState(uuidDir)

		sameExecution := prevState != nil && prevState.ExecutionDir == executionDir && prevState.WorkflowID == workflow.ID

		// Read outputs.json if workflow is done and this concrete execution has
		// not pushed outputs yet. A UUID can point _LAST at a new execution.
		if workflow.Status == model.WorkflowStatusSuccess && (!sameExecution || !prevState.OutputsPushed) {
			outputsFile := filepath.Join(executionDir, "outputs.json")
			if resolved, err := resolveOutputsJSON(outputsFile, executionDir); err == nil {
				workflow.OutputsJSON = resolved
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

// resolveSymlink resolves a _LAST link, directory, or file pointer to a
// concrete execution directory that must stay inside rootDir.
func resolveSymlink(symlinkPath string, rootDir string) (string, error) {
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		return "", err
	}

	target := symlinkPath
	if info.Mode()&os.ModeSymlink != 0 {
		target, err = os.Readlink(symlinkPath)
		if err != nil {
			return "", err
		}
	} else if !info.IsDir() {
		data, err := os.ReadFile(symlinkPath)
		if err != nil {
			return "", err
		}
		target = strings.TrimSpace(string(data))
		if target == "" {
			return "", fmt.Errorf("_LAST file is empty")
		}
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(symlinkPath), target)
	}

	executionDir, err := pathsafe.ResolveExistingWithin(rootDir, target)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(executionDir); err != nil || !info.IsDir() {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("_LAST target is not a directory")
	}
	return executionDir, nil
}

// MarkOutputsPushed marks that outputs.json has been successfully pushed
func (c *ProgressCollector) MarkOutputsPushed(uuidDir string) error {
	return c.stateMgr.MarkOutputsPushed(uuidDir)
}

// LoadState loads the workflow state for a UUID directory.
func (c *ProgressCollector) LoadState(uuidDir string) (*state.WorkflowState, error) {
	return c.stateMgr.LoadState(uuidDir)
}

// MarkArchived marks that the workflow has been archived.
func (c *ProgressCollector) MarkArchived(uuidDir string) error {
	return c.stateMgr.MarkArchived(uuidDir)
}

// readTaskLogs reads stdout and stderr from task directory.
func readTaskLogs(dir string, executionDir string) (string, string) {
	var stdout, stderr string

	taskDir, err := pathsafe.ResolveExistingWithin(executionDir, dir)
	if err != nil {
		return stdout, stderr
	}

	stdoutFile := filepath.Join(taskDir, "stdout.txt")
	if data, err := os.ReadFile(stdoutFile); err == nil {
		stdout = string(data)
	}

	stderrFile := filepath.Join(taskDir, "stderr.txt")
	if data, err := os.ReadFile(stderrFile); err == nil {
		stderr = string(data)
	}

	return stdout, stderr
}

// resolveOutputsJSON reads outputs.json and recursively resolves file path values.
// If a value is an absolute path to an existing file whose content is valid JSON,
// the value is replaced with the parsed JSON content. This handles the case where
// outputs.json points to a tmp file that contains the actual output manifest.
func resolveOutputsJSON(outputsPath string, executionDir string) (string, error) {
	outputsRealPath, err := pathsafe.ResolveExistingWithin(executionDir, outputsPath)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(outputsRealPath)
	if err != nil {
		return "", err
	}

	resolved, changed := resolveJSONValues(data, executionDir)
	if !changed {
		return string(data), nil
	}

	// Marshal back to get a clean JSON string
	out, err := json.Marshal(resolved)
	if err != nil {
		return string(data), nil
	}
	return string(out), nil
}

// resolveJSONValues walks a parsed JSON structure and replaces any string value
// that is a file path pointing to a JSON file with the parsed content of that file.
// Returns the resolved value and whether any substitution was made.
func resolveJSONValues(data []byte, executionDir string) (interface{}, bool) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}

	return resolveValue(raw, executionDir)
}

// resolveValue recursively resolves a JSON value.
func resolveValue(v interface{}, executionDir string) (interface{}, bool) {
	switch val := v.(type) {
	case string:
		return resolveString(val, executionDir)
	case map[string]interface{}:
		changed := false
		for k, child := range val {
			resolved, childChanged := resolveValue(child, executionDir)
			if childChanged {
				val[k] = resolved
				changed = true
			}
		}
		return val, changed
	case []interface{}:
		changed := false
		for i, child := range val {
			resolved, childChanged := resolveValue(child, executionDir)
			if childChanged {
				val[i] = resolved
				changed = true
			}
		}
		return val, changed
	default:
		return v, false
	}
}

// resolveString checks if a string is a file path and resolves it.
// For all file paths: symlinks are resolved to real paths.
// For JSON files: content is parsed and recursively resolved.
func resolveString(s string, executionDir string) (interface{}, bool) {
	if !pathsafe.IsAbsoluteLocalPath(s) {
		return s, false
	}

	realPath, err := pathsafe.ResolveExistingWithin(executionDir, s)
	if err != nil {
		return s, false
	}

	info, err := os.Stat(realPath)
	if err != nil || info.IsDir() {
		// Path resolved but not a file — return resolved path if it changed
		if realPath != filepath.Clean(s) {
			return realPath, true
		}
		return s, false
	}

	// Try reading as JSON for recursive resolution
	data, err := os.ReadFile(realPath)
	if err != nil {
		// Can't read, but still return resolved path if symlink changed
		if realPath != filepath.Clean(s) {
			return realPath, true
		}
		return s, false
	}

	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		// Not JSON — return resolved path if symlink changed
		if realPath != filepath.Clean(s) {
			log.Printf("Resolved symlink: %s -> %s", s, realPath)
			return realPath, true
		}
		return s, false
	}

	// JSON file — recursively resolve its contents
	resolved, _ := resolveValue(parsed, executionDir)
	log.Printf("Resolved outputs reference: %s -> %s", s, realPath)
	return resolved, true
}
