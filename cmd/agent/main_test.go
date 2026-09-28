package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SchemaBio/Sepiida/internal/agent/collector"
	statepkg "github.com/SchemaBio/Sepiida/internal/agent/state"
	"github.com/SchemaBio/Sepiida/internal/common/model"
)

func TestParseWatchDirsDropsEmptyEntries(t *testing.T) {
	got := parseWatchDirs(` /data/a,,"/data/b", '' `)
	want := []string{"/data/a", "/data/b"}

	if len(got) != len(want) {
		t.Fatalf("unexpected dirs: got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected dirs: got %#v want %#v", got, want)
		}
	}
}

func TestRedactURLForLog(t *testing.T) {
	got := redactURLForLog("https://user:secret@example.test/archive?token=abc&prefix=ok")

	if strings.Contains(got, "secret") || strings.Contains(got, "token=abc") {
		t.Fatalf("URL was not redacted: %s", got)
	}
	if !strings.Contains(got, "prefix=ok") {
		t.Fatalf("non-sensitive query parameter should be preserved: %s", got)
	}
}

func TestDefaultServerURLIgnoresRemovedAliases(t *testing.T) {
	t.Setenv("SEPIIDA_SERVER_URL", "")
	t.Setenv("SEPIIDA_API_URL", "http://legacy-api:9090")
	t.Setenv("SEPIIDA_SERVER", "http://legacy-server:9090")
	if got := defaultServerURL(); got != "" {
		t.Fatalf("removed server URL aliases still affected configuration: %q", got)
	}
}

func TestValidateAgentCredentialsRequiresExactlyOneMode(t *testing.T) {
	if err := validateAgentCredentials("static-key", ""); err != nil {
		t.Fatalf("static key should be valid: %v", err)
	}
	if err := validateAgentCredentials("", "task-token"); err != nil {
		t.Fatalf("task token should be valid: %v", err)
	}
	if err := validateAgentCredentials("", ""); err == nil {
		t.Fatal("missing credentials should fail")
	}
	if err := validateAgentCredentials("static-key", "task-token"); err == nil {
		t.Fatal("mixed authentication modes should fail")
	}
}

func TestWorkflowArchiverForNilReturnsNilInterface(t *testing.T) {
	if got := workflowArchiverFor(nil); got != nil {
		t.Fatalf("nil archiver became a non-nil interface: %#v", got)
	}
}

func TestNodeCallbackSendsTaskIdentityAndPersistsRouteState(t *testing.T) {
	previous := newNodeCallbackRoute
	t.Cleanup(func() { newNodeCallbackRoute = previous })
	route := &nodeCallbackRouteFake{response: nodeCallbackResponse(http.StatusNoContent)}
	newNodeCallbackRoute = func(address string) (nodeCallbackRoute, error) {
		if address != "10.0.0.9:443" {
			t.Fatalf("unexpected private address: %q", address)
		}
		return route, nil
	}
	t.Setenv("SEPIIDA_TASK_TOKEN", "task-token")
	t.Setenv("SEPIIDA_NODE_SECRET", "node-secret")
	stateFile := t.TempDir() + "/route.json"
	var output bytes.Buffer
	code := runNodeCallbackIO("https://squid.example/api/v1/cvm/node/status", "10.0.0.9:443", stateFile, strings.NewReader(`{"phase":"starting"}`), &output)
	if code != 0 || output.String() != "204||upstream\n" {
		t.Fatalf("unexpected node callback result: code=%d output=%q", code, output.String())
	}
	if route.request == nil || route.request.Header.Get("Authorization") != "Bearer task-token" || route.request.Header.Get("x-node-secret") != "node-secret" {
		t.Fatalf("node callback lost its identity headers: %#v", route.request)
	}
	if got := loadNodeCallbackCooldown(stateFile); got.IsZero() {
		t.Fatal("node callback did not write its route cooldown file")
	}
}

func TestNodeCallbackRejectsInvalidPayloadWithoutRequest(t *testing.T) {
	previous := newNodeCallbackRoute
	t.Cleanup(func() { newNodeCallbackRoute = previous })
	called := false
	newNodeCallbackRoute = func(string) (nodeCallbackRoute, error) { called = true; return &nodeCallbackRouteFake{}, nil }
	t.Setenv("SEPIIDA_TASK_TOKEN", "task-token")
	var output bytes.Buffer
	code := runNodeCallbackIO("https://squid.example/api", "10.0.0.9:443", t.TempDir()+"/route.json", strings.NewReader("not-json"), &output)
	if code == 0 || called || output.String() != "0||network\n" {
		t.Fatalf("invalid payload was accepted: code=%d called=%t output=%q", code, called, output.String())
	}
}

type nodeCallbackRouteFake struct {
	request  *http.Request
	response *http.Response
	cooldown time.Time
}

func (f *nodeCallbackRouteFake) RoundTrip(request *http.Request) (*http.Response, error) {
	f.request = request
	if f.response == nil {
		return nil, io.EOF
	}
	return f.response, nil
}

func (f *nodeCallbackRouteFake) SetPrivateCooldownUntil(deadline time.Time) { f.cooldown = deadline }
func (f *nodeCallbackRouteFake) PrivateCooldownUntil() time.Time {
	if f.cooldown.IsZero() {
		return time.Now().Add(time.Minute)
	}
	return f.cooldown
}

func nodeCallbackResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}
}

func TestRunCollectionRetriesArchiveNotificationWithoutReupload(t *testing.T) {
	workflowState := &statepkg.WorkflowState{
		UUID:         "sample-uuid",
		WorkflowID:   "run-1",
		ExecutionDir: "/work/run-1",
		TaskStates:   map[string]statepkg.TaskState{},
	}
	collectorFake := &archiveRetryCollector{state: workflowState}
	senderFake := &archiveRetrySender{notifyErrors: 1}
	archiverFake := &archiveRetryArchiver{}

	runCollection(collectorFake, senderFake, archiverFake, time.Second, "attempt-1")
	if archiverFake.calls != 1 {
		t.Fatalf("first collection uploaded %d times, want 1", archiverFake.calls)
	}
	if collectorFake.markArchivedCalls != 0 {
		t.Fatalf("archive was marked before callback succeeded: %d", collectorFake.markArchivedCalls)
	}
	if workflowState.ArchiveResult == nil {
		t.Fatal("successful upload was not persisted in local state")
	}

	runCollection(collectorFake, senderFake, archiverFake, time.Second, "attempt-1")
	if archiverFake.calls != 1 {
		t.Fatalf("retry re-uploaded archive %d times, want 1", archiverFake.calls)
	}
	if senderFake.notifyCalls != 2 {
		t.Fatalf("archive callback attempts = %d, want 2", senderFake.notifyCalls)
	}
	if collectorFake.markArchivedCalls != 1 || !workflowState.Archived {
		t.Fatalf("archive was not marked after callback success: calls=%d state=%+v", collectorFake.markArchivedCalls, workflowState)
	}
}

type archiveRetryCollector struct {
	state             *statepkg.WorkflowState
	markArchivedCalls int
}

func (f *archiveRetryCollector) Collect() ([]collector.CollectResult, error) {
	return []collector.CollectResult{{
		Progress: model.WorkflowProgress{
			AgentID: "agent-1", UUID: "sample-uuid",
			Workflow: model.Workflow{ID: "run-1", Status: model.WorkflowStatusSuccess},
		},
		UUIDDir:      "/work/sample-uuid",
		ExecutionDir: "/work/run-1",
		NeedPush:     true,
		State:        f.state,
	}}, nil
}

func (f *archiveRetryCollector) SaveState(_ string, state *statepkg.WorkflowState) error {
	f.state = state
	return nil
}

func (f *archiveRetryCollector) MarkOutputsPushed(string) error { return nil }

func (f *archiveRetryCollector) MarkArchived(_ string) error {
	f.markArchivedCalls++
	f.state.Archived = true
	return nil
}

type archiveRetrySender struct {
	notifyErrors int
	notifyCalls  int
}

func (f *archiveRetrySender) SendProgress(*model.WorkflowProgress) error { return nil }
func (f *archiveRetrySender) SendOutput(string, string, string) error    { return nil }
func (f *archiveRetrySender) NotifyArchived(*model.ArchiveResult) error {
	f.notifyCalls++
	if f.notifyErrors > 0 {
		f.notifyErrors--
		return context.DeadlineExceeded
	}
	return nil
}

type archiveRetryArchiver struct{ calls int }

func (f *archiveRetryArchiver) ArchiveWorkflowWithPrefix(context.Context, string, string, string, string) (*model.ArchiveResult, error) {
	f.calls++
	return &model.ArchiveResult{UUID: "sample-uuid", WorkflowID: "run-1", AgentID: "agent-1", ObjectPrefix: "attempt-1", ArchivedCount: 3}, nil
}
