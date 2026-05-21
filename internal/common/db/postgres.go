package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/model"
	_ "github.com/lib/pq"
)

// PostgreSQL implements Database interface using PostgreSQL
type PostgreSQL struct {
	db *sql.DB
}

// NewPostgreSQL creates a new PostgreSQL database instance
func NewPostgreSQL(cfg Config) (*PostgreSQL, error) {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to connect to postgres database: %w", err)
	}

	return &PostgreSQL{db: db}, nil
}

// Initialize creates tables
func (p *PostgreSQL) Initialize(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS workflows (
				id TEXT PRIMARY KEY,
				uuid TEXT NOT NULL,
				name TEXT NOT NULL,
				status TEXT NOT NULL,
				start_time TIMESTAMPTZ,
				end_time TIMESTAMPTZ,
				output_dir TEXT,
				outputs_json JSONB,
				agent_id TEXT,
				archived BOOLEAN DEFAULT FALSE,
				archived_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
			)`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS archived BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS idx_workflows_uuid ON workflows(uuid)`,
		`CREATE TABLE IF NOT EXISTS tasks (
				id TEXT PRIMARY KEY,
				workflow_id TEXT NOT NULL,
				uuid TEXT NOT NULL,
				name TEXT NOT NULL,
				job_name TEXT NOT NULL,
				status TEXT NOT NULL,
				start_time TIMESTAMPTZ,
				end_time TIMESTAMPTZ,
				exit_code SMALLINT,
				output_dir TEXT,
				stdout TEXT,
				stderr TEXT,
				created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (workflow_id) REFERENCES workflows(id)
			)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_uuid ON tasks(uuid)`,
	}

	for _, query := range queries {
		if _, err := p.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}
	return nil
}

// Close closes the database connection
func (p *PostgreSQL) Close() error {
	return p.db.Close()
}

// CreateWorkflow creates a new workflow record
func (p *PostgreSQL) CreateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	query := `INSERT INTO workflows (id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, created_at, updated_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
	now := time.Now()
	workflow.CreatedAt = now
	workflow.UpdatedAt = now

	_, err := p.db.ExecContext(ctx, query,
		workflow.ID, workflow.UUID, workflow.Name, workflow.Status,
		workflow.StartTime, workflow.EndTime, workflow.OutputDir,
		workflow.OutputsJSON, workflow.AgentID, workflow.Archived, workflow.ArchivedAt,
		workflow.CreatedAt, workflow.UpdatedAt)
	return err
}

// UpdateWorkflow updates an existing workflow record
func (p *PostgreSQL) UpdateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	query := `UPDATE workflows SET uuid=$1, name=$2, status=$3, start_time=$4, end_time=$5, output_dir=$6, outputs_json=$7, agent_id=$8, archived=$9, archived_at=$10, updated_at=$11 WHERE id=$12`
	workflow.UpdatedAt = time.Now()

	_, err := p.db.ExecContext(ctx, query,
		workflow.UUID, workflow.Name, workflow.Status, workflow.StartTime, workflow.EndTime,
		workflow.OutputDir, workflow.OutputsJSON, workflow.AgentID, workflow.Archived, workflow.ArchivedAt,
		workflow.UpdatedAt, workflow.ID)
	return err
}

// MarkArchived marks a workflow as archived by UUID
func (p *PostgreSQL) MarkArchived(ctx context.Context, uuid string) error {
	now := time.Now()
	_, err := p.db.ExecContext(ctx, `UPDATE workflows SET archived=$1, archived_at=$2, updated_at=$3 WHERE uuid=$4`,
		true, now, now, uuid)
	return err
}

// GetWorkflow retrieves a workflow by ID
func (p *PostgreSQL) GetWorkflow(ctx context.Context, id string) (*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, created_at, updated_at FROM workflows WHERE id=$1`
	row := p.db.QueryRowContext(ctx, query, id)

	workflow := &model.Workflow{}
	err := row.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
		&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
		&workflow.Archived, &workflow.ArchivedAt, &workflow.CreatedAt, &workflow.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return workflow, nil
}

// GetWorkflowByUUID retrieves a workflow by UUID
func (p *PostgreSQL) GetWorkflowByUUID(ctx context.Context, uuid string) (*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, created_at, updated_at FROM workflows WHERE uuid=$1 ORDER BY created_at DESC LIMIT 1`
	row := p.db.QueryRowContext(ctx, query, uuid)

	workflow := &model.Workflow{}
	err := row.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
		&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
		&workflow.Archived, &workflow.ArchivedAt, &workflow.CreatedAt, &workflow.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return workflow, nil
}

// GetWorkflowsByAgent retrieves workflows by agent ID
func (p *PostgreSQL) GetWorkflowsByAgent(ctx context.Context, agentID string) ([]*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, created_at, updated_at FROM workflows WHERE agent_id=$1 ORDER BY created_at DESC`
	rows, err := p.db.QueryContext(ctx, query, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []*model.Workflow
	for rows.Next() {
		workflow := &model.Workflow{}
		err := rows.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
			&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
			&workflow.Archived, &workflow.ArchivedAt, &workflow.CreatedAt, &workflow.UpdatedAt)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

// ListWorkflows lists workflows with pagination
func (p *PostgreSQL) ListWorkflows(ctx context.Context, limit, offset int) ([]*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, created_at, updated_at FROM workflows ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := p.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []*model.Workflow
	for rows.Next() {
		workflow := &model.Workflow{}
		err := rows.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
			&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
			&workflow.Archived, &workflow.ArchivedAt, &workflow.CreatedAt, &workflow.UpdatedAt)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

// CreateTask creates a new task record
func (p *PostgreSQL) CreateTask(ctx context.Context, task *model.Task) error {
	query := `INSERT INTO tasks (id, workflow_id, uuid, name, job_name, status, start_time, end_time, exit_code, output_dir, stdout, stderr, created_at, updated_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now
	task.ID = task.GenerateID()

	_, err := p.db.ExecContext(ctx, query,
		task.ID, task.WorkflowID, task.UUID, task.Name, task.JobName, task.Status,
		task.StartTime, task.EndTime, task.ExitCode, task.OutputDir,
		task.Stdout, task.Stderr, task.CreatedAt, task.UpdatedAt)
	return err
}

// UpdateTask updates an existing task record
func (p *PostgreSQL) UpdateTask(ctx context.Context, task *model.Task) error {
	query := `UPDATE tasks SET workflow_id=$1, uuid=$2, name=$3, job_name=$4, status=$5, start_time=$6, end_time=$7, exit_code=$8, output_dir=$9, stdout=$10, stderr=$11, updated_at=$12 WHERE id=$13`
	task.UpdatedAt = time.Now()
	task.ID = task.GenerateID()

	_, err := p.db.ExecContext(ctx, query,
		task.WorkflowID, task.UUID, task.Name, task.JobName, task.Status,
		task.StartTime, task.EndTime, task.ExitCode, task.OutputDir,
		task.Stdout, task.Stderr, task.UpdatedAt, task.ID)
	return err
}

// GetTask retrieves a task by ID
func (p *PostgreSQL) GetTask(ctx context.Context, id string) (*model.Task, error) {
	query := `SELECT id, workflow_id, uuid, name, job_name, status, start_time, end_time, exit_code, output_dir, stdout, stderr, created_at, updated_at FROM tasks WHERE id=$1`
	row := p.db.QueryRowContext(ctx, query, id)

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
func (p *PostgreSQL) GetTasksByWorkflow(ctx context.Context, workflowID string) ([]*model.Task, error) {
	query := `SELECT id, workflow_id, uuid, name, job_name, status, start_time, end_time, exit_code, output_dir, stdout, stderr, created_at, updated_at FROM tasks WHERE workflow_id=$1 ORDER BY created_at ASC`
	rows, err := p.db.QueryContext(ctx, query, workflowID)
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
