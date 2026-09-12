package tokenrevoke

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Persistence is implemented by the service database. Keeping this small
// interface here avoids coupling token validation to a concrete SQL driver
// while allowing deployments to survive restarts and run multiple instances.
type Persistence interface {
	PersistTokenRevocation(ctx context.Context, jti string, exp int64) error
	IsTaskTokenRevoked(ctx context.Context, jti string, now int64) (bool, error)
}

type Store struct {
	mu      sync.Mutex
	revoked map[string]int64
	persist Persistence
}

func NewStore() *Store {
	return &Store{revoked: make(map[string]int64)}
}

func (s *Store) SetPersistence(persist Persistence) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persist = persist
}

func (s *Store) Revoke(jti string, exp int64) error {
	if s == nil || jti == "" {
		return fmt.Errorf("token revocation store is not initialized")
	}
	if exp <= 0 {
		exp = time.Now().Add(24 * time.Hour).Unix()
	}
	s.mu.Lock()
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		if err := persist.PersistTokenRevocation(context.Background(), jti, exp); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[jti] = exp
	return nil
}

func (s *Store) Revoked(jti string) bool {
	revoked, _ := s.RevokedWithError(context.Background(), jti)
	return revoked
}

// RevokedWithError checks the durable store before the local cache. A database
// failure is returned to the caller so authenticated agent writes can fail
// closed instead of accepting a token whose revocation status is unknown.
func (s *Store) RevokedWithError(ctx context.Context, jti string) (bool, error) {
	if s == nil || jti == "" {
		return false, nil
	}
	now := time.Now().Unix()
	s.mu.Lock()
	persist := s.persist
	// Read the local cache while holding the mutex, then release it before
	// querying PostgreSQL. The explicit unlock/relock makes every return path
	// balanced, including persistence errors, and avoids holding the cache lock
	// across network I/O.
	exp, cached := s.revoked[jti]
	if cached && (exp <= 0 || exp >= now) {
		s.mu.Unlock()
		return true, nil
	}
	if persist == nil {
		if cached {
			delete(s.revoked, jti)
		}
		s.mu.Unlock()
		return false, nil
	}
	s.mu.Unlock()

	revoked, err := persist.IsTaskTokenRevoked(ctx, jti, now)
	if err != nil {
		return false, err
	}
	if !revoked {
		return false, nil
	}
	s.mu.Lock()
	s.revoked[jti] = now + 24*60*60
	s.mu.Unlock()
	return true, nil
}
