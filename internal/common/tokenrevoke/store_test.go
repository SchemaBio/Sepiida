package tokenrevoke

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRevokeStore(t *testing.T) {
	store := NewStore()
	if store.Revoked("missing") {
		t.Fatal("missing jti should not be revoked")
	}
	store.Revoke("abc", 0)
	if !store.Revoked("abc") {
		t.Fatal("expected revoked jti")
	}
}

type fakePersistence struct {
	mu      sync.Mutex
	revoked map[string]int64
	err     error
}

func (p *fakePersistence) PersistTokenRevocation(_ context.Context, jti string, exp int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if p.revoked == nil {
		p.revoked = make(map[string]int64)
	}
	if exp > p.revoked[jti] {
		p.revoked[jti] = exp
	}
	return nil
}

func (p *fakePersistence) IsTaskTokenRevoked(_ context.Context, jti string, now int64) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return false, p.err
	}
	exp, ok := p.revoked[jti]
	return ok && exp >= now, nil
}

func TestRevokeStorePersistsAcrossInstances(t *testing.T) {
	persist := &fakePersistence{revoked: make(map[string]int64)}
	first := NewStore()
	first.SetPersistence(persist)
	if err := first.Revoke("jti-cross-instance", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	second := NewStore()
	second.SetPersistence(persist)
	revoked, err := second.RevokedWithError(context.Background(), "jti-cross-instance")
	if err != nil || !revoked {
		t.Fatalf("second store did not observe durable revocation: revoked=%v err=%v", revoked, err)
	}
}

func TestRevokeStoreFailsClosedWhenPersistenceUnavailable(t *testing.T) {
	persist := &fakePersistence{err: errors.New("database unavailable")}
	store := NewStore()
	store.SetPersistence(persist)
	if err := store.Revoke("jti-db-failure", time.Now().Add(time.Hour).Unix()); err == nil {
		t.Fatal("revocation should report persistence failure")
	}
	if revoked, err := store.RevokedWithError(context.Background(), "jti-db-failure"); err == nil || revoked {
		t.Fatalf("revocation lookup should fail closed: revoked=%v err=%v", revoked, err)
	}
}
