package apikey

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	maxKeyFileLineBytes = 1 << 20
	minAPIKeyBytes      = 16
)

// KeyManager manages API keys from a dynamic key file
type KeyManager struct {
	keyFile     string
	keys        map[string]KeyScope
	mu          sync.RWMutex
	lastModTime time.Time
}

// KeyScope describes which query resources a key may read.
type KeyScope struct {
	Unrestricted bool
	WorkflowIDs  map[string]struct{}
	UUIDs        map[string]struct{}
}

// AllowsWorkflow checks whether a scope permits a concrete workflow.
func (s KeyScope) AllowsWorkflow(workflowID, uuid string) bool {
	if s.Unrestricted {
		return true
	}
	if workflowID != "" {
		if _, ok := s.WorkflowIDs[workflowID]; ok {
			return true
		}
	}
	if uuid != "" {
		if _, ok := s.UUIDs[uuid]; ok {
			return true
		}
	}
	return false
}

// Restricted returns true when a key has a scoped allow-list.
func (s KeyScope) Restricted() bool {
	return !s.Unrestricted
}

// NewKeyManager creates a new key manager
func NewKeyManager(keyFile string) *KeyManager {
	return &KeyManager{
		keyFile: keyFile,
		keys:    make(map[string]KeyScope),
	}
}

// Start starts the key file watcher
// It periodically checks the key file for updates
func (km *KeyManager) Start(interval time.Duration) {
	km.StartContext(context.Background(), interval)
}

// StartContext starts the key file watcher and stops it when ctx is canceled.
func (km *KeyManager) StartContext(ctx context.Context, interval time.Duration) {
	if strings.TrimSpace(km.keyFile) == "" {
		return
	}
	// Load keys immediately
	if err := km.loadKeys(); err != nil {
		log.Printf("Warning: failed to load key file %s: %v", km.keyFile, err)
	}
	if interval <= 0 {
		log.Printf("Warning: non-positive key refresh interval %v; key auto-refresh disabled", interval)
		return
	}

	// Start periodic refresh
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := km.loadKeys(); err != nil {
					log.Printf("Warning: failed to refresh key file %s: %v", km.keyFile, err)
				}
			}
		}
	}()
}

// loadKeys loads keys from the key file
// It checks file modification time to avoid unnecessary reloads
func (km *KeyManager) loadKeys() error {
	if strings.TrimSpace(km.keyFile) == "" {
		return nil
	}
	info, err := os.Stat(km.keyFile)
	if err != nil {
		// File doesn't exist or inaccessible, keep existing keys
		return fmt.Errorf("stat key file: %w", err)
	}

	// Check if file was modified since last load
	km.mu.RLock()
	lastMod := km.lastModTime
	km.mu.RUnlock()

	if !info.ModTime().After(lastMod) {
		// File not modified, skip reload
		return nil
	}

	// Read file
	file, err := os.Open(km.keyFile)
	if err != nil {
		return fmt.Errorf("open key file: %w", err)
	}
	defer file.Close()

	newKeys := make(map[string]KeyScope)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), maxKeyFileLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || (len(line) > 0 && line[0] == '#') {
			continue
		}
		key, scope := parseKeyLine(line)
		if key == "" {
			continue
		}
		if isPlaceholderKey(key) {
			log.Printf("Warning: ignoring placeholder API key in %s; replace example key files before use", km.keyFile)
			continue
		}
		if len(key) < minAPIKeyBytes {
			log.Printf("Warning: ignoring weak API key in %s; keys must be at least %d bytes", km.keyFile, minAPIKeyBytes)
			continue
		}
		newKeys[key] = scope
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read key file: %w", err)
	}

	km.mu.Lock()
	km.keys = newKeys
	km.lastModTime = info.ModTime()
	km.mu.Unlock()
	return nil
}

// Validate checks if a key is valid
func (km *KeyManager) Validate(key string) bool {
	km.mu.RLock()
	defer km.mu.RUnlock()
	_, ok := km.keys[key]
	return ok
}

// Scope returns the scope associated with a key.
func (km *KeyManager) Scope(key string) (KeyScope, bool) {
	km.mu.RLock()
	defer km.mu.RUnlock()
	scope, ok := km.keys[key]
	return scope, ok
}

// List returns all valid keys
func (km *KeyManager) List() []string {
	km.mu.RLock()
	defer km.mu.RUnlock()

	keys := make([]string, 0, len(km.keys))
	for k := range km.keys {
		keys = append(keys, k)
	}
	return keys
}

func parseKeyLine(line string) (string, KeyScope) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", KeyScope{}
	}

	scope := KeyScope{Unrestricted: true}
	for _, field := range fields[1:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "workflow", "workflow_id":
			scope.Unrestricted = false
			if scope.WorkflowIDs == nil {
				scope.WorkflowIDs = make(map[string]struct{})
			}
			for _, id := range splitScopeValues(value) {
				scope.WorkflowIDs[id] = struct{}{}
			}
		case "uuid":
			scope.Unrestricted = false
			if scope.UUIDs == nil {
				scope.UUIDs = make(map[string]struct{})
			}
			for _, uuid := range splitScopeValues(value) {
				scope.UUIDs[uuid] = struct{}{}
			}
		default:
			// Treat unknown key=value directives as scoped-but-empty instead of
			// silently granting an unrestricted query key. This fails closed for
			// typos such as "workfow=..." while preserving old unrestricted lines
			// that contain only the key (or inline comments without "=").
			scope.Unrestricted = false
		}
	}

	return fields[0], scope
}

func splitScopeValues(value string) []string {
	rawValues := strings.Split(value, ",")
	values := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		item := strings.TrimSpace(raw)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}

func isPlaceholderKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	compact := strings.NewReplacer("-", "", "_", "", " ", "").Replace(normalized)
	switch compact {
	case "agentkey001", "agentkey002", "agentkey003",
		"querykey001", "querykey002", "querykey003":
		return true
	}
	for _, marker := range []string{
		"changeme",
		"replacewith",
		"placeholder",
		"examplekey",
		"yourapikey",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

// Count returns the number of valid keys
func (km *KeyManager) Count() int {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return len(km.keys)
}

// Reload forces an immediate reload of the key file
func (km *KeyManager) Reload() error {
	km.mu.Lock()
	km.lastModTime = time.Time{} // Reset to force reload
	km.mu.Unlock()
	return km.loadKeys()
}

// MultiKeyManager manages multiple key files (e.g., agent keys and query keys)
type MultiKeyManager struct {
	agentKeyMgr *KeyManager
	queryKeyMgr *KeyManager
}

// NewMultiKeyManager creates a multi key manager
func NewMultiKeyManager(agentKeyFile, queryKeyFile string) *MultiKeyManager {
	return &MultiKeyManager{
		agentKeyMgr: NewKeyManager(agentKeyFile),
		queryKeyMgr: NewKeyManager(queryKeyFile),
	}
}

// Start starts all key managers
func (mkm *MultiKeyManager) Start(interval time.Duration) {
	mkm.agentKeyMgr.Start(interval)
	mkm.queryKeyMgr.Start(interval)
}

// StartContext starts all key managers and ties their watchers to ctx.
func (mkm *MultiKeyManager) StartContext(ctx context.Context, interval time.Duration) {
	mkm.agentKeyMgr.StartContext(ctx, interval)
	mkm.queryKeyMgr.StartContext(ctx, interval)
}

// ValidateAgentKey checks if a key is valid for agent operations (push data)
func (mkm *MultiKeyManager) ValidateAgentKey(key string) bool {
	return mkm.agentKeyMgr.Validate(key)
}

// ValidateQueryKey checks if a key is valid for query operations
func (mkm *MultiKeyManager) ValidateQueryKey(key string) bool {
	return mkm.queryKeyMgr.Validate(key)
}

// GetAgentKeyManager returns the agent key manager
func (mkm *MultiKeyManager) GetAgentKeyManager() *KeyManager {
	return mkm.agentKeyMgr
}

// GetQueryKeyManager returns the query key manager
func (mkm *MultiKeyManager) GetQueryKeyManager() *KeyManager {
	return mkm.queryKeyMgr
}

// AgentKeyCount returns the number of agent keys
func (mkm *MultiKeyManager) AgentKeyCount() int {
	return mkm.agentKeyMgr.Count()
}

// QueryKeyCount returns the number of query keys
func (mkm *MultiKeyManager) QueryKeyCount() int {
	return mkm.queryKeyMgr.Count()
}

// ReloadAll forces reload of all key files.
func (mkm *MultiKeyManager) ReloadAll() error {
	return errors.Join(mkm.agentKeyMgr.Reload(), mkm.queryKeyMgr.Reload())
}
