package config

import (
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Manager handles thread-safe access to server configuration with hot-reload capability.
type Manager struct {
	configPath string
	config     ServerConfig
	mu         sync.RWMutex
	lastReload time.Time
}

// NewManager creates a new config manager with the given initial configuration.
func NewManager(configPath string, initialConfig ServerConfig) *Manager {
	return &Manager{
		configPath: configPath,
		config:     initialConfig,
		lastReload: time.Now(),
	}
}

// Get returns a copy of the current server configuration.
func (m *Manager) Get() ServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// Reload reloads the configuration from disk and returns any error.
func (m *Manager) Reload() error {
	cfg, err := Load(m.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := cfg.ValidateServer(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	m.mu.Lock()
	m.config = cfg.Server
	m.lastReload = time.Now()
	m.mu.Unlock()

	return nil
}

// LastReload returns the time of the last successful reload.
func (m *Manager) LastReload() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastReload
}

// Update updates the configuration in memory and saves it to disk.
func (m *Manager) Update(newConfig ServerConfig) error {
	// Read entire config file to preserve hosts and other sections
	var config map[string]interface{}
	content, err := os.ReadFile(m.configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	}

	if len(content) > 0 {
		if err := yaml.Unmarshal(content, &config); err != nil {
			return fmt.Errorf("decode config: %w", err)
		}
	}

	if config == nil {
		config = make(map[string]interface{})
	}

	// Update server section
	serverConfig := map[string]interface{}{
		"port":                     newConfig.Port,
		"subnetBroadcast":          newConfig.SubnetBroadcast,
		"activeTimeout":            newConfig.ActiveTimeout.String(),
		"defaultHeartbeatInterval": newConfig.DefaultHeartbeatInterval.String(),
		"telegramToken":            newConfig.TelegramToken,
		"allowedTelegramUsers":     newConfig.AllowedTelegramUsers,
	}
	config["server"] = serverConfig

	// Marshal to YAML
	payload, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	// Write to temp file first, then rename
	tempPath := m.configPath + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		return fmt.Errorf("write config temp file: %w", err)
	}

	if err := os.Rename(tempPath, m.configPath); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}

	// Update in-memory config
	m.mu.Lock()
	m.config = newConfig
	m.lastReload = time.Now()
	m.mu.Unlock()

	return nil
}
