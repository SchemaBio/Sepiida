package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func init() { sql.Register("sepiida-nullable-test", nullableTestDriver{}) }

type nullableTestDriver struct{}

func (nullableTestDriver) Open(string) (driver.Conn, error) { return nullableTestConn{}, nil }

type nullableTestConn struct{}

func (nullableTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not supported")
}
func (nullableTestConn) Close() error { return nil }
func (nullableTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions not supported")
}
func (nullableTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	values := []driver.Value{"workflow-1", "uuid-1", "test", "running", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, int64(0), time.Unix(1, 0), time.Unix(1, 0)}
	if strings.Contains(query, "COALESCE(output_dir, '')") {
		values[6] = ""
	}
	if strings.Contains(query, "COALESCE(outputs_json::text, '')") {
		values[7] = ""
	}
	if strings.Contains(query, "COALESCE(agent_id, '')") {
		values[8] = ""
	}
	if strings.Contains(query, "COALESCE(archived, FALSE)") {
		values[9] = false
	}
	if strings.Contains(query, "COALESCE(archive_base, '')") {
		for _, index := range []int{11, 12, 13, 14, 15} {
			values[index] = ""
		}
	}
	return &nullableTestRows{values: values}, nil
}

type nullableTestRows struct {
	values []driver.Value
	done   bool
}

func (r *nullableTestRows) Columns() []string {
	return []string{"id", "uuid", "name", "status", "start_time", "end_time", "output_dir", "outputs_json", "agent_id", "archived", "archived_at", "archive_base", "base_path", "outputs_resolved_key", "object_prefix", "key_prefix", "archived_count", "created_at", "updated_at"}
}
func (r *nullableTestRows) Close() error { return nil }
func (r *nullableTestRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, r.values)
	return nil
}

func TestWorkflowReadsCoalesceNullableColumns(t *testing.T) {
	dbConn, err := sql.Open("sepiida-nullable-test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer dbConn.Close()
	p := &PostgreSQL{db: dbConn}
	ctx := context.Background()
	tests := []struct {
		name string
		read func() (*string, error)
	}{
		{"by id", func() (*string, error) {
			row, err := p.GetWorkflow(ctx, "workflow-1")
			if err != nil || row == nil {
				return nil, err
			}
			return &row.OutputsJSON, nil
		}},
		{"by uuid", func() (*string, error) {
			row, err := p.GetWorkflowByUUID(ctx, "uuid-1")
			if err != nil || row == nil {
				return nil, err
			}
			return &row.OutputsJSON, nil
		}},
		{"by attempt", func() (*string, error) {
			row, err := p.GetWorkflowByAttempt(ctx, "uuid-1", "agent-1")
			if err != nil || row == nil {
				return nil, err
			}
			return &row.OutputsJSON, nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := test.read()
			if err != nil {
				t.Fatalf("nullable workflow read failed: %v", err)
			}
			if value == nil || *value != "" {
				t.Fatalf("NULL outputs_json should read as empty string, got %#v", value)
			}
		})
	}
	for name, read := range map[string]func() ([]*string, error){
		"attempt list": func() ([]*string, error) {
			rows, err := p.ListWorkflowsByUUID(ctx, "uuid-1")
			if err != nil {
				return nil, err
			}
			out := make([]*string, 0, len(rows))
			for _, row := range rows {
				out = append(out, &row.OutputsJSON)
			}
			return out, nil
		},
		"agent list": func() ([]*string, error) {
			rows, err := p.GetWorkflowsByAgent(ctx, "agent-1")
			if err != nil {
				return nil, err
			}
			out := make([]*string, 0, len(rows))
			for _, row := range rows {
				out = append(out, &row.OutputsJSON)
			}
			return out, nil
		},
		"page list": func() ([]*string, error) {
			rows, err := p.ListWorkflows(ctx, 10, 0)
			if err != nil {
				return nil, err
			}
			out := make([]*string, 0, len(rows))
			for _, row := range rows {
				out = append(out, &row.OutputsJSON)
			}
			return out, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			values, err := read()
			if err != nil || len(values) != 1 || values[0] == nil || *values[0] != "" {
				t.Fatalf("nullable workflow list read failed: values=%#v err=%v", values, err)
			}
		})
	}
}
