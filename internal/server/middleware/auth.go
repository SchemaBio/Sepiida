package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/common/apikey"
)

// AuthMiddleware provides API key authentication
type AuthMiddleware struct {
	keyMgr *apikey.KeyManager
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(keyMgr *apikey.KeyManager) *AuthMiddleware {
	return &AuthMiddleware{keyMgr: keyMgr}
}

// Middleware returns the authentication middleware function
func (a *AuthMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		// Parse Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "invalid authorization format", http.StatusUnauthorized)
			return
		}

		apiKey := parts[1]

		// Validate API key using dynamic key manager
		if !a.keyMgr.Validate(apiKey) {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}

		// Add API key to context for later use
		ctx := context.WithValue(r.Context(), "api_key", apiKey)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}