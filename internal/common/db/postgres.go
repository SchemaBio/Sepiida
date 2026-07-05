package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
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
	connStr := cfg.DSN
	if connStr == "" {
		connStr = postgresURLDSN(cfg)
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres database: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to postgres database: %w", err)
	}

	return &PostgreSQL{db: db}, nil
}

func postgresURLDSN(cfg Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port <= 0 {
		port = 5432
	}
	user := cfg.User
	if user == "" {
		user = "postgres"
	}

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, cfg.Password),
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + cfg.Database,
	}
	if escapedDatabase := url.PathEscape(cfg.Database); escapedDatabase != cfg.Database {
		u.RawPath = "/" + escapedDatabase
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
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
				archive_base TEXT DEFAULT '',
				base_path TEXT DEFAULT '',
				outputs_resolved_key TEXT DEFAULT '',
				object_prefix TEXT DEFAULT '',
				key_prefix TEXT DEFAULT '',
				archived_count INTEGER DEFAULT 0,
				created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
			)`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS archived BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS archive_base TEXT DEFAULT ''`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS base_path TEXT DEFAULT ''`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS outputs_resolved_key TEXT DEFAULT ''`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS object_prefix TEXT DEFAULT ''`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS key_prefix TEXT DEFAULT ''`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS archived_count INTEGER DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_workflows_uuid ON workflows(uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_workflows_agent_id ON workflows(agent_id)`,
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
		`CREATE INDEX IF NOT EXISTS idx_tasks_workflow_id ON tasks(workflow_id)`,
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
	query := `INSERT INTO workflows (id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, archive_base, base_path, outputs_resolved_key, object_prefix, key_prefix, archived_count, created_at, updated_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`
	now := time.Now()
	workflow.CreatedAt = now
	workflow.UpdatedAt = now

	_, err := p.db.ExecContext(ctx, query,
		workflow.ID, workflow.UUID, workflow.Name, workflow.Status,
		workflow.StartTime, workflow.EndTime, workflow.OutputDir,
		normalizeJSONB(workflow.OutputsJSON), workflow.AgentID, workflow.Archived, workflow.ArchivedAt,
		workflow.ArchiveBase, workflow.BasePath, workflow.OutputsResolvedKey,
		workflow.ObjectPrefix, workflow.KeyPrefix, workflow.ArchivedCount,
		workflow.CreatedAt, workflow.UpdatedAt)
	return err
}

// UpdateWorkflow updates an existing workflow record
func (p *PostgreSQL) UpdateWorkflow(ctx context.Context, workflow *model.Workflow) error {
	query := `UPDATE workflows SET uuid=$1, name=$2, status=$3, start_time=$4, end_time=$5, output_dir=$6, outputs_json=$7, agent_id=$8, archived=$9, archived_at=$10, archive_base=$11, base_path=$12, outputs_resolved_key=$13, object_prefix=$14, key_prefix=$15, archived_count=$16, updated_at=$17 WHERE id=$18`
	workflow.UpdatedAt = time.Now()

	res, err := p.db.ExecContext(ctx, query,
		workflow.UUID, workflow.Name, workflow.Status, workflow.StartTime, workflow.EndTime,
		workflow.OutputDir, normalizeJSONB(workflow.OutputsJSON), workflow.AgentID, workflow.Archived, workflow.ArchivedAt,
		workflow.ArchiveBase, workflow.BasePath, workflow.OutputsResolvedKey,
		workflow.ObjectPrefix, workflow.KeyPrefix, workflow.ArchivedCount,
		workflow.UpdatedAt, workflow.ID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// MarkArchived marks a concrete workflow execution as archived. New agents send
// WorkflowID; UUID fallback is kept for compatibility with older agents.
func (p *PostgreSQL) MarkArchived(ctx context.Context, result *model.ArchiveResult) error {
	now := time.Now()
	query := `
		UPDATE workflows
		SET archived=$1, archived_at=$2, archive_base=$3, base_path=$4, outputs_resolved_key=$5, object_prefix=$6, key_prefix=$7, archived_count=$8, updated_at=$9
		WHERE id=$10`
	workflowKey := result.WorkflowID
	if workflowKey == "" {
		query = `
			UPDATE workflows
			SET archived=$1, archived_at=$2, archive_base=$3, base_path=$4, outputs_resolved_key=$5, object_prefix=$6, key_prefix=$7, archived_count=$8, updated_at=$9
			WHERE id = (
				SELECT id FROM workflows WHERE uuid=$10 ORDER BY created_at DESC LIMIT 1
			)`
		workflowKey = result.UUID
	}

	res, err := p.db.ExecContext(ctx, query,
		true, now, result.ArchiveBase, result.BasePath, result.OutputsResolvedKey,
		result.ObjectPrefix, result.KeyPrefix, result.ArchivedCount, now, workflowKey)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetWorkflow retrieves a workflow by ID
func (p *PostgreSQL) GetWorkflow(ctx context.Context, id string) (*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, COALESCE(archive_base, ''), COALESCE(base_path, ''), COALESCE(outputs_resolved_key, ''), COALESCE(object_prefix, ''), COALESCE(key_prefix, ''), COALESCE(archived_count, 0), created_at, updated_at FROM workflows WHERE id=$1`
	row := p.db.QueryRowContext(ctx, query, id)

	workflow := &model.Workflow{}
	err := row.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
		&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
		&workflow.Archived, &workflow.ArchivedAt, &workflow.ArchiveBase, &workflow.BasePath,
		&workflow.OutputsResolvedKey, &workflow.ObjectPrefix, &workflow.KeyPrefix,
		&workflow.ArchivedCount, &workflow.CreatedAt, &workflow.UpdatedAt)
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
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, COALESCE(archive_base, ''), COALESCE(base_path, ''), COALESCE(outputs_resolved_key, ''), COALESCE(object_prefix, ''), COALESCE(key_prefix, ''), COALESCE(archived_count, 0), created_at, updated_at FROM workflows WHERE uuid=$1 ORDER BY created_at DESC LIMIT 1`
	row := p.db.QueryRowContext(ctx, query, uuid)

	workflow := &model.Workflow{}
	err := row.Scan(&workflow.ID, &workflow.UUID, &workflow.Name, &workflow.Status, &workflow.StartTime,
		&workflow.EndTime, &workflow.OutputDir, &workflow.OutputsJSON, &workflow.AgentID,
		&workflow.Archived, &workflow.ArchivedAt, &workflow.ArchiveBase, &workflow.BasePath,
		&workflow.OutputsResolvedKey, &workflow.ObjectPrefix, &workflow.KeyPrefix,
		&workflow.ArchivedCount, &workflow.CreatedAt, &workflow.UpdatedAt)
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
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, COALESCE(archive_base, ''), COALESCE(base_path, ''), COALESCE(outputs_resolved_key, ''), COALESCE(object_prefix, ''), COALESCE(key_prefix, ''), COALESCE(archived_count, 0), created_at, updated_at FROM workflows WHERE agent_id=$1 ORDER BY created_at DESC`
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
			&workflow.Archived, &workflow.ArchivedAt, &workflow.ArchiveBase, &workflow.BasePath,
			&workflow.OutputsResolvedKey, &workflow.ObjectPrefix, &workflow.KeyPrefix,
			&workflow.ArchivedCount, &workflow.CreatedAt, &workflow.UpdatedAt)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, workflow)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return workflows, nil
}

// ListWorkflows lists workflows with pagination
func (p *PostgreSQL) ListWorkflows(ctx context.Context, limit, offset int) ([]*model.Workflow, error) {
	query := `SELECT id, uuid, name, status, start_time, end_time, output_dir, outputs_json, agent_id, archived, archived_at, COALESCE(archive_base, ''), COALESCE(base_path, ''), COALESCE(outputs_resolved_key, ''), COALESCE(object_prefix, ''), COALESCE(key_prefix, ''), COALESCE(archived_count, 0), created_at, updated_at FROM workflows ORDER BY created_at DESC LIMIT $1 OFFSET $2`
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
			&workflow.Archived, &workflow.ArchivedAt, &workflow.ArchiveBase, &workflow.BasePath,
			&workflow.OutputsResolvedKey, &workflow.ObjectPrefix, &workflow.KeyPrefix,
			&workflow.ArchivedCount, &workflow.CreatedAt, &workflow.UpdatedAt)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, workflow)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func normalizeJSONB(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	quoted, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return string(quoted)
}
