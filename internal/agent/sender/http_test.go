package sender

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SchemaBio/Sepiida/internal/common/model"
	"github.com/SchemaBio/Sepiida/internal/common/tasktoken"
)

func TestSendProgressUsesTaskToken(t *testing.T) {
	const secret = "shared-secret"

	var authHeader string
	sender := NewHTTPSenderWithTaskToken("http://sepiida.test", "static-key", "agent-1", secret)
	sender.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		authHeader = r.Header.Get("Authorization")
		return testResponse(http.StatusOK, ""), nil
	})}

	err := sender.SendProgress(&model.WorkflowProgress{
		AgentID: "agent-1",
		UUID:    "sample-uuid",
		Workflow: model.Workflow{
			ID: "run-1",
		},
	})
	if err != nil {
		t.Fatalf("SendProgress returned error: %v", err)
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	claims, err := tasktoken.Validate(secret, token)
	if err != nil {
		t.Fatalf("Authorization did not contain a valid task token: %v", err)
	}
	if claims.UUID != "sample-uuid" || claims.AgentID != "agent-1" {
		t.Fatalf("unexpected token claims: %+v", claims)
	}
}

func TestNotifyArchivedSendsAgentAndWorkflowID(t *testing.T) {
	var req model.ArchiveResult
	sender := NewHTTPSender("http://sepiida.test", "static-key", "agent-1")
	sender.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		return testResponse(http.StatusOK, ""), nil
	})}

	err := sender.NotifyArchived(&model.ArchiveResult{
		UUID:       "sample-uuid",
		WorkflowID: "run-1",
	})
	if err != nil {
		t.Fatalf("NotifyArchived returned error: %v", err)
	}

	if req.AgentID != "agent-1" || req.WorkflowID != "run-1" {
		t.Fatalf("archive notification missed identity fields: %+v", req)
	}
}

func TestSendOutputReturnsServerErrorBody(t *testing.T) {
	sender := NewHTTPSender("http://sepiida.test", "static-key", "agent-1")
	sender.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testResponse(http.StatusBadRequest, "bad output\n"), nil
	})}

	err := sender.SendOutput("sample-uuid", "run-1", "{}")
	if err == nil {
		t.Fatal("expected SendOutput to return server error")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "bad output") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEndpointPreservesBasePathAndDropsQueryFragment(t *testing.T) {
	sender := NewHTTPSender("http://sepiida.test/base/?debug=true#fragment", "static-key", "agent-1")

	endpoint, err := sender.endpoint("/api/v1/progress")
	if err != nil {
		t.Fatalf("endpoint returned error: %v", err)
	}
	if endpoint != "http://sepiida.test/base/api/v1/progress" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
}

func TestEndpointRejectsUnsupportedURLForms(t *testing.T) {
	tests := []string{
		"ftp://sepiida.test",
		"http://user:pass@sepiida.test",
		"://missing-scheme",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			sender := NewHTTPSender(rawURL, "static-key", "agent-1")
			if _, err := sender.endpoint("/api/v1/progress"); err == nil {
				t.Fatalf("expected endpoint to reject %q", rawURL)
			}
		})
	}
}

func TestSendOutputTruncatesLargeServerErrorBody(t *testing.T) {
	sender := NewHTTPSender("http://sepiida.test", "static-key", "agent-1")
	sender.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testResponse(http.StatusInternalServerError, strings.Repeat("x", maxErrorBodyBytes+100)), nil
	})}

	err := sender.SendOutput("sample-uuid", "run-1", "{}")
	if err == nil {
		t.Fatal("expected SendOutput to return server error")
	}
	msg := err.Error()
	if !strings.Contains(msg, strings.Repeat("x", maxErrorBodyBytes)) || !strings.Contains(msg, "...") {
		t.Fatalf("expected truncated body with ellipsis, got: %v", err)
	}
	if strings.Contains(msg, strings.Repeat("x", maxErrorBodyBytes+1)) {
		t.Fatalf("error body was not truncated: %d-byte message", len(msg))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}
