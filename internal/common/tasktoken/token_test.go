package tasktoken

import (
	"strings"
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestGenerateValidateTaskToken(t *testing.T) {
	token, err := Generate(testSecret, "task-uuid", "agent-1", time.Hour)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !LooksLike(token) {
		t.Fatalf("token should use st1 prefix: %s", token)
	}

	claims, err := Validate(testSecret, token)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if claims.UUID != "task-uuid" || claims.AgentID != "agent-1" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestGenerateForWorkflowBindsWorkflowID(t *testing.T) {
	token, err := GenerateForWorkflow(testSecret, "task-uuid", "agent-1", "run-1", time.Hour)
	if err != nil {
		t.Fatalf("GenerateForWorkflow() error = %v", err)
	}

	claims, err := Validate(testSecret, token)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if claims.WorkflowID != "run-1" {
		t.Fatalf("expected workflow_id claim, got %+v", claims)
	}
}

func TestValidateRejectsTamperedToken(t *testing.T) {
	token, err := Generate(testSecret, "task-uuid", "agent-1", time.Hour)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	token = strings.Replace(token, "st1.", "st1.x", 1)
	if _, err := Validate(testSecret, token); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
}

func TestRejectsShortSecret(t *testing.T) {
	if _, err := Generate("short-secret", "task-uuid", "agent-1", time.Hour); err == nil {
		t.Fatal("expected Generate to reject short secret")
	}
	if _, err := Validate("short-secret", "st1.payload.signature"); err == nil {
		t.Fatal("expected Validate to reject short secret")
	}
}

func TestRejectsPlaceholderSecret(t *testing.T) {
	placeholder := "change-me-long-random-shared-secret"
	if _, err := Generate(placeholder, "task-uuid", "agent-1", time.Hour); err == nil {
		t.Fatal("expected Generate to reject placeholder secret")
	}
	if _, err := Validate(placeholder, "st1.payload.signature"); err == nil {
		t.Fatal("expected Validate to reject placeholder secret")
	}
}
