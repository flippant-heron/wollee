package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/flippant-heron/wollee/internal/config"
	appservice "github.com/flippant-heron/wollee/internal/service"
)

func TestHandleRegisterReturnsHeartbeatInterval(t *testing.T) {
	t.Parallel()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}

	app := &App{
		cfg: config.ServerConfig{
			Port:                     8080,
			SubnetBroadcast:          "192.168.1.255",
			DefaultHeartbeatInterval: 20 * time.Second,
		},
		registry: registry,
		logger:   appservice.NewLogger(true),
	}

	body := []byte(`{"mac":"00:11:22:33:44:55","hostname":"desk","ip":"192.168.1.10"}`)
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	app.handleRegister(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var payload hostStatus
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.HeartbeatInterval != "20s" {
		t.Fatalf("heartbeatInterval = %q, want 20s", payload.HeartbeatInterval)
	}
}

func TestTelegramListIncludesHosts(t *testing.T) {
	t.Parallel()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}
	if err := registry.Upsert(HostRecord{MAC: "00:11:22:33:44:55", Hostname: "desk", IP: "192.168.1.10", LastSeen: time.Now()}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	app := &App{
		cfg:      config.ServerConfig{DefaultHeartbeatInterval: 30 * time.Second},
		registry: registry,
		logger:   appservice.NewLogger(true),
	}

	response := app.List()
	if response == "No registered hosts." || !bytes.Contains([]byte(response), []byte("desk")) {
		t.Fatalf("unexpected list response: %s", response)
	}
}
