package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/common/apikey"
	"github.com/SchemaBio/Sepiida/internal/common/tasktoken"
)

type contextKey string

const taskTokenClaimsKey contextKey = "task_token_claims"

// AgentAuthMiddleware provides API key authentication for agent operations (push data)
type AgentAuthMiddleware struct {
	keyMgr          *apikey.KeyManager
	taskTokenSecret string
}

// NewAgentAuthMiddleware creates a new agent authentication middleware
func NewAgentAuthMiddleware(keyMgr *apikey.KeyManager, taskTokenSecret string) *AgentAuthMiddleware {
	return &AgentAuthMiddleware{keyMgr: keyMgr, taskTokenSecret: taskTokenSecret}
}

// Middleware returns the authentication middleware function
func (a *AgentAuthMiddleware) Middleware(next http.Handler) http.Handler {
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

		if tasktoken.LooksLike(apiKey) && a.taskTokenSecret != "" {
			claims, err := tasktoken.Validate(a.taskTokenSecret, apiKey)
			if err != nil {
				http.Error(w, "invalid task token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), taskTokenClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Validate API key using agent key manager
		if !a.keyMgr.Validate(apiKey) {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}

		// Add API key to context for later use
		ctx := context.WithValue(r.Context(), "api_key", apiKey)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TaskTokenClaims(ctx context.Context) (*tasktoken.Claims, bool) {
	claims, ok := ctx.Value(taskTokenClaimsKey).(*tasktoken.Claims)
	return claims, ok
}

// QueryAuthMiddleware provides API key authentication for query operations
type QueryAuthMiddleware struct {
	keyMgr *apikey.KeyManager
}

// NewQueryAuthMiddleware creates a new query authentication middleware
func NewQueryAuthMiddleware(keyMgr *apikey.KeyManager) *QueryAuthMiddleware {
	return &QueryAuthMiddleware{keyMgr: keyMgr}
}

// Middleware returns the authentication middleware function
func (q *QueryAuthMiddleware) Middleware(next http.Handler) http.Handler {
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

		// Validate API key using query key manager
		if !q.keyMgr.Validate(apiKey) {
			http.Error(w, "invalid query api key", http.StatusUnauthorized)
			return
		}

		// Add API key to context for later use
		ctx := context.WithValue(r.Context(), "api_key", apiKey)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
