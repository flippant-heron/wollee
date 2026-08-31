package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

func TestSettingsPageFragmentShowsCurrentConfig(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.Header.Set("HX-Request", "true")
	resp := httptest.NewRecorder()

	app.handleSettingsPage(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte("192.168.1.0/24")) {
		t.Fatal("response missing configured network")
	}
}

func TestUpdateSettingsFormPreventsTelegramTokenOverwrite(t *testing.T) {
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
	form := url.Values{
		"network":       {"192.168.2.0/24"},
		"heartbeat":     {"30s"},
		"timeout":       {"5m"},
		"configRefresh": {"5m"},
		"telegramToken": {"new-token"},
	}
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	app.handleUpdateSettingsForm(resp, req)

	// Should be forbidden
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusForbidden)
	}

	// Verify token didn't change
	if mgr := cfgMgr.Get(); mgr.Token != "existing-token" {
		t.Fatalf("token changed to %q, want existing-token", mgr.Token)
	}
}

func TestUpdateSettingsFormAllowsTokenOnlyOnce(t *testing.T) {
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
	form := url.Values{
		"network":       {"192.168.1.0/24"},
		"heartbeat":     {"30s"},
		"timeout":       {"5m"},
		"configRefresh": {"5m"},
		"telegramToken": {"new-token"},
	}
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	app.handleUpdateSettingsForm(resp, req)

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

func TestHandleRegisterUpsertsHostAndReturnsHeartbeatInterval(t *testing.T) {
	t.Parallel()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}

	cfgMgr := config.NewManager("", config.ServerConfig{Heartbeat: 45 * time.Second})
	app := &App{
		cfgMgr:   cfgMgr,
		registry: registry,
		logger:   appservice.NewLogger(true),
	}

	body, _ := json.Marshal(registerRequest{
		MAC:      "00:11:22:33:44:55",
		Hostname: "desk",
		IP:       "192.168.1.10",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	app.handleRegister(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload registerResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.HeartbeatInterval != "45s" {
		t.Fatalf("HeartbeatInterval = %q, want 45s", payload.HeartbeatInterval)
	}

	host, ok := registry.FindByMAC("00:11:22:33:44:55")
	if !ok {
		t.Fatal("host was not persisted to the registry")
	}
	if host.Hostname != "desk" || host.IP != "192.168.1.10" {
		t.Fatalf("unexpected host record: %+v", host)
	}
}

func TestHandleRegisterPreservesDisabledFlag(t *testing.T) {
	t.Parallel()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}
	if err := registry.Upsert(HostRecord{
		MAC:      "00:11:22:33:44:55",
		Hostname: "desk",
		IP:       "192.168.1.10",
		Disabled: true,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	cfgMgr := config.NewManager("", config.ServerConfig{Heartbeat: 30 * time.Second})
	app := &App{
		cfgMgr:   cfgMgr,
		registry: registry,
		logger:   appservice.NewLogger(true),
	}

	body, _ := json.Marshal(registerRequest{
		MAC:      "00:11:22:33:44:55",
		Hostname: "desk",
		IP:       "192.168.1.20",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	app.handleRegister(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", resp.Code, http.StatusOK, resp.Body.String())
	}

	host, ok := registry.FindByMAC("00:11:22:33:44:55")
	if !ok {
		t.Fatal("host missing after heartbeat")
	}
	if !host.Disabled {
		t.Fatal("heartbeat should not clear the disabled flag")
	}
	if host.IP != "192.168.1.20" {
		t.Fatalf("IP = %q, want updated 192.168.1.20", host.IP)
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()

	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}

	cfg := config.ServerConfig{
		Port:      8080,
		Network:   "192.168.1.0/24",
		Heartbeat: 30 * time.Second,
		Timeout:   5 * time.Minute,
	}
	cfgMgr := config.NewManager("", cfg)

	return &App{
		cfgMgr:   cfgMgr,
		registry: registry,
		logger:   appservice.NewLogger(true),
	}
}

// isFullHTMLPage reports whether body looks like a complete HTML document
// (as opposed to an HTMX fragment meant to be swapped into an existing page).
func isFullHTMLPage(body string) bool {
	return bytes.Contains([]byte(body), []byte("<!doctype html>")) && bytes.Contains([]byte(body), []byte("<nav"))
}

func TestHandleIndexFullPageVsFragment(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	// Full page load (no HX-Request header) must return the complete document.
	fullReq := httptest.NewRequest(http.MethodGet, "/", nil)
	fullResp := httptest.NewRecorder()
	app.handleIndex(fullResp, fullReq)

	if fullResp.Code != http.StatusOK {
		t.Fatalf("full page status = %d, want %d", fullResp.Code, http.StatusOK)
	}
	if ct := fullResp.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("full page Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	if !isFullHTMLPage(fullResp.Body.String()) {
		t.Fatalf("full page response does not look like a full HTML document: %s", fullResp.Body.String())
	}

	// HTMX fragment request must return only the inner article content.
	fragReq := httptest.NewRequest(http.MethodGet, "/", nil)
	fragReq.Header.Set("HX-Request", "true")
	fragResp := httptest.NewRecorder()
	app.handleIndex(fragResp, fragReq)

	if fragResp.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want %d", fragResp.Code, http.StatusOK)
	}
	if isFullHTMLPage(fragResp.Body.String()) {
		t.Fatalf("fragment response should not contain full page shell: %s", fragResp.Body.String())
	}
	if !bytes.Contains(fragResp.Body.Bytes(), []byte("status-table")) {
		t.Fatal("fragment response missing status table content")
	}
}

func TestHandleAddHostPageFullPageVsFragment(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	fullReq := httptest.NewRequest(http.MethodGet, "/add-host", nil)
	fullResp := httptest.NewRecorder()
	app.handleAddHostPage(fullResp, fullReq)

	if fullResp.Code != http.StatusOK {
		t.Fatalf("full page status = %d, want %d", fullResp.Code, http.StatusOK)
	}
	if !isFullHTMLPage(fullResp.Body.String()) {
		t.Fatalf("full page response does not look like a full HTML document: %s", fullResp.Body.String())
	}

	fragReq := httptest.NewRequest(http.MethodGet, "/add-host", nil)
	fragReq.Header.Set("HX-Request", "true")
	fragResp := httptest.NewRecorder()
	app.handleAddHostPage(fragResp, fragReq)

	if fragResp.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want %d", fragResp.Code, http.StatusOK)
	}
	if isFullHTMLPage(fragResp.Body.String()) {
		t.Fatalf("fragment response should not contain full page shell: %s", fragResp.Body.String())
	}
	if !bytes.Contains(fragResp.Body.Bytes(), []byte("Add new host")) {
		t.Fatal("fragment response missing add-host form heading")
	}
}

func TestHandleSettingsPageFullPageVsFragment(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	fullReq := httptest.NewRequest(http.MethodGet, "/settings", nil)
	fullResp := httptest.NewRecorder()
	app.handleSettingsPage(fullResp, fullReq)

	if fullResp.Code != http.StatusOK {
		t.Fatalf("full page status = %d, want %d", fullResp.Code, http.StatusOK)
	}
	if !isFullHTMLPage(fullResp.Body.String()) {
		t.Fatalf("full page response does not look like a full HTML document: %s", fullResp.Body.String())
	}

	fragReq := httptest.NewRequest(http.MethodGet, "/settings", nil)
	fragReq.Header.Set("HX-Request", "true")
	fragResp := httptest.NewRecorder()
	app.handleSettingsPage(fragResp, fragReq)

	if fragResp.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want %d", fragResp.Code, http.StatusOK)
	}
	if isFullHTMLPage(fragResp.Body.String()) {
		t.Fatalf("fragment response should not contain full page shell: %s", fragResp.Body.String())
	}
	if !bytes.Contains(fragResp.Body.Bytes(), []byte("Save Settings")) {
		t.Fatal("fragment response missing settings form")
	}
}

func TestHandleWakeErrorSetsHTMLContentType(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/wake", strings.NewReader("mac=&hostname="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	app.handleWake(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
	// Content-Type must be set even though WriteHeader happens as part of the
	// same call (regression test: Content-Type set after WriteHeader is a no-op).
	if ct := resp.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
}

func TestUnauthenticatedRequestServesLoginPage(t *testing.T) {
	t.Parallel()

	cfgMgr := config.NewManager("", config.ServerConfig{Port: 8080, Heartbeat: 30 * time.Second})
	registry, err := OpenRegistry(filepath.Join(t.TempDir(), "hosts.yaml"))
	if err != nil {
		t.Fatalf("OpenRegistry() error = %v", err)
	}

	app, err := New(cfgMgr, registry, appservice.NewLogger(true), config.LogoConfig{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	router := app.newRouter()

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}
	if ct := resp.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte("loginApp")) {
		t.Fatal("response does not look like the login page")
	}
}
