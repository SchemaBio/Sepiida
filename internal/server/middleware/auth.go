package middleware

import (
	"context"
	"net/http"
	"strings"
)

// AuthMiddleware provides API key authentication
type AuthMiddleware struct {
	validateKey func(ctx context.Context, key string) (bool, error)
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(validateKey func(ctx context.Context, key string) (bool, error)) *AuthMiddleware {
	return &AuthMiddleware{validateKey: validateKey}
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

		// Validate API key
		valid, err := a.validateKey(r.Context(), apiKey)
		if err != nil {
			http.Error(w, "failed to validate api key", http.StatusInternalServerError)
			return
		}

		if !valid {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}

		// Add API key to context for later use
		ctx := context.WithValue(r.Context(), "api_key", apiKey)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}