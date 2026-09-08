package archiver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// renewableCOSTransport refreshes credentials between requests, so a multipart
// archive can outlive an STS session without restarting the agent.
type renewableCOSTransport struct {
	endpoint, token string
	mu              sync.Mutex
	transport       *cos.AuthorizationTransport
	expires         time.Time
}

func (t *renewableCOSTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	if t.transport == nil || time.Until(t.expires) < 2*time.Minute {
		req, err := http.NewRequestWithContext(request.Context(), http.MethodGet, t.endpoint, nil)
		if err != nil {
			t.mu.Unlock()
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+t.token)
		client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.mu.Unlock()
			return nil, fmt.Errorf("credential renewal unavailable")
		}
		var data struct {
			SecretID     string `json:"secret_id"`
			SecretKey    string `json:"secret_key"`
			SessionToken string `json:"session_token"`
			Expires      int64  `json:"expires_at"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&data)
		resp.Body.Close()
		if resp.StatusCode != 200 || decodeErr != nil || data.SecretID == "" || data.SecretKey == "" || data.SessionToken == "" || data.Expires <= time.Now().Unix() {
			t.mu.Unlock()
			return nil, fmt.Errorf("invalid temporary credential response")
		}
		t.transport = &cos.AuthorizationTransport{SecretID: data.SecretID, SecretKey: data.SecretKey, SessionToken: data.SessionToken}
		t.expires = time.Unix(data.Expires, 0)
	}
	transport := t.transport
	t.mu.Unlock()
	return transport.RoundTrip(request)
}
