package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type ipRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateWindow
	maxReqs int
	window  time.Duration
}

type rateWindow struct {
	count int
	start time.Time
}

func newIPRateLimiter(maxReqs int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{entries: make(map[string]*rateWindow), maxReqs: maxReqs, window: window}
}

func (l *ipRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l != nil && !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenRateLimiter scopes authenticated machine callbacks by their task token
// (one token per execution attempt), keeping callback traffic independent from
// the public/IP limiter.
type tokenRateLimiter struct{ *ipRateLimiter }

func newTokenRateLimiter(maxReqs int, window time.Duration) *tokenRateLimiter {
	return &tokenRateLimiter{ipRateLimiter: newIPRateLimiter(maxReqs, window)}
}

func (l *tokenRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		if key == "" {
			key = clientIP(r)
		}
		if l != nil && !l.allow(key) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *ipRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.start) > l.window {
		l.entries[key] = &rateWindow{count: 1, start: now}
		return true
	}
	if entry.count >= l.maxReqs {
		return false
	}
	entry.count++
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
