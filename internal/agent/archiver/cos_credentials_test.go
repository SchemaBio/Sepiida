package archiver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestArchiveUsesResolvedCredentialsInsteadOfEnvironment(t *testing.T) {
	t.Setenv("SEPIIDA_CREDENTIALS_URL", "https://old.invalid/credentials")
	t.Setenv("SEPIIDA_TASK_TOKEN", "old-token")
	t.Setenv("SEPIIDA_NODE_SECRET", "old-secret")
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	credentialRequests, uploads := 0, 0
	http.DefaultTransport = credentialRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Host {
		case "node.test":
			credentialRequests++
			if r.Header.Get("Authorization") != "Bearer cli-token" || r.Header.Get("x-node-secret") != "cli-secret" {
				t.Error("credential renewal did not use resolved CLI overrides")
			}
			body = fmt.Sprintf(`{"secret_id":"id","secret_key":"key","session_token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "bucket-1250000000.cos.ap-guangzhou.myqcloud.com":
			uploads++
			if r.Header.Get("x-node-secret") != "" || r.Header.Get("Authorization") == "Bearer cli-token" {
				t.Error("node credentials were forwarded to object storage")
			}
			if r.Header.Get("x-cos-security-token") != "session" {
				t.Error("upload did not use the renewed COS credentials")
			}
		default:
			t.Fatalf("unexpected request to %s", r.URL.Host)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	arch, err := NewFromPathWithNodeCredentials("cos://ap-guangzhou/bucket-1250000000/results", "", "", NodeCredentials{
		Endpoint: "https://node.test/credentials", TaskToken: "cli-token", NodeSecret: "cli-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer arch.Close()
	if err := arch.backend.Upload(context.Background(), "result.txt", strings.NewReader("ok"), 2); err != nil {
		t.Fatal(err)
	}
	if credentialRequests != 1 || uploads != 1 {
		t.Fatalf("credential requests=%d uploads=%d, want one of each", credentialRequests, uploads)
	}
}

func TestCredentialRejectionReportsRayWithoutResponseBody(t *testing.T) {
	credentials := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("CF-Ray", "credential-ray-LHR")
		http.Error(w, "private-response-content", http.StatusForbidden)
	}))
	defer credentials.Close()
	transport := &renewableCOSTransport{endpoint: credentials.URL, token: "task-token"}
	_, err := transport.RoundTrip(httptest.NewRequest(http.MethodPut, "http://object.invalid/result", nil))
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "credential-ray-LHR") || strings.Contains(err.Error(), "private-response-content") {
		t.Fatalf("incorrect credential failure diagnostic: %v", err)
	}
}

func TestCredentialRedirectDoesNotForwardSecrets(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("credential request followed a redirect")
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	transport := &renewableCOSTransport{endpoint: origin.URL, token: "task-token", nodeSecret: "test-edge-secret"}
	_, err := transport.RoundTrip(httptest.NewRequest(http.MethodPut, "http://object.invalid/result", nil))
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect was not reported: %v", err)
	}
}

type credentialRoundTripFunc func(*http.Request) (*http.Response, error)

func (f credentialRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestCOSCredentialsRenewAndIncludeSessionToken(t *testing.T) {
	renewals := 0
	credentials := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer task-token" {
			t.Error("credential request missing node identity")
		}
		renewals++
		fmt.Fprintf(w, `{"secret_id":"temporary-id","secret_key":"temporary-key","session_token":"session-%d","expires_at":%d}`, renewals, time.Now().Add(30*time.Minute).Unix())
	}))
	defer credentials.Close()
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-cos-security-token"); got != fmt.Sprintf("session-%d", renewals) {
			t.Errorf("missing or stale COS session token: %q", got)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("object request unsigned")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer object.Close()
	transport := &renewableCOSTransport{endpoint: credentials.URL, token: "task-token"}
	client := &http.Client{Transport: transport}
	for i := 0; i < 3; i++ {
		if i == 2 {
			transport.expires = time.Now()
		}
		resp, err := client.Get(object.URL + "/attempt/result.bam")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if renewals != 2 {
		t.Fatalf("expected cached credentials then renewal, got %d requests", renewals)
	}
}

func TestCOSCredentialsRenewalFailureIsReturned(t *testing.T) {
	credentials := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer credentials.Close()

	transport := &renewableCOSTransport{endpoint: credentials.URL, token: "task-token"}
	client := &http.Client{Transport: transport}
	response, err := client.Get("http://object.invalid/result")
	if err == nil || response != nil {
		t.Fatalf("expected credential renewal error, response=%v err=%v", response, err)
	}
	if got := err.Error(); !strings.Contains(got, "invalid temporary credential response") {
		t.Fatalf("unexpected renewal error: %q", got)
	}
}

func TestCOSCredentialsRenewalDoesNotReuseExpiredSession(t *testing.T) {
	renewals := 0
	credentials := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		renewals++
		fmt.Fprintf(w, `{"secret_id":"id","secret_key":"key","session_token":"session-%d","expires_at":%d}`, renewals, time.Now().Add(30*time.Minute).Unix())
	}))
	defer credentials.Close()

	transport := &renewableCOSTransport{endpoint: credentials.URL, token: "task-token"}
	transport.expires = time.Now().Add(-time.Minute)
	// The underlying request is expected to fail because the host is invalid,
	// but the transport must still refresh once before forwarding it.
	_, _ = transport.RoundTrip(httptest.NewRequest(http.MethodGet, "http://object.invalid/result", nil))
	if renewals != 1 {
		t.Fatalf("expected one renewal after expiry, got %d", renewals)
	}
}

func TestCOSCredentialsIncludesNodeSecret(t *testing.T) {
	var gotHeader string
	credentials := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-node-secret")
		fmt.Fprintf(w, `{"secret_id":"temporary-id","secret_key":"temporary-key","session_token":"session-1","expires_at":%d}`, time.Now().Add(30*time.Minute).Unix())
	}))
	defer credentials.Close()

	transport := &renewableCOSTransport{
		endpoint:   credentials.URL,
		token:      "task-token",
		nodeSecret: "nJ6RLC6LU2NwmTvEwZOtazUSgzkQ3LOyGa0QcJfjGsg=",
	}
	_, _ = transport.RoundTrip(httptest.NewRequest(http.MethodGet, "http://object.invalid/result", nil))
	if gotHeader != "nJ6RLC6LU2NwmTvEwZOtazUSgzkQ3LOyGa0QcJfjGsg=" {
		t.Fatalf("expected x-node-secret to be %q, got %q", "nJ6RLC6LU2NwmTvEwZOtazUSgzkQ3LOyGa0QcJfjGsg=", gotHeader)
	}
}
