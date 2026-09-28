package callback

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPrivateRouteUsesPublicHostAndTLSName(t *testing.T) {
	certificate, certificatePEM := testCertificate(t, "sepiida.example")
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certificatePEM)
	requests := make(chan *http.Request, 1)
	privateAddress := startTLSServer(t, certificate, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests <- request
		w.WriteHeader(http.StatusNoContent)
	}))
	client := &Client{
		privateAddress:   "10.1.2.3:443",
		privateTransport: testTLSTransport(privateAddress, roots),
		publicTransport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("public route should not be used")
			return nil, nil
		}),
		now: time.Now,
	}
	request, err := http.NewRequest(http.MethodPost, "https://sepiida.example/api/v1/progress", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusNoContent {
		t.Fatalf("private TLS request failed: response=%v err=%v", response, err)
	}
	response.Body.Close()
	select {
	case received := <-requests:
		if received.Host != "sepiida.example" || received.TLS == nil || received.TLS.ServerName != "sepiida.example" {
			t.Fatalf("private connection lost public authority: Host=%q TLS=%#v", received.Host, received.TLS)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("private TLS server did not receive a callback")
	}
}

func TestPrivateCertificateFailureFallsBackPublic(t *testing.T) {
	privateCertificate, privateRoots := testCertificate(t, "wrong.example")
	publicCertificate, publicRoots := testCertificate(t, "sepiida.example")
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(privateRoots)
	roots.AppendCertsFromPEM(publicRoots)
	privateAddress := startTLSServer(t, privateCertificate, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("invalid private certificate must not reach an HTTP handler")
	}))
	publicRequests := make(chan *http.Request, 1)
	publicAddress := startTLSServer(t, publicCertificate, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		publicRequests <- request
		w.WriteHeader(http.StatusNoContent)
	}))
	client := &Client{
		privateAddress:   "10.1.2.3:443",
		privateTransport: testTLSTransport(privateAddress, roots),
		publicTransport:  testTLSTransport(publicAddress, roots),
		now:              time.Now,
	}
	request, _ := http.NewRequest(http.MethodPost, "https://sepiida.example/api/v1/progress", strings.NewReader("payload"))
	response, err := client.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusNoContent {
		t.Fatalf("public fallback after TLS failure failed: response=%v err=%v", response, err)
	}
	response.Body.Close()
	select {
	case received := <-publicRequests:
		if received.Host != "sepiida.example" || received.TLS == nil || received.TLS.ServerName != "sepiida.example" {
			t.Fatalf("public fallback lost public authority: Host=%q TLS=%#v", received.Host, received.TLS)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("public fallback did not reach the public server")
	}
}

func testCertificate(t *testing.T, hostname string) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: hostname},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{hostname},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, certPEM
}

func startTLSServer(t *testing.T, certificate tls.Certificate, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}}))
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	return listener.Addr().String()
}

func testTLSTransport(address string, roots *x509.CertPool) *http.Transport {
	dialer := &net.Dialer{}
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		TLSClientConfig:     &tls.Config{RootCAs: roots},
		TLSHandshakeTimeout: time.Second,
	}
}
