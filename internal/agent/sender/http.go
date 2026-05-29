package sender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/model"
	"github.com/SchemaBio/Sepiida/internal/common/tasktoken"
)

const defaultHTTPTimeout = 30 * time.Second

// HTTPSender sends progress data to server via HTTP
type HTTPSender struct {
	serverURL       string
	apiKey          string
	agentID         string
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

// SendProgress sends workflow progress to server
func (s *HTTPSender) SendProgress(progress *model.WorkflowProgress) error {
	url := s.serverURL + "/api/v1/progress"

	body, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal progress: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if err := s.authorize(req, progress.UUID); err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// NotifyArchived notifies the server that a workflow's outputs have been archived.
func (s *HTTPSender) NotifyArchived(result *model.ArchiveResult) error {
	url := s.serverURL + "/api/v1/workflow/archive"
	if result.AgentID == "" {
		result.AgentID = s.agentID
	}

	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal archive notification: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if err := s.authorize(req, result.UUID); err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendOutput sends workflow output to server (with UUID)
func (s *HTTPSender) SendOutput(uuid string, workflowID string, outputsJSON string) error {
	url := s.serverURL + "/api/v1/workflow/output"

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

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if err := s.authorize(req, uuid); err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (s *HTTPSender) authorize(req *http.Request, uuid string) error {
	token := s.apiKey
	if s.taskTokenSecret != "" {
		var err error
		token, err = tasktoken.Generate(s.taskTokenSecret, uuid, s.agentID, 24*time.Hour)
		if err != nil {
			return fmt.Errorf("failed to generate task token: %w", err)
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}
