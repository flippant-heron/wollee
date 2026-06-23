package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAndValidateServerConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`server:
  port: 8080
  network: 192.168.1.255
  heartbeat: 45s
hosts:
  - hostname: desktop
    mac: 00-11-22-33-44-55
`)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if err := cfg.ValidateServer(); err != nil {
		t.Fatalf("ValidateServer() error = %v", err)
	}

	if cfg.Server.Heartbeat != 45*time.Second {
		t.Fatalf("unexpected heartbeat interval: %s", cfg.Server.Heartbeat)
	}

	if len(cfg.Hosts) != 1 || cfg.Hosts[0].MAC != "00:11:22:33:44:55" {
		t.Fatalf("unexpected hosts: %+v", cfg.Hosts)
	}
}

func TestValidateServerRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Server: ServerConfig{
			Port:      0,
			Network:   "bad-ip",
			Heartbeat: 0,
			Token:     "token",
		},
		Hosts: []HostConfig{{Hostname: "", MAC: "bad-mac"}},
	}

	if err := cfg.ValidateServer(); err == nil {
		t.Fatal("ValidateServer() error = nil, want error")
	}
}

func TestManagerUpdate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`server:
  port: 8080
  network: 192.168.1.0/24
  heartbeat: 30s
  timeout: 5m
  configRefresh: 5m
hosts:
  - hostname: desktop
    mac: 00:11:22:33:44:55
`)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	initialCfg := ServerConfig{
		Port:          8080,
		Network:       "192.168.1.0/24",
		Heartbeat:     30 * time.Second,
		Timeout:       5 * time.Minute,
		ConfigRefresh: 5 * time.Minute,
	}

	mgr := NewManager(path, initialCfg)

	// Verify initial values
	if got := mgr.Get(); got.Network != "192.168.1.0/24" {
		t.Fatalf("initial Network = %q, want 192.168.1.0/24", got.Network)
	}

	// Update settings
	newCfg := ServerConfig{
		Port:          8080,
		Network:       "10.0.0.0/8",
		Heartbeat:     60 * time.Second,
		Timeout:       10 * time.Minute,
		ConfigRefresh: 5 * time.Minute,
		Token:         "test-token",
		Users:         []int64{123},
	}

	if err := mgr.Update(newCfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify in-memory changes
	if got := mgr.Get(); got.Network != "10.0.0.0/8" {
		t.Fatalf("after Update Network = %q, want 10.0.0.0/8", got.Network)
	}

	// Verify file was written with new values
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Network != "10.0.0.0/8" {
		t.Fatalf("file Network = %q, want 10.0.0.0/8", cfg.Server.Network)
	}

	if cfg.Server.Heartbeat != 60*time.Second {
		t.Fatalf("file Heartbeat = %v, want 60s", cfg.Server.Heartbeat)
	}

	// Verify hosts section was preserved
	if len(cfg.Hosts) != 1 || cfg.Hosts[0].MAC != "00:11:22:33:44:55" {
		t.Fatalf("hosts not preserved: %+v", cfg.Hosts)
	}
}
