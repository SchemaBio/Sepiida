package db

import (
	"context"

	"github.com/SchemaBio/Sepiida/internal/common/model"
)

// Database interface defines the methods for database operations
type Database interface {
	// Initialize initializes the database connection and creates tables
	Initialize(ctx context.Context) error

	// Close closes the database connection
	Close() error

	// Workflow operations
	CreateWorkflow(ctx context.Context, workflow *model.Workflow) error
	UpdateWorkflow(ctx context.Context, workflow *model.Workflow) error
	MarkArchived(ctx context.Context, uuid string) error
	GetWorkflow(ctx context.Context, id string) (*model.Workflow, error)
	GetWorkflowByUUID(ctx context.Context, uuid string) (*model.Workflow, error)
	GetWorkflowsByAgent(ctx context.Context, agentID string) ([]*model.Workflow, error)
	ListWorkflows(ctx context.Context, limit, offset int) ([]*model.Workflow, error)

	// Task operations
	CreateTask(ctx context.Context, task *model.Task) error
	UpdateTask(ctx context.Context, task *model.Task) error
	GetTask(ctx context.Context, id string) (*model.Task, error)
	GetTasksByWorkflow(ctx context.Context, workflowID string) ([]*model.Task, error)
}

// Config represents PostgreSQL database configuration
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}
