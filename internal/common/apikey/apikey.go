package apikey

import (
	"bufio"
	"os"
	"sync"
	"time"
)

// KeyManager manages API keys from a dynamic key file
type KeyManager struct {
	keyFile     string
	keys        map[string]bool
	mu          sync.RWMutex
	lastModTime time.Time
}

// NewKeyManager creates a new key manager
func NewKeyManager(keyFile string) *KeyManager {
	return &KeyManager{
		keyFile: keyFile,
		keys:    make(map[string]bool),
	}
}

// Start starts the key file watcher
// It periodically checks the key file for updates
func (km *KeyManager) Start(interval time.Duration) {
	// Load keys immediately
	km.loadKeys()

	// Start periodic refresh
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			km.loadKeys()
		}
	}()
}

// loadKeys loads keys from the key file
// It checks file modification time to avoid unnecessary reloads
func (km *KeyManager) loadKeys() {
	info, err := os.Stat(km.keyFile)
	if err != nil {
		// File doesn't exist or inaccessible, keep existing keys
		return
	}

	// Check if file was modified since last load
	km.mu.RLock()
	lastMod := km.lastModTime
	km.mu.RUnlock()

	if !info.ModTime().After(lastMod) {
		// File not modified, skip reload
		return
	}

	// Read file
	file, err := os.Open(km.keyFile)
	if err != nil {
		return
	}
	defer file.Close()

	newKeys := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip empty lines and comments
		if line == "" || (len(line) > 0 && line[0] == '#') {
			continue
		}
		newKeys[line] = true
	}

	if err := scanner.Err(); err != nil {
		return
	}

	km.mu.Lock()
	km.keys = newKeys
	km.lastModTime = info.ModTime()
	km.mu.Unlock()
}

// Validate checks if a key is valid
func (km *KeyManager) Validate(key string) bool {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.keys[key]
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
	km.loadKeys()
	return nil
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

// ReloadAll forces reload of all key files
func (mkm *MultiKeyManager) ReloadAll() {
	mkm.agentKeyMgr.Reload()
	mkm.queryKeyMgr.Reload()
}