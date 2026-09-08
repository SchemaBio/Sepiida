package archiver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
