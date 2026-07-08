package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flippant-heron/wollee/internal/auth"
)

// TestPasswordHashPersistsAcrossReload verifies that password hashes are saved to disk
// and loaded correctly when the config is reloaded
func TestPasswordHashPersistsAcrossReload(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Create initial config with basic settings
	initialCfg := ServerConfig{
		Port:          8080,
		Network:       "192.168.1.0/24",
		Heartbeat:     30 * time.Second,
		Timeout:       5 * time.Minute,
		ConfigRefresh: 5 * time.Minute,
		PasswordHash:  "",
		JWTSecret:     "initial-secret",
	}

	// Create manager and update with password hash
	mgr := NewManager(configPath, initialCfg)

	// Hash a password
	hasher := &auth.Hasher{}
	hash, err := hasher.HashPassword("test-password-123")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	// Update config with password hash
	updatedCfg := mgr.Get()
	updatedCfg.PasswordHash = hash
	if err := mgr.Update(updatedCfg); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify hash is in the config
	currentCfg := mgr.Get()
	if currentCfg.PasswordHash != hash {
		t.Errorf("PasswordHash not set in memory: got %q, want %q", currentCfg.PasswordHash, hash)
	}

	// Verify the file was written
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file was not created: %v", err)
	}

	// Create a new manager and reload from disk
	newMgr := NewManager(configPath, initialCfg)
	if err := newMgr.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// Verify password hash persisted
	reloadedCfg := newMgr.Get()
	if reloadedCfg.PasswordHash == "" {
		t.Error("PasswordHash not loaded from disk")
	}

	if reloadedCfg.PasswordHash != hash {
		t.Errorf("PasswordHash mismatch: got %q, want %q", reloadedCfg.PasswordHash, hash)
	}

	// Verify the hash can be verified
	if err := hasher.VerifyPassword(reloadedCfg.PasswordHash, "test-password-123"); err != nil {
		t.Errorf("VerifyPassword failed: %v", err)
	}
}

// TestJWTSecretPersistsAcrossReload verifies JWT secrets are persisted to disk
func TestJWTSecretPersistsAcrossReload(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	initialCfg := ServerConfig{
		Port:          8080,
		Network:       "192.168.1.0/24",
		Heartbeat:     30 * time.Second,
		Timeout:       5 * time.Minute,
		ConfigRefresh: 5 * time.Minute,
		JWTSecret:     "test-jwt-secret-key",
	}

	mgr := NewManager(configPath, initialCfg)

	// Update config (this should save to disk)
	cfg := mgr.Get()
	if err := mgr.Update(cfg); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Create new manager and reload
	newMgr := NewManager(configPath, initialCfg)
	if err := newMgr.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	reloadedCfg := newMgr.Get()
	if reloadedCfg.JWTSecret != "test-jwt-secret-key" {
		t.Errorf("JWTSecret mismatch: got %q, want %q", reloadedCfg.JWTSecret, "test-jwt-secret-key")
	}
}
