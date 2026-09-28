package callback

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPrivateCallbackAddressValidation(t *testing.T) {
	for _, value := range []string{"10.1.2.3:443", "172.16.1.2:8443", "192.168.2.3:443"} {
		if err := ValidatePrivateAddress(value); err != nil {
			t.Fatalf("expected %q to be valid: %v", value, err)
		}
	}
	for _, value := range []string{"127.0.0.1:443", "8.8.8.8:443", "10.0.0.1", "10.0.0.1:0", "[fd00::1]:443"} {
		if err := ValidatePrivateAddress(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestPrivateRoutePreservesURLHostAndFallsBackOn5xx(t *testing.T) {
	privateCalls, publicCalls := 0, 0
	var privateBody, publicBody string
	client := testClient(
		roundTripFunc(func(request *http.Request) (*http.Response, error) {
			privateCalls++
			if request.URL.Host != "sepiida.example:443" || request.Host != "sepiida.example:443" {
				t.Errorf("private route changed request authority: URL=%q Host=%q", request.URL.Host, request.Host)
			}
			body, _ := io.ReadAll(request.Body)
			privateBody = string(body)
			return response(http.StatusServiceUnavailable), nil
		}),
		roundTripFunc(func(request *http.Request) (*http.Response, error) {
			publicCalls++
			body, _ := io.ReadAll(request.Body)
			publicBody = string(body)
			return response(http.StatusOK), nil
		}),
	)
	request, err := http.NewRequest(http.MethodPost, "https://sepiida.example:443/api/v1/progress", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.RoundTrip(request)
	if err != nil || got.StatusCode != http.StatusOK {
		t.Fatalf("fallback failed: response=%v err=%v", got, err)
	}
	got.Body.Close()
	if privateCalls != 1 || publicCalls != 1 || privateBody != "payload" || publicBody != "payload" {
		t.Fatalf("unexpected replay: private=%d public=%d privateBody=%q publicBody=%q", privateCalls, publicCalls, privateBody, publicBody)
	}
}

func TestPrivateRouteDoesNotFallbackForBusinessResponse(t *testing.T) {
	privateCalls, publicCalls := 0, 0
	client := testClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			privateCalls++
			return response(http.StatusForbidden), nil
		}),
		roundTripFunc(func(*http.Request) (*http.Response, error) { publicCalls++; return response(http.StatusOK), nil }),
	)
	request, _ := http.NewRequest(http.MethodPost, "https://sepiida.example/api", bytes.NewReader([]byte("payload")))
	got, err := client.RoundTrip(request)
	if err != nil || got.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected response=%v err=%v", got, err)
	}
	got.Body.Close()
	if privateCalls != 1 || publicCalls != 0 {
		t.Fatalf("business response should not route around private: private=%d public=%d", privateCalls, publicCalls)
	}
}

func TestPublicFailureCanProbePrivateDuringCooldown(t *testing.T) {
	privateCalls, publicCalls := 0, 0
	now := time.Now()
	client := testClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) { privateCalls++; return response(http.StatusOK), nil }),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			publicCalls++
			return nil, errors.New("public unavailable")
		}),
	)
	client.now = func() time.Time { return now }
	client.privateUnavailableUntil = now.Add(time.Minute)
	request, _ := http.NewRequest(http.MethodGet, "https://sepiida.example/api", nil)
	got, err := client.RoundTrip(request)
	if err != nil || got.StatusCode != http.StatusOK {
		t.Fatalf("private recovery probe failed: response=%v err=%v", got, err)
	}
	got.Body.Close()
	if publicCalls != 1 || privateCalls != 1 {
		t.Fatalf("expected public then private recovery probe, got public=%d private=%d", publicCalls, privateCalls)
	}
}

func TestPrivateRouteRejectsHTTP(t *testing.T) {
	client := testClient(roundTripFunc(func(*http.Request) (*http.Response, error) { return response(http.StatusOK), nil }), roundTripFunc(func(*http.Request) (*http.Response, error) { return response(http.StatusOK), nil }))
	request, _ := http.NewRequest(http.MethodGet, "http://sepiida.example/api", nil)
	if _, err := client.RoundTrip(request); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS requirement error, got %v", err)
	}
}

func testClient(private, public http.RoundTripper) *Client {
	return &Client{privateAddress: "10.1.2.3:443", privateTransport: private, publicTransport: public, now: time.Now}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("body"))}
}
