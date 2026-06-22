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
