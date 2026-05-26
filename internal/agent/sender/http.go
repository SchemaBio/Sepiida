package sender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/model"
)

const defaultHTTPTimeout = 30 * time.Second

// HTTPSender sends progress data to server via HTTP
type HTTPSender struct {
	serverURL string
	apiKey    string
	client    *http.Client
}

// NewHTTPSender creates a new HTTP sender
func NewHTTPSender(serverURL, apiKey string) *HTTPSender {
	return &HTTPSender{
		serverURL: serverURL,
		apiKey:    apiKey,
		client:    &http.Client{Timeout: defaultHTTPTimeout},
	}
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
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

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

	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal archive notification: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

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
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

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
