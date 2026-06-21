package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAndValidateClientConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`server:
  port: 8080
  subnetBroadcast: 192.168.1.255
client:
  serverURL: http://127.0.0.1:8080
  macAddress: 00-11-22-33-44-55
  heartbeatInterval: 45s
`)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if err := cfg.ValidateClient(); err != nil {
		t.Fatalf("ValidateClient() error = %v", err)
	}

	if cfg.Client.MACAddress != "00:11:22:33:44:55" {
		t.Fatalf("unexpected normalized MAC: %s", cfg.Client.MACAddress)
	}

	if cfg.Client.HeartbeatInterval != 45*time.Second {
		t.Fatalf("unexpected heartbeat interval: %s", cfg.Client.HeartbeatInterval)
	}
}

func TestValidateServerRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Server: ServerConfig{
			Port:            0,
			SubnetBroadcast: "bad-ip",
			TelegramToken:   "token",
		},
	}

	if err := cfg.ValidateServer(); err == nil {
		t.Fatal("ValidateServer() error = nil, want error")
	}
}
