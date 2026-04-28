package apikey

import (
	"bufio"
	"os"
	"sync"
	"time"
)

// KeyManager manages API keys from a dynamic key file
type KeyManager struct {
	keyFile    string
	keys       map[string]bool
	mu         sync.RWMutex
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
		if line == "" || line[0] == '#' {
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