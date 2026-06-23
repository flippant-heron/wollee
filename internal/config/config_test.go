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
  subnetBroadcast: 192.168.1.255
  defaultHeartbeatInterval: 45s
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

	if cfg.Server.DefaultHeartbeatInterval != 45*time.Second {
		t.Fatalf("unexpected heartbeat interval: %s", cfg.Server.DefaultHeartbeatInterval)
	}

	if len(cfg.Hosts) != 1 || cfg.Hosts[0].MAC != "00:11:22:33:44:55" {
		t.Fatalf("unexpected hosts: %+v", cfg.Hosts)
	}
}

func TestValidateServerRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Server: ServerConfig{
			Port:                     0,
			SubnetBroadcast:          "bad-ip",
			DefaultHeartbeatInterval: 0,
			TelegramToken:            "token",
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
  subnetBroadcast: 192.168.1.0/24
  defaultHeartbeatInterval: 30s
  activeTimeout: 5m
hosts:
  - hostname: desktop
    mac: 00:11:22:33:44:55
`)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	initialCfg := ServerConfig{
		Port:                     8080,
		SubnetBroadcast:          "192.168.1.0/24",
		DefaultHeartbeatInterval: 30 * time.Second,
		ActiveTimeout:            5 * time.Minute,
	}

	mgr := NewManager(path, initialCfg)

	// Verify initial values
	if got := mgr.Get(); got.SubnetBroadcast != "192.168.1.0/24" {
		t.Fatalf("initial SubnetBroadcast = %q, want 192.168.1.0/24", got.SubnetBroadcast)
	}

	// Update settings
	newCfg := ServerConfig{
		Port:                     8080,
		SubnetBroadcast:          "10.0.0.0/8",
		DefaultHeartbeatInterval: 60 * time.Second,
		ActiveTimeout:            10 * time.Minute,
		TelegramToken:            "test-token",
		AllowedTelegramUsers:     []int64{123},
	}

	if err := mgr.Update(newCfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify in-memory changes
	if got := mgr.Get(); got.SubnetBroadcast != "10.0.0.0/8" {
		t.Fatalf("after Update SubnetBroadcast = %q, want 10.0.0.0/8", got.SubnetBroadcast)
	}

	// Verify file was written with new values
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.SubnetBroadcast != "10.0.0.0/8" {
		t.Fatalf("file SubnetBroadcast = %q, want 10.0.0.0/8", cfg.Server.SubnetBroadcast)
	}

	if cfg.Server.DefaultHeartbeatInterval != 60*time.Second {
		t.Fatalf("file DefaultHeartbeatInterval = %v, want 60s", cfg.Server.DefaultHeartbeatInterval)
	}

	// Verify hosts section was preserved
	if len(cfg.Hosts) != 1 || cfg.Hosts[0].MAC != "00:11:22:33:44:55" {
		t.Fatalf("hosts not preserved: %+v", cfg.Hosts)
	}
}
