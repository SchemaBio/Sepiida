package tasktoken

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateValidateTaskToken(t *testing.T) {
	token, err := Generate("shared-secret", "task-uuid", "agent-1", time.Hour)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !LooksLike(token) {
		t.Fatalf("token should use st1 prefix: %s", token)
	}

	claims, err := Validate("shared-secret", token)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if claims.UUID != "task-uuid" || claims.AgentID != "agent-1" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestValidateRejectsTamperedToken(t *testing.T) {
	token, err := Generate("shared-secret", "task-uuid", "agent-1", time.Hour)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	token = strings.Replace(token, "st1.", "st1.x", 1)
	if _, err := Validate("shared-secret", token); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
}
