package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SchemaBio/Sepiida/internal/common/apikey"
	"github.com/SchemaBio/Sepiida/internal/common/tasktoken"
)

func TestAgentAuthAcceptsTaskTokenAndSetsClaims(t *testing.T) {
	token, err := tasktoken.Generate("shared-secret", "sample-uuid", "agent-1", time.Hour)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	middleware := NewAgentAuthMiddleware(apikey.NewKeyManager(filepath.Join(t.TempDir(), "missing.txt")), "shared-secret", false)
	nextCalled := false
	handler := middleware.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		claims, ok := TaskTokenClaims(r.Context())
		if !ok {
			t.Fatal("expected task token claims in request context")
		}
		if claims.UUID != "sample-uuid" || claims.AgentID != "agent-1" {
			t.Fatalf("unexpected claims: %+v", claims)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/progress", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !nextCalled {
		t.Fatalf("expected request to pass, status=%d called=%t body=%s", rec.Code, nextCalled, rec.Body.String())
	}
}

func TestAgentAuthRejectsStaticKeyWhenDisabled(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("agent-key\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
	keyMgr := apikey.NewKeyManager(keyFile)
	if err := keyMgr.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	middleware := NewAgentAuthMiddleware(keyMgr, "shared-secret", false)
	handler := middleware.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("static key should not reach next handler")
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/progress", nil)
	req.Header.Set("Authorization", "Bearer agent-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestQueryAuthAcceptsQueryKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(keyFile, []byte("query-key\n"), 0o644); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
	keyMgr := apikey.NewKeyManager(keyFile)
	if err := keyMgr.Reload(); err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	middleware := NewQueryAuthMiddleware(keyMgr)
	nextCalled := false
	handler := middleware.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	req.Header.Set("Authorization", "Bearer query-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !nextCalled {
		t.Fatalf("expected query key to pass, status=%d called=%t body=%s", rec.Code, nextCalled, rec.Body.String())
	}
}
