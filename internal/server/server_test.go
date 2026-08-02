package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flippant-heron/wollee/internal/config"
	appservice "github.com/flippant-heron/wollee/internal/service"
)

func TestTelegramListIncludesHosts(t *testing.T) {
	t.Parallel()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}
	if err := registry.Upsert(HostRecord{MAC: "00:11:22:33:44:55", Hostname: "desk", IP: "192.168.1.10", LastSeen: time.Now()}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	cfg := config.ServerConfig{Heartbeat: 30 * time.Second}
	cfgMgr := config.NewManager("", cfg)

	app := &App{
		cfgMgr:   cfgMgr,
		registry: registry,
		logger:   appservice.NewLogger(true),
	}

	response := app.List()
	if response == "No registered hosts." || !bytes.Contains([]byte(response), []byte("desk")) {
		t.Fatalf("unexpected list response: %s", response)
	}
}

func TestGetSettingsReturnsCurrentConfig(t *testing.T) {
	t.Parallel()

	cfg := config.ServerConfig{
		Port:          8080,
		Network:       "192.168.1.0/24",
		Heartbeat:     30 * time.Second,
		Timeout:       5 * time.Minute,
		ConfigRefresh: 5 * time.Minute,
	}
	cfgMgr := config.NewManager("", cfg)

	app := &App{
		cfgMgr:   cfgMgr,
		registry: nil,
		logger:   appservice.NewLogger(true),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	resp := httptest.NewRecorder()

	app.getSettings(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var payload settingsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Network != "192.168.1.0/24" {
		t.Fatalf("Network = %q, want 192.168.1.0/24", payload.Network)
	}
	if payload.TokenSet {
		t.Fatal("TokenSet = true, want false")
	}
}

func TestUpdateSettingsPreventsTelegramTokenOverwrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write initial config with token
	initialContent := []byte(`server:
  port: 8080
  network: 192.168.1.0/24
  heartbeat: 30s
  timeout: 5m
  configRefresh: 5m
  token: existing-token
  users:
    - 123
hosts: []
`)
	if err := os.WriteFile(configPath, initialContent, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfgMgr := config.NewManager(configPath, cfg.Server)

	registry, err := OpenRegistry(filepath.Join(dir, "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}

	app := &App{
		cfgMgr:   cfgMgr,
		registry: registry,
		logger:   appservice.NewLogger(true),
	}

	// Try to update token
	updatePayload := serverSettingsRequest{
		Network:       "192.168.2.0/24",
		Heartbeat:     "30s",
		Timeout:       "5m",
		ConfigRefresh: "5m",
		Token:         "new-token",
		Users:         []int64{456},
	}
	body, _ := json.Marshal(settingsUpdateRequest{Settings: updatePayload})
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	app.updateSettings(resp, req)

	// Should be forbidden
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusForbidden)
	}

	// Verify token didn't change
	if mgr := cfgMgr.Get(); mgr.Token != "existing-token" {
		t.Fatalf("token changed to %q, want existing-token", mgr.Token)
	}
}

func TestUpdateSettingsAllowsTokenOnlyOnce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write initial config without token
	initialContent := []byte(`server:
  port: 8080
  network: 192.168.1.0/24
  heartbeat: 30s
  timeout: 5m
  configRefresh: 5m
hosts: []
`)
	if err := os.WriteFile(configPath, initialContent, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfgMgr := config.NewManager(configPath, cfg.Server)

	registry, err := OpenRegistry(filepath.Join(dir, "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}

	app := &App{
		cfgMgr:   cfgMgr,
		registry: registry,
		logger:   appservice.NewLogger(true),
	}

	// Set token for the first time
	updatePayload := serverSettingsRequest{
		Network:       "192.168.1.0/24",
		Heartbeat:     "30s",
		Timeout:       "5m",
		ConfigRefresh: "5m",
		Token:         "new-token",
		Users:         []int64{123},
	}
	body, _ := json.Marshal(settingsUpdateRequest{Settings: updatePayload})
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	app.updateSettings(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("first token set status = %d, want %d", resp.Code, http.StatusOK)
	}

	// Verify token was set
	if mgr := cfgMgr.Get(); mgr.Token != "new-token" {
		t.Fatalf("token not set: %q", mgr.Token)
	}
}

func TestStatusTableReturnsHTMLFragment(t *testing.T) {
	t.Parallel()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}

	// Add test hosts
	if err := registry.Upsert(HostRecord{
		MAC:      "00:11:22:33:44:55",
		Hostname: "desk",
		IP:       "192.168.1.10",
		LastSeen: time.Now().Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("Upsert desk: %v", err)
	}

	if err := registry.Upsert(HostRecord{
		MAC:      "aa:bb:cc:dd:ee:ff",
		Hostname: "server",
		IP:       "192.168.1.20",
		LastSeen: time.Now().Add(-10 * time.Minute),
		Disabled: true,
	}); err != nil {
		t.Fatalf("Upsert server: %v", err)
	}

	cfg := config.ServerConfig{
		Timeout: 5 * time.Minute,
	}
	cfgMgr := config.NewManager("", cfg)

	app := &App{
		cfgMgr:   cfgMgr,
		registry: registry,
		logger:   appservice.NewLogger(true),
	}

	req := httptest.NewRequest(http.MethodGet, "/status/table", nil)
	resp := httptest.NewRecorder()

	app.handleStatusTable(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	contentType := resp.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", contentType)
	}

	body := resp.Body.String()

	// Verify HTML structure and host data
	if !bytes.Contains([]byte(body), []byte("<tr")) {
		t.Fatal("response missing <tr> elements")
	}
	if !bytes.Contains([]byte(body), []byte("desk")) {
		t.Fatal("response missing hostname 'desk'")
	}
	if !bytes.Contains([]byte(body), []byte("00:11:22:33:44:55")) {
		t.Fatal("response missing MAC '00:11:22:33:44:55'")
	}
	if !bytes.Contains([]byte(body), []byte("server")) {
		t.Fatal("response missing hostname 'server'")
	}
	if !bytes.Contains([]byte(body), []byte("disabled")) {
		t.Fatal("response missing 'disabled' class for disabled host")
	}
}
