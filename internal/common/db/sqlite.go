package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/model"
	_ "github.com/mattn/go-sqlite3"
)

// SQLite implements Database interface using SQLite
type SQLite struct {
	db *sql.DB
}

// NewSQLite creates a new SQLite database instance
func NewSQLite(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}
	return &SQLite{db: db}, nil
}

// Initialize creates tables
func (s *SQLite) Initialize(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS workflows (
			id TEXT PRIMARY KEY,
			uuid TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			start_time DATETIME,
			end_time DATETIME,
			output_dir TEXT,
			outputs_json TEXT,
			agent_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflows_uuid ON workflows(uuid)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			uuid TEXT NOT NULL,
			name TEXT NOT NULL,
			job_name TEXT NOT NULL,
			status TEXT NOT NULL,
			start_time DATETIME,
			end_time DATETIME,
			exit_code INTEGER,
			output_dir TEXT,
			stdout TEXT,
			stderr TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (workflow_id) REFERENCES workflows(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_uuid ON tasks(uuid)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			key TEXT PRIMARY KEY,
			description TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_active BOOLEAN DEFAULT true
		)`,
	}

	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}
	return nil
}

// Close closes the database connection
func (s *SQLite) Close() error {
	return s.db.Close()
}

// CreateWorkflow creates a new workflow record
func (s *SQLite) CreateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	query := `INSERT INTO workflows (id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	now := time.Now()
	workflow.CreatedAt = now
	workflow.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, query,
		workflow.ID, workflow.UUID, workflow.Name, workflow.Status,
		workflow.StartTime, workflow.EndTime, workflow.OutputDir,
		workflow.OutputsJSON, workflow.AgentID, workflow.CreatedAt, workflow.UpdatedAt)
	return err
}

// UpdateWorkflow updates an existing workflow record
func (s *SQLite) UpdateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	query := `UPDATE workflows SET uuid=?, name=?, status=?, start_time=?, end_time=?, output_dir=?, outputs_json=?, agent_id=?, updated_at=? WHERE id=?`
	workflow.UpdatedAt = time.Now()

	_, err := s.db.ExecContext(ctx, query,
		workflow.UUID, workflow.Name, workflow.Status, workflow.StartTime, workflow.EndTime,
		workflow.OutputDir, workflow.OutputsJSON, workflow.AgentID, workflow.UpdatedAt, workflow.ID)
	return err
}

// GetWorkflow retrieves a workflow by ID
func (s *SQLite) GetWorkflow(ctx context.Context, id string) (*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, created_at, updated_at FROM workflows WHERE id=?`
	row := s.db.QueryRowContext(ctx, query, id)

	workflow := &model.Workflow{}
	err := row.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
		&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
		&workflow.CreatedAt, &workflow.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return workflow, nil
}

// GetWorkflowByUUID retrieves a workflow by UUID
func (s *SQLite) GetWorkflowByUUID(ctx context.Context, uuid string) (*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, created_at, updated_at FROM workflows WHERE uuid=? ORDER BY created_at DESC LIMIT 1`
	row := s.db.QueryRowContext(ctx, query, uuid)

	workflow := &model.Workflow{}
	err := row.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
		&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
		&workflow.CreatedAt, &workflow.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return workflow, nil
}

// GetWorkflowsByAgent retrieves workflows by agent ID
func (s *SQLite) GetWorkflowsByAgent(ctx context.Context, agentID string) ([]*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, created_at, updated_at FROM workflows WHERE agent_id=? ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []*model.Workflow
	for rows.Next() {
		workflow := &model.Workflow{}
		err := rows.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
			&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
			&workflow.CreatedAt, &workflow.UpdatedAt)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

// ListWorkflows lists workflows with pagination
func (s *SQLite) ListWorkflows(ctx context.Context, limit, offset int) ([]*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, created_at, updated_at FROM workflows ORDER BY created_at DESC LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []*model.Workflow
	for rows.Next() {
		workflow := &model.Workflow{}
		err := rows.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
			&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
			&workflow.CreatedAt, &workflow.UpdatedAt)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

// CreateTask creates a new task record
func (s *SQLite) CreateTask(ctx context.Context, task *model.Task) error {
	query := `INSERT INTO tasks (id, workflow_id, uuid, name, job_name, status, start_time, end_time, exit_code, output_dir, stdout, stderr, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now
	task.ID = task.GenerateID()

	_, err := s.db.ExecContext(ctx, query,
		task.ID, task.WorkflowID, task.UUID, task.Name, task.JobName, task.Status,
		task.StartTime, task.EndTime, task.ExitCode, task.OutputDir,
		task.Stdout, task.Stderr, task.CreatedAt, task.UpdatedAt)
	return err
}

// UpdateTask updates an existing task record
func (s *SQLite) UpdateTask(ctx context.Context, task *model.Task) error {
	query := `UPDATE tasks SET workflow_id=?, uuid=?, name=?, job_name=?, status=?, start_time=?, end_time=?, exit_code=?, output_dir=?, stdout=?, stderr=?, updated_at=? WHERE id=?`
	task.UpdatedAt = time.Now()
	task.ID = task.GenerateID()

	_, err := s.db.ExecContext(ctx, query,
		task.WorkflowID, task.UUID, task.Name, task.JobName, task.Status,
		task.StartTime, task.EndTime, task.ExitCode, task.OutputDir,
		task.Stdout, task.Stderr, task.UpdatedAt, task.ID)
	return err
}

// GetTask retrieves a task by ID
func (s *SQLite) GetTask(ctx context.Context, id string) (*model.Task, error) {
	query := `SELECT id, workflow_id, uuid, name, job_name, status, start_time, end_time, exit_code, output_dir, stdout, stderr, created_at, updated_at FROM tasks WHERE id=?`
	row := s.db.QueryRowContext(ctx, query, id)

	task := &model.Task{}
	err := row.Scan(&task.ID, &task.WorkflowID, &task.UUID, &task.Name, &task.JobName, &task.Status,
		&task.StartTime, &task.EndTime, &task.ExitCode, &task.OutputDir,
		&task.Stdout, &task.Stderr, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return task, nil
}

// GetTasksByWorkflow retrieves tasks by workflow ID
func (s *SQLite) GetTasksByWorkflow(ctx context.Context, workflowID string) ([]*model.Task, error) {
	query := `SELECT id, workflow_id, uuid, name, job_name, status, start_time, end_time, exit_code, output_dir, stdout, stderr, created_at, updated_at FROM tasks WHERE workflow_id=? ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*model.Task
	for rows.Next() {
		task := &model.Task{}
		err := rows.Scan(&task.ID, &task.WorkflowID, &task.UUID, &task.Name, &task.JobName, &task.Status,
			&task.StartTime, &task.EndTime, &task.ExitCode, &task.OutputDir,
			&task.Stdout, &task.Stderr, &task.CreatedAt, &task.UpdatedAt)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// ValidateAPIKey checks if an API key is valid
func (s *SQLite) ValidateAPIKey(ctx context.Context, key string) (bool, error) {
	query := `SELECT is_active FROM api_keys WHERE key=?`
	row := s.db.QueryRowContext(ctx, query, key)

	var isActive bool
	err := row.Scan(&isActive)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return isActive, nil
}

// CreateAPIKey creates a new API key
func (s *SQLite) CreateAPIKey(ctx context.Context, key, description string) error {
	query := `INSERT INTO api_keys (key, description, created_at, is_active) VALUES (?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, key, description, time.Now(), true)
	return err
}

// ListAPIKeys lists all API keys
func (s *SQLite) ListAPIKeys(ctx context.Context) ([]string, error) {
	query := `SELECT key FROM api_keys WHERE is_active=true`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// DeleteAPIKey deletes an API key
func (s *SQLite) DeleteAPIKey(ctx context.Context, key string) error {
	query := `DELETE FROM api_keys WHERE key=?`
	_, err := s.db.ExecContext(ctx, query, key)
	return err
}