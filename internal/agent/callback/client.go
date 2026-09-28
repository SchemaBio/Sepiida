// Package callback routes authenticated node callbacks over a private HTTPS
// path when one is configured, while retaining the public URL as a fallback.
package callback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	privateConnectTimeout = 3 * time.Second
	privateTLSTimeout     = 5 * time.Second
	callbackHeaderTimeout = 10 * time.Second
	privateCooldown       = time.Minute
)

// Client is an http.RoundTripper which keeps the public request URL, Host
// header, and TLS SNI intact while dialing PrivateAddress first. It is safe for
// concurrent progress, archive, and credential callbacks to share one Client.
type Client struct {
	privateAddress          string
	privateTransport        http.RoundTripper
	publicTransport         http.RoundTripper
	now                     func() time.Time
	mu                      sync.Mutex
	privateUnavailableUntil time.Time
}

// NewClient creates a callback transport. privateAddress must be an RFC1918
// IPv4 address and port, for example 10.0.8.12:443. An empty address preserves
// the existing public callback behavior.
func NewClient(privateAddress string) (*Client, error) {
	address, err := normalizePrivateAddress(privateAddress)
	if err != nil {
		return nil, err
	}
	public := cloneDefaultTransport()
	client := &Client{
		privateAddress:  address,
		publicTransport: public,
		now:             time.Now,
	}
	if address != "" {
		private := cloneDefaultTransport()
		private.Proxy = nil
		dialer := &net.Dialer{Timeout: privateConnectTimeout, KeepAlive: 30 * time.Second}
		private.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		}
		private.TLSHandshakeTimeout = privateTLSTimeout
		private.ResponseHeaderTimeout = callbackHeaderTimeout
		client.privateTransport = private
	}
	return client, nil
}

// DirectClient returns a transport for callers that do not have a private
// route. It cannot fail and retains the standard public transport behavior.
func DirectClient() *Client {
	client, _ := NewClient("")
	return client
}

// PrivateAddress returns the validated private dial target, without exposing a
// request URL or credential.
func (c *Client) PrivateAddress() string {
	if c == nil {
		return ""
	}
	return c.privateAddress
}

// SetPrivateCooldownUntil restores a persisted node-callback cooldown before
// the first request in a short-lived helper process.
func (c *Client) SetPrivateCooldownUntil(deadline time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.privateUnavailableUntil = deadline
	c.mu.Unlock()
}

// PrivateCooldownUntil returns the current route cooldown for persistence by a
// short-lived node-callback helper process.
func (c *Client) PrivateCooldownUntil() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.privateUnavailableUntil
}

// ValidatePrivateAddress validates an operator-owned private dial target.
func ValidatePrivateAddress(value string) error {
	_, err := normalizePrivateAddress(value)
	return err
}

func normalizePrivateAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("private callback address must be IPv4:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || !isRFC1918(ip.To4()) {
		return "", errors.New("private callback address must use an RFC1918 IPv4 address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("private callback address must include a valid port")
	}
	return net.JoinHostPort(ip.To4().String(), strconv.Itoa(port)), nil
}

func isRFC1918(ip net.IP) bool {
	return ip[0] == 10 ||
		(ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) ||
		(ip[0] == 192 && ip[1] == 168)
}

func cloneDefaultTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return &http.Transport{Proxy: http.ProxyFromEnvironment}
}

// RoundTrip attempts the private route first unless it is in its short
// cooldown window. It retries only connection/TLS failures and HTTP 5xx
// responses. Authentication failures, redirects, and rate limits stay on the
// original route so they cannot be bypassed by changing network paths.
func (c *Client) RoundTrip(request *http.Request) (*http.Response, error) {
	if c == nil {
		return http.DefaultTransport.RoundTrip(request)
	}
	if c.privateAddress == "" {
		return c.publicTransport.RoundTrip(request)
	}
	if request == nil || request.URL == nil || !strings.EqualFold(request.URL.Scheme, "https") {
		return nil, errors.New("private callback routing requires an HTTPS request URL")
	}

	privateFirst := c.shouldTryPrivate()
	firstTransport, secondTransport := c.publicTransport, c.privateTransport
	firstName, secondName := "public", "private"
	if privateFirst {
		firstTransport, secondTransport = c.privateTransport, c.publicTransport
		firstName, secondName = "private", "public"
	}

	response, err := firstTransport.RoundTrip(request)
	if !retryable(response, err) {
		if firstName == "private" {
			c.markPrivateHealthy()
		}
		return response, err
	}
	if firstName == "private" {
		c.markPrivateFailed()
	}
	if !canReplay(request) {
		return response, err
	}
	log.Printf("callback route service=%s switch=%s->%s reason=%s", safeServiceName(request), firstName, secondName, retryReason(response, err))
	discardResponse(response)
	retry, retryErr := replayRequest(request)
	if retryErr != nil {
		return nil, retryErr
	}
	response, err = secondTransport.RoundTrip(retry)
	if secondName == "private" {
		if retryable(response, err) {
			c.markPrivateFailed()
		} else {
			c.markPrivateHealthy()
		}
	}
	return response, err
}

func (c *Client) shouldTryPrivate() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.now().Before(c.privateUnavailableUntil)
}

func (c *Client) markPrivateFailed() {
	c.mu.Lock()
	c.privateUnavailableUntil = c.now().Add(privateCooldown)
	c.mu.Unlock()
}

func (c *Client) markPrivateHealthy() {
	c.mu.Lock()
	c.privateUnavailableUntil = time.Time{}
	c.mu.Unlock()
}

func retryable(response *http.Response, err error) bool {
	return err != nil || (response != nil && response.StatusCode >= http.StatusInternalServerError)
}

func canReplay(request *http.Request) bool {
	return request.Body == nil || request.GetBody != nil
}

func replayRequest(request *http.Request) (*http.Request, error) {
	retry := request.Clone(request.Context())
	retry.Header = request.Header.Clone()
	if request.Body != nil {
		body, err := request.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	return retry, nil
}

func discardResponse(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
}

func retryReason(response *http.Response, err error) string {
	if err != nil {
		return "network_or_tls"
	}
	if response != nil {
		return "http_" + strconv.Itoa(response.StatusCode)
	}
	return "network_or_tls"
}

func safeServiceName(request *http.Request) string {
	if request == nil || request.URL == nil {
		return "unknown"
	}
	return request.URL.Hostname()
}
