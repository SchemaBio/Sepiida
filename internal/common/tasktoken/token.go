package tasktoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const prefix = "st1"
const MinSecretBytes = 32

type Claims struct {
	UUID       string `json:"uuid"`
	AgentID    string `json:"agent_id"`
	WorkflowID string `json:"workflow_id,omitempty"`
	Exp        int64  `json:"exp"`
}

func Generate(secret, uuid, agentID string, ttl time.Duration) (string, error) {
	return GenerateForWorkflow(secret, uuid, agentID, "", ttl)
}

func GenerateForWorkflow(secret, uuid, agentID, workflowID string, ttl time.Duration) (string, error) {
	if err := validateSecret(secret); err != nil {
		return "", err
	}
	if uuid == "" || agentID == "" {
		return "", errors.New("uuid and agent_id are required")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	claims := Claims{
		UUID:       uuid,
		AgentID:    agentID,
		WorkflowID: workflowID,
		Exp:        time.Now().Add(ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := sign(secret, encodedPayload)
	return prefix + "." + encodedPayload + "." + signature, nil
}

func Validate(secret, token string) (*Claims, error) {
	if err := validateSecret(secret); err != nil {
		return nil, err
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != prefix {
		return nil, errors.New("invalid task token format")
	}
	expected := sign(secret, parts[1])
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, errors.New("invalid task token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid task token payload: %w", err)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("invalid task token claims: %w", err)
	}
	if claims.UUID == "" || claims.AgentID == "" {
		return nil, errors.New("task token missing uuid or agent_id")
	}
	if claims.Exp <= time.Now().Unix() {
		return nil, errors.New("task token expired")
	}
	return &claims, nil
}

func LooksLike(token string) bool {
	return strings.HasPrefix(token, prefix+".")
}

func validateSecret(secret string) error {
	if secret == "" {
		return errors.New("task token secret is required")
	}
	if len(secret) < MinSecretBytes {
		return fmt.Errorf("task token secret must be at least %d bytes", MinSecretBytes)
	}
	if looksLikePlaceholderSecret(secret) {
		return errors.New("task token secret appears to be a placeholder; use a long random secret")
	}
	return nil
}

func sign(secret, encodedPayload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func looksLikePlaceholderSecret(secret string) bool {
	normalized := strings.ToLower(strings.TrimSpace(secret))
	normalized = strings.NewReplacer("-", "", "_", "", " ", "").Replace(normalized)
	for _, marker := range []string{
		"changeme",
		"changeit",
		"replacewith",
		"example",
		"placeholder",
		"yourrandomsecret",
		"longrandomsharedsecret",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
