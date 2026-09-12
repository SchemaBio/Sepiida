package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/SchemaBio/Sepiida/internal/common/tokenrevoke"
)

type TokenRevokeHandler struct {
	store  *tokenrevoke.Store
	secret string
}

func NewTokenRevokeHandler(store *tokenrevoke.Store, secret string) *TokenRevokeHandler {
	return &TokenRevokeHandler{store: store, secret: strings.TrimSpace(secret)}
}

func (h *TokenRevokeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.secret == "" || h.store == nil {
		http.Error(w, "task token revocation is not configured", http.StatusUnauthorized)
		return
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.Fields(auth)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] != h.secret {
		http.Error(w, "invalid revocation credentials", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		JTI string `json:"jti"`
		Exp int64  `json:"exp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.JTI) == "" {
		http.Error(w, "jti is required", http.StatusBadRequest)
		return
	}
	if err := h.store.Revoke(strings.TrimSpace(req.JTI), req.Exp); err != nil {
		http.Error(w, "token revocation service unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
