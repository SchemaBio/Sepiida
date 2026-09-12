package tasktoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

const protocolTestSecret = "0123456789abcdef0123456789abcdef0123456789abcdef"

// Keep this wire vector identical to Squid's tasktoken test. It is the
// compatibility contract between the signer and Sepiida's validator.
func TestProtocolVector(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"uuid":"task-123","agent_id":"attempt-456","attempt_id":"attempt-456","workflow_id":"wf-789","jti":"jti-abc","exp":4102444800}`))
	mac := hmac.New(sha256.New, []byte(protocolTestSecret))
	_, _ = mac.Write([]byte(payload))
	token := prefix + "." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	claims, err := Validate(protocolTestSecret, token)
	if err != nil {
		t.Fatalf("protocol vector rejected: %v", err)
	}
	if claims.UUID != "task-123" || claims.AgentID != "attempt-456" || claims.AttemptID != "attempt-456" || claims.WorkflowID != "wf-789" || claims.JTI != "jti-abc" || claims.Exp != 4102444800 {
		t.Fatalf("unexpected protocol claims: %+v", claims)
	}
}

func TestGenerateValidateAndRejectsTampering(t *testing.T) {
	token, err := GenerateForAttempt(protocolTestSecret, "task-123", "agent-1", "attempt-1", "workflow-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Validate(protocolTestSecret, token)
	if err != nil || claims.AttemptID != "attempt-1" || claims.WorkflowID != "workflow-1" || claims.JTI == "" {
		t.Fatalf("round trip failed: claims=%+v err=%v", claims, err)
	}
	parts := []byte(token)
	parts[len(parts)-1] ^= 1
	if _, err := Validate(protocolTestSecret, string(parts)); err == nil {
		t.Fatal("tampered task token was accepted")
	}
}

func TestRejectsExpiredAndWeakSecret(t *testing.T) {
	if _, err := Generate(protocolTestSecret[:8], "task", "agent", time.Hour); err == nil {
		t.Fatal("weak task token secret was accepted")
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"uuid":"task","agent_id":"agent","exp":1}`))
	mac := hmac.New(sha256.New, []byte(protocolTestSecret))
	_, _ = mac.Write([]byte(payload))
	token := prefix + "." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if _, err := Validate(protocolTestSecret, token); err == nil {
		t.Fatal("expired task token was accepted")
	}
}
