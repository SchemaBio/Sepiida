package parser

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/model"
)

// LogParser parses MiniWDL workflow.log files
type LogParser struct {
	workflowID string
}

// NewLogParser creates a new log parser
func NewLogParser() *LogParser {
	return &LogParser{}
}

// ParseWorkflowStart parses workflow start line
// Format: 2026-04-28 09:49:55.697 wdl.w:SingleWES NOTICE workflow start :: name: "SingleWES", source: "...", dir: "/mnt/data/output/20260428_094955_SingleWES"
func (p *LogParser) ParseWorkflowStart(line string) (*model.Workflow, error) {
	pattern := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+) wdl\.w:([^\s]+) NOTICE workflow start :: name: "([^"]+)", source: "([^"]+)", .*dir: "([^"]+)"`)
	matches := pattern.FindStringSubmatch(line)

	if len(matches) < 6 {
		return nil, fmt.Errorf("invalid workflow start format")
	}

	startTime, err := parseTimestamp(matches[1])
	if err != nil {
		return nil, err
	}

	// Extract workflow ID from directory name
	dir := matches[5]
	workflowID := filepath.Base(filepath.Clean(dir))
	p.workflowID = workflowID

	workflow := &model.Workflow{
		ID:        workflowID,
		Name:      matches[3],
		Status:    model.WorkflowStatusRunning,
		StartTime: startTime,
		OutputDir: dir,
	}

	return workflow, nil
}

// ParseTaskSetup parses task setup line
// Format: 2026-04-28 09:49:55.708 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE task setup :: name: "CreateMitoBed", source: "...", dir: "..."
func (p *LogParser) ParseTaskSetup(line string) (*model.Task, error) {
	pattern := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+) wdl\.w:[^\s]+\.t:(call-[^\s]+) NOTICE task setup :: name: "([^"]+)", .*dir: "([^"]+)"`)
	matches := pattern.FindStringSubmatch(line)

	if len(matches) < 5 {
		return nil, fmt.Errorf("invalid task setup format")
	}

	task := &model.Task{
		WorkflowID: p.workflowID,
		Name:       matches[3],
		JobName:    matches[2],
		Status:     model.TaskStatusPending,
		OutputDir:  matches[4],
	}

	return task, nil
}

// ParseTaskRunning parses task running line
// Format: 2026-04-28 09:49:59.280 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE docker task running :: service: "...", task: "...", message: "started"
func (p *LogParser) ParseTaskRunning(line string) (*TaskEvent, error) {
	pattern := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+) wdl\.w:[^\s]+\.t:(call-[^\s]+) NOTICE docker task running`)
	matches := pattern.FindStringSubmatch(line)

	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid task running format")
	}

	startTime, err := parseTimestamp(matches[1])
	if err != nil {
		return nil, err
	}

	event := &TaskEvent{
		JobName:   matches[2],
		EventType: EventTypeRunning,
		Time:      startTime,
	}

	return event, nil
}

// ParseTaskExit parses task exit line
// Format: 2026-04-28 09:50:00.334 wdl.w:SingleWES.t:call-CreateMitoBed NOTICE docker task exit :: state: "complete", exit_code: 0
func (p *LogParser) ParseTaskExit(line string) (*TaskEvent, error) {
	pattern := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+) wdl\.w:[^\s]+\.t:(call-[^\s]+) NOTICE docker task exit :: state: "([^"]+)", exit_code: (\d+)`)
	matches := pattern.FindStringSubmatch(line)

	if len(matches) < 5 {
		return nil, fmt.Errorf("invalid task exit format")
	}

	endTime, err := parseTimestamp(matches[1])
	if err != nil {
		return nil, err
	}

	exitCode, err := strconv.Atoi(matches[4])
	if err != nil {
		return nil, err
	}

	var status model.TaskStatus
	if matches[3] == "complete" && exitCode == 0 {
		status = model.TaskStatusSuccess
	} else {
		status = model.TaskStatusFailed
	}

	event := &TaskEvent{
		JobName:   matches[2],
		EventType: EventTypeExit,
		Time:      endTime,
		ExitCode:  exitCode,
		Status:    status,
	}

	return event, nil
}

// ParseWorkflowDone parses workflow done line
// Format: 2026-04-28 10:20:05.417 wdl.w:SingleWES NOTICE done
func (p *LogParser) ParseWorkflowDone(line string) (*WorkflowEvent, error) {
	pattern := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+) wdl\.w:([^\s]+) NOTICE done`)
	matches := pattern.FindStringSubmatch(line)

	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid workflow done format")
	}

	endTime, err := parseTimestamp(matches[1])
	if err != nil {
		return nil, err
	}

	event := &WorkflowEvent{
		WorkflowID: p.workflowID,
		EventType:  WorkflowEventTypeDone,
		Time:       endTime,
	}

	return event, nil
}

// ParseLogFile parses entire log file and returns current state
func (p *LogParser) ParseLogFile(filePath string) (*model.Workflow, []model.Task, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	var workflow *model.Workflow
	taskMap := make(map[string]*model.Task)
	taskEvents := make(map[string][]TaskEvent)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Try parsing different event types
		if strings.Contains(line, "workflow start") {
			workflow, err = p.ParseWorkflowStart(line)
			if err != nil {
				continue
			}
		} else if strings.Contains(line, "task setup") {
			task, err := p.ParseTaskSetup(line)
			if err != nil {
				continue
			}
			taskMap[task.JobName] = task
		} else if strings.Contains(line, "docker task running") {
			event, err := p.ParseTaskRunning(line)
			if err != nil {
				continue
			}
			taskEvents[event.JobName] = append(taskEvents[event.JobName], *event)
		} else if strings.Contains(line, "docker task exit") {
			event, err := p.ParseTaskExit(line)
			if err != nil {
				continue
			}
			taskEvents[event.JobName] = append(taskEvents[event.JobName], *event)
		} else if strings.Contains(line, "NOTICE done") && !strings.Contains(line, ".t:") {
			event, err := p.ParseWorkflowDone(line)
			if err != nil {
				continue
			}
			if workflow != nil {
				workflow.Status = model.WorkflowStatusSuccess
				workflow.EndTime = event.Time
			}
		}
	}

	// Apply task events to tasks
	var tasks []model.Task
	for jobName, task := range taskMap {
		events := taskEvents[jobName]
		for _, event := range events {
			switch event.EventType {
			case EventTypeRunning:
				task.Status = model.TaskStatusRunning
				task.StartTime = event.Time
			case EventTypeExit:
				task.EndTime = event.Time
				task.ExitCode = &event.ExitCode
				task.Status = event.Status
			}
		}
		tasks = append(tasks, *task)
	}

	return workflow, tasks, scanner.Err()
}

// EventType represents task event type
type EventType int

const (
	EventTypeRunning EventType = 1
	EventTypeExit    EventType = 2
)

// TaskEvent represents a task event from log
type TaskEvent struct {
	JobName   string
	EventType EventType
	Time      *time.Time
	ExitCode  int
	Status    model.TaskStatus
}

// WorkflowEventType represents workflow event type
type WorkflowEventType int

const (
	WorkflowEventTypeDone WorkflowEventType = 1
)

// WorkflowEvent represents a workflow event from log
type WorkflowEvent struct {
	WorkflowID string
	EventType  WorkflowEventType
	Time       *time.Time
}

// parseTimestamp parses MiniWDL timestamp format
func parseTimestamp(s string) (*time.Time, error) {
	t, err := time.Parse("2006-01-02 15:04:05.000", s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
