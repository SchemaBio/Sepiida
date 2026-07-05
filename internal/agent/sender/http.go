package sender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/model"
	"github.com/SchemaBio/Sepiida/internal/common/tasktoken"
)

const defaultHTTPTimeout = 30 * time.Second
const maxErrorBodyBytes = 4 << 10

// HTTPSender sends progress data to server via HTTP
type HTTPSender struct {
	serverURL       string
	apiKey          string
	agentID         string
	taskToken       string
	taskTokenSecret string
	client          *http.Client
}

// NewHTTPSender creates a new HTTP sender
func NewHTTPSender(serverURL, apiKey, agentID string) *HTTPSender {
	return &HTTPSender{
		serverURL: serverURL,
		apiKey:    apiKey,
		agentID:   agentID,
		client:    &http.Client{Timeout: defaultHTTPTimeout},
	}
}

// NewHTTPSenderWithTaskToken creates a sender that signs each write request with a per-task token.
func NewHTTPSenderWithTaskToken(serverURL, apiKey, agentID, taskTokenSecret string) *HTTPSender {
	s := NewHTTPSender(serverURL, apiKey, agentID)
	s.taskTokenSecret = taskTokenSecret
	return s
}

// NewHTTPSenderWithTaskCredential creates a sender that prefers a pre-issued
// task token and only falls back to signing locally when a legacy shared secret
// is explicitly configured. Production agents should receive taskToken rather
// than the signing secret.
func NewHTTPSenderWithTaskCredential(serverURL, apiKey, agentID, taskToken, taskTokenSecret string) *HTTPSender {
	s := NewHTTPSender(serverURL, apiKey, agentID)
	s.taskToken = strings.TrimSpace(taskToken)
	s.taskTokenSecret = taskTokenSecret
	return s
}

// SendProgress sends workflow progress to server
func (s *HTTPSender) SendProgress(progress *model.WorkflowProgress) error {
	endpoint, err := s.endpoint("/api/v1/progress")
	if err != nil {
		return err
	}

	body, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal progress: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if err := s.authorize(req, progress.UUID, progress.Workflow.ID); err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody := readErrorBody(resp.Body)
		return fmt.Errorf("server returned error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// NotifyArchived notifies the server that a workflow's outputs have been archived.
func (s *HTTPSender) NotifyArchived(result *model.ArchiveResult) error {
	endpoint, err := s.endpoint("/api/v1/workflow/archive")
	if err != nil {
		return err
	}
	if result.AgentID == "" {
		result.AgentID = s.agentID
	}

	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal archive notification: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if err := s.authorize(req, result.UUID, result.WorkflowID); err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody := readErrorBody(resp.Body)
		return fmt.Errorf("server returned error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendOutput sends workflow output to server (with UUID)
func (s *HTTPSender) SendOutput(uuid string, workflowID string, outputsJSON string) error {
	endpoint, err := s.endpoint("/api/v1/workflow/output")
	if err != nil {
		return err
	}

	reqBody := model.WorkflowOutputRequest{
		UUID:        uuid,
		WorkflowID:  workflowID,
		AgentID:     s.agentID,
		OutputsJSON: outputsJSON,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal output: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if err := s.authorize(req, uuid, workflowID); err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody := readErrorBody(resp.Body)
		return fmt.Errorf("server returned error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (s *HTTPSender) authorize(req *http.Request, uuid string, workflowID string) error {
	token := s.apiKey
	if s.taskToken != "" {
		token = s.taskToken
	} else if s.taskTokenSecret != "" {
		var err error
		token, err = tasktoken.GenerateForWorkflow(s.taskTokenSecret, uuid, s.agentID, workflowID, 24*time.Hour)
		if err != nil {
			return fmt.Errorf("failed to generate task token: %w", err)
		}
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("authentication token is required")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (s *HTTPSender) endpoint(apiPath string) (string, error) {
	rawBase := strings.TrimSpace(s.serverURL)
	if rawBase == "" ||
		strings.HasPrefix(rawBase, "//") ||
		strings.HasPrefix(rawBase, `\\`) ||
		strings.Contains(rawBase, `\`) ||
		strings.ContainsFunc(rawBase, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("invalid server URL %q", s.serverURL)
	}
	if !strings.HasPrefix(apiPath, "/") ||
		strings.HasPrefix(apiPath, "//") ||
		strings.Contains(apiPath, `\`) ||
		strings.ContainsFunc(apiPath, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("invalid API path %q", apiPath)
	}

	base, err := url.Parse(rawBase)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("invalid server URL %q", s.serverURL)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return "", fmt.Errorf("unsupported server URL scheme %q", base.Scheme)
	}
	if base.User != nil {
		return "", fmt.Errorf("server URL must not include username or password")
	}

	base.Path = strings.TrimRight(base.Path, "/") + apiPath
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}

func readErrorBody(r io.Reader) []byte {
	data, err := io.ReadAll(io.LimitReader(r, maxErrorBodyBytes+1))
	if err != nil {
		return nil
	}
	if len(data) > maxErrorBodyBytes {
		return append(data[:maxErrorBodyBytes], []byte("...")...)
	}
	return data
}
