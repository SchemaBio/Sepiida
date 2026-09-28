package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SchemaBio/Sepiida/internal/agent/callback"
)

const maxNodeCallbackPayloadBytes = 128 << 10

type nodeCallbackRouteState struct {
	PrivateCooldownUntil int64 `json:"private_cooldown_until,omitempty"`
}

type nodeCallbackRoute interface {
	http.RoundTripper
	SetPrivateCooldownUntil(time.Time)
	PrivateCooldownUntil() time.Time
}

var newNodeCallbackRoute = func(privateAddr string) (nodeCallbackRoute, error) {
	return callback.NewClient(privateAddr)
}

// runNodeCallback is a compact helper for Squid's bootstrap supervisor. It
// uses the same private-first HTTPS transport as the Agent and prints only
// status, a sanitized Ray ID, and a failure class to stdout.
func runNodeCallback(rawURL, privateAddr, stateFile string) int {
	return runNodeCallbackIO(rawURL, privateAddr, stateFile, os.Stdin, os.Stdout)
}

func runNodeCallbackIO(rawURL, privateAddr, stateFile string, input io.Reader, output io.Writer) int {
	result := func(status int, ray, class string) int {
		fmt.Fprintf(output, "%d|%s|%s\n", status, sanitizeRay(ray), class)
		if status == http.StatusNoContent {
			return 0
		}
		return 1
	}
	endpoint, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" || endpoint.User != nil {
		return result(0, "", "network")
	}
	if err := callback.ValidatePrivateAddress(privateAddr); err != nil || strings.TrimSpace(privateAddr) == "" {
		return result(0, "", "network")
	}
	token := strings.TrimSpace(os.Getenv("SEPIIDA_TASK_TOKEN"))
	if token == "" {
		return result(0, "", "auth")
	}
	body, err := io.ReadAll(io.LimitReader(input, maxNodeCallbackPayloadBytes+1))
	if err != nil || len(body) > maxNodeCallbackPayloadBytes || !json.Valid(body) {
		return result(0, "", "network")
	}
	route, err := newNodeCallbackRoute(privateAddr)
	if err != nil {
		return result(0, "", "network")
	}
	route.SetPrivateCooldownUntil(loadNodeCallbackCooldown(stateFile))
	defer saveNodeCallbackCooldown(stateFile, route.PrivateCooldownUntil())
	request, err := http.NewRequest(http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return result(0, "", "network")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if secret := strings.TrimSpace(os.Getenv("SEPIIDA_NODE_SECRET")); secret != "" {
		request.Header.Set("x-node-secret", secret)
	} else if secret := strings.TrimSpace(os.Getenv("CVM_NODE_SECRET")); secret != "" {
		request.Header.Set("x-node-secret", secret)
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: route, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return result(0, "", classifyNodeCallbackError(err))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	class := "upstream"
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		class = "auth"
	} else if response.StatusCode == http.StatusTooManyRequests {
		class = "rate_limited"
	}
	return result(response.StatusCode, response.Header.Get("CF-Ray"), class)
}

func loadNodeCallbackCooldown(path string) time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	var state nodeCallbackRouteState
	if json.Unmarshal(data, &state) != nil || state.PrivateCooldownUntil <= 0 {
		return time.Time{}
	}
	return time.Unix(state.PrivateCooldownUntil, 0)
}

func saveNodeCallbackCooldown(path string, deadline time.Time) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	// Short-lived helper invocations can overlap while the bootstrap supervisor
	// posts diagnostics and status. Preserve the longer cooldown so a successful
	// sibling cannot immediately erase another helper's private-route failure.
	if existing := loadNodeCallbackCooldown(path); existing.After(deadline) {
		deadline = existing
	}
	state := nodeCallbackRouteState{}
	if !deadline.IsZero() {
		state.PrivateCooldownUntil = deadline.Unix()
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return
	}
	temporary, err := os.CreateTemp(directory, ".sepiida-node-route-")
	if err != nil {
		return
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return
	}
	if err := json.NewEncoder(temporary).Encode(state); err != nil || temporary.Close() != nil {
		return
	}
	_ = os.Rename(name, path)
}

func classifyNodeCallbackError(err error) string {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return "tls"
	}
	var recordHeader tls.RecordHeaderError
	if errors.As(err, &recordHeader) {
		return "tls"
	}
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return "tls"
	}
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &certificateInvalid) {
		return "tls"
	}
	return "network"
}

func sanitizeRay(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return -1
	}, value)
}
