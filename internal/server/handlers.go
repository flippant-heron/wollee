package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/flippant-heron/wollee/internal/auth"
	"github.com/flippant-heron/wollee/internal/server/templates"
	"github.com/flippant-heron/wollee/internal/telegram"
	internalwol "github.com/flippant-heron/wollee/internal/wol"
)

var errHostNotFound = errors.New("host not found")

// renderHTML renders a templ component to the response writer
func renderHTML(w http.ResponseWriter, component templ.Component) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return component.Render(context.Background(), w)
}

// renderHTMLStatus sets the Content-Type header before writing the status
// code, then renders the component. Content-Type must be set before
// WriteHeader or it is silently dropped from the response.
func renderHTMLStatus(w http.ResponseWriter, status int, component templ.Component) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	return component.Render(context.Background(), w)
}

// getHostStatuses converts registry hosts to template HostStatus objects
func (a *App) getHostStatuses() []templates.HostStatus {
	cfg := a.cfgMgr.Get()
	records := a.registry.List()
	hosts := make([]templates.HostStatus, 0, len(records))
	cutoff := time.Now().Add(-cfg.Timeout)
	for _, host := range records {
		hosts = append(hosts, templates.HostStatus{
			MAC:      host.MAC,
			Hostname: host.Hostname,
			IP:       host.IP,
			LastSeen: host.LastSeen,
			Disabled: host.Disabled,
			Active:   host.LastSeen.After(cutoff),
		})
	}
	return hosts
}

// handleFavicon serves the configured logo as the site favicon. There is no
// separate favicon asset; browsers accept arbitrary raster image formats and
// sizes for rel="icon", so the logo bytes are reused as-is.
func (a *App) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if len(a.faviconData) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.faviconContentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(a.faviconData)
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// If this is an HTMX request, return just the article content
	if r.Header.Get("HX-Request") == "true" {
		renderHTML(w, templates.HomePage())
		return
	}

	// Otherwise, return the full page
	renderHTML(w, templates.IndexPage(a.logoDataURI))
}

func (a *App) handleAddHostPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		renderHTML(w, templates.AddHostPage())
		return
	}

	renderHTML(w, templates.AddHostFullPage(a.logoDataURI))
}

func (a *App) handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := r.ParseForm(); err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("invalid form data"))
		return
	}

	mac := strings.TrimSpace(r.FormValue("mac"))
	hostname := strings.TrimSpace(r.FormValue("hostname"))

	if mac == "" && hostname == "" {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("hostname or mac is required"))
		return
	}

	req := wakeRequest{MAC: mac, Hostname: hostname}
	host, err := a.resolveWakeTarget(req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errHostNotFound) {
			status = http.StatusNotFound
		}
		renderHTMLStatus(w, status, templates.ErrorRow(err.Error()))
		return
	}

	if host.Disabled {
		renderHTMLStatus(w, http.StatusForbidden, templates.ErrorRow("Host is disabled"))
		return
	}

	cfg := a.cfgMgr.Get()
	if err := internalwol.SendMagicPacket(host.MAC, cfg.Network); err != nil {
		a.logger.Error("send magic packet", err, "mac", host.MAC, "hostname", host.Hostname, "broadcast", cfg.Network)
		renderHTMLStatus(w, http.StatusBadGateway, templates.ErrorRow("Failed to send magic packet"))
		return
	}

	a.logger.Info("sent magic packet", "mac", host.MAC, "hostname", host.Hostname, "broadcast", cfg.Network)

	hosts := a.getHostStatuses()
	renderHTML(w, templates.StatusTableRows(hosts))
}

func (a *App) resolveWakeTarget(req wakeRequest) (HostRecord, error) {
	if req.MAC != "" {
		mac, err := internalwol.NormalizeMAC(req.MAC)
		if err != nil {
			return HostRecord{}, errors.New("invalid MAC address")
		}
		host, ok := a.registry.FindByMAC(mac)
		if !ok {
			return HostRecord{}, errHostNotFound
		}
		return host, nil
	}

	hostname := strings.TrimSpace(req.Hostname)
	if hostname == "" {
		return HostRecord{}, errors.New("hostname or mac is required")
	}

	host, ok := a.registry.FindByHostname(hostname)
	if !ok {
		return HostRecord{}, errHostNotFound
	}
	return host, nil
}

func (a *App) decodeJSON(r *http.Request, target any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	return nil
}

func (a *App) writeError(w http.ResponseWriter, status int, message string) {
	a.writeJSON(w, status, map[string]string{"error": message})
}

func (a *App) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		a.logger.Error("encode JSON response", err, "status", status)
	}
}

func (a *App) List() string {
	cfg := a.cfgMgr.Get()
	hosts := a.registry.List()
	if len(hosts) == 0 {
		return "No registered hosts."
	}

	cutoff := time.Now().Add(-cfg.Timeout)
	var builder strings.Builder
	builder.WriteString("Registered hosts:\n")
	for _, host := range hosts {
		status := "offline"
		if host.LastSeen.After(cutoff) {
			status = "online"
		}
		_, _ = fmt.Fprintf(&builder, "- %s (%s) %s [%s]\n", host.Hostname, host.MAC, host.IP, status)
	}
	return builder.String()
}

func (a *App) Wake(target string) string {
	host, err := a.resolveWakeTarget(wakeRequest{Hostname: target})
	if err != nil && errors.Is(err, errHostNotFound) {
		host, err = a.resolveWakeTarget(wakeRequest{MAC: target})
	}
	if err != nil {
		return err.Error()
	}
	cfg := a.cfgMgr.Get()
	if err := internalwol.SendMagicPacket(host.MAC, cfg.Network); err != nil {
		a.logger.Error("send magic packet from telegram", err, "mac", host.MAC, "hostname", host.Hostname)
		return "Failed to send Wake-on-LAN packet."
	}
	return fmt.Sprintf("Sent wake signal to %s (%s).", host.Hostname, host.MAC)
}

func (a *App) handleAddHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := r.ParseForm(); err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.AddHostError("invalid form data"))
		return
	}

	macStr := strings.TrimSpace(r.FormValue("mac"))
	hostname := strings.TrimSpace(r.FormValue("hostname"))
	ipStr := strings.TrimSpace(r.FormValue("ip"))

	mac, err := internalwol.NormalizeMAC(macStr)
	if err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.AddHostError("invalid MAC address"))
		return
	}

	if hostname == "" {
		hostname = mac
	}

	ip := net.ParseIP(ipStr)
	if ip == nil || ip.To4() == nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.AddHostError("invalid IPv4 address"))
		return
	}

	host := HostRecord{
		MAC:      mac,
		Hostname: hostname,
		IP:       ip.String(),
		LastSeen: time.Unix(0, 0),
	}
	if err := a.registry.Upsert(host); err != nil {
		a.logger.Error("persist host addition", err, "mac", mac)
		renderHTMLStatus(w, http.StatusInternalServerError, templates.AddHostError("failed to store host"))
		return
	}

	a.logger.Info("added host", "mac", host.MAC, "hostname", host.Hostname, "ip", host.IP)

	renderHTMLStatus(w, http.StatusOK, templates.AddHostSuccess(hostname))
}

func (a *App) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	mac := strings.TrimSpace(r.PathValue("mac"))
	if mac == "" {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("MAC address is required"))
		return
	}

	normalized, err := internalwol.NormalizeMAC(mac)
	if err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("Invalid MAC address"))
		return
	}

	if _, ok := a.registry.FindByMAC(normalized); !ok {
		renderHTMLStatus(w, http.StatusNotFound, templates.ErrorRow("Host not found"))
		return
	}

	if err := a.registry.Delete(normalized); err != nil {
		a.logger.Error("delete host", err, "mac", normalized)
		renderHTMLStatus(w, http.StatusInternalServerError, templates.ErrorRow("Failed to delete host"))
		return
	}

	a.logger.Info("deleted host", "mac", normalized)

	hosts := a.getHostStatuses()
	renderHTML(w, templates.StatusTableRows(hosts))
}

func (a *App) handleDisableHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	mac := strings.TrimSpace(r.PathValue("mac"))
	if mac == "" {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("MAC address is required"))
		return
	}

	normalized, err := internalwol.NormalizeMAC(mac)
	if err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("Invalid MAC address"))
		return
	}

	host, ok := a.registry.FindByMAC(normalized)
	if !ok {
		renderHTMLStatus(w, http.StatusNotFound, templates.ErrorRow("Host not found"))
		return
	}

	if host.Disabled {
		hosts := a.getHostStatuses()
		renderHTML(w, templates.StatusTableRows(hosts))
		return
	}

	host.Disabled = true
	if err := a.registry.Upsert(host); err != nil {
		a.logger.Error("disable host", err, "mac", normalized)
		renderHTMLStatus(w, http.StatusInternalServerError, templates.ErrorRow("Failed to disable host"))
		return
	}

	a.logger.Info("disabled host", "mac", normalized)

	hosts := a.getHostStatuses()
	renderHTML(w, templates.StatusTableRows(hosts))
}

func (a *App) handleEnableHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	mac := strings.TrimSpace(r.PathValue("mac"))
	if mac == "" {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("MAC address is required"))
		return
	}

	normalized, err := internalwol.NormalizeMAC(mac)
	if err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("Invalid MAC address"))
		return
	}

	host, ok := a.registry.FindByMAC(normalized)
	if !ok {
		renderHTMLStatus(w, http.StatusNotFound, templates.ErrorRow("Host not found"))
		return
	}

	if !host.Disabled {
		hosts := a.getHostStatuses()
		renderHTML(w, templates.StatusTableRows(hosts))
		return
	}

	host.Disabled = false
	if err := a.registry.Upsert(host); err != nil {
		a.logger.Error("enable host", err, "mac", normalized)
		renderHTMLStatus(w, http.StatusInternalServerError, templates.ErrorRow("Failed to enable host"))
		return
	}

	a.logger.Info("enabled host", "mac", normalized)

	hosts := a.getHostStatuses()
	renderHTML(w, templates.StatusTableRows(hosts))
}

func (a *App) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := a.reloadConfig(); err != nil {
		a.logger.Error("reload config", err)
		a.writeError(w, http.StatusInternalServerError, "failed to reload config: "+err.Error())
		return
	}

	a.logger.Info("config reloaded successfully")
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "config reloaded successfully",
		"lastReload": a.cfgMgr.LastReload(),
	})
}

func (a *App) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.cfgMgr.Get()
	if r.Header.Get("HX-Request") == "true" {
		renderHTML(w, templates.SettingsPage(&cfg))
		return
	}

	renderHTML(w, templates.SettingsFullPage(&cfg, a.logoDataURI))
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getSettings(w, r)
	case http.MethodPost:
		a.updateSettings(w, r)
	default:
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleUpdateSettingsForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := r.ParseForm(); err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.ErrorRow("invalid form data"))
		return
	}

	// Validate and update settings
	cfg := a.cfgMgr.Get()

	// Save original config values before modification
	oldToken := cfg.Token
	oldUsers := make([]int64, len(cfg.Users))
	copy(oldUsers, cfg.Users)
	oldWhoami := cfg.Whoami

	network := strings.TrimSpace(r.FormValue("network"))
	if err := internalwol.ValidateBroadcast(network); err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.SettingsError("invalid network: "+err.Error(), &cfg))
		return
	}

	timeoutStr := strings.TrimSpace(r.FormValue("timeout"))
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.SettingsError("invalid timeout: "+err.Error(), &cfg))
		return
	}
	if timeout <= 0 {
		renderHTMLStatus(w, http.StatusBadRequest, templates.SettingsError("timeout must be greater than 0", &cfg))
		return
	}

	heartbeatStr := strings.TrimSpace(r.FormValue("heartbeat"))
	heartbeat, err := time.ParseDuration(heartbeatStr)
	if err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.SettingsError("invalid heartbeat: "+err.Error(), &cfg))
		return
	}
	if heartbeat <= 0 {
		renderHTMLStatus(w, http.StatusBadRequest, templates.SettingsError("heartbeat must be greater than 0", &cfg))
		return
	}

	cfgRefreshStr := strings.TrimSpace(r.FormValue("configRefresh"))
	cfgRefresh, err := time.ParseDuration(cfgRefreshStr)
	if err != nil {
		renderHTMLStatus(w, http.StatusBadRequest, templates.SettingsError("invalid configRefresh: "+err.Error(), &cfg))
		return
	}
	if cfgRefresh <= 0 {
		renderHTMLStatus(w, http.StatusBadRequest, templates.SettingsError("configRefresh must be greater than 0", &cfg))
		return
	}

	// Handle token - only allow setting it once
	newToken := strings.TrimSpace(r.FormValue("telegramToken"))
	if cfg.Token != "" && newToken != "" {
		// Token is already set and user is trying to change it - not allowed
		renderHTMLStatus(w, http.StatusForbidden, templates.SettingsError("token is already configured and cannot be changed", &cfg))
		return
	}

	// Update config (port remains unchanged - requires server restart)
	cfg.Network = network
	cfg.Timeout = timeout
	cfg.Heartbeat = heartbeat
	cfg.ConfigRefresh = cfgRefresh
	cfg.Whoami = r.FormValue("whoami") == "on"
	if newToken != "" {
		cfg.Token = newToken
	}

	if err := a.cfgMgr.Update(cfg); err != nil {
		a.logger.Error("update config", err)
		renderHTMLStatus(w, http.StatusInternalServerError, templates.SettingsError("failed to update config: "+err.Error(), &cfg))
		return
	}

	// Immediately reload config from disk to verify persistence and ensure consistency
	if err := a.cfgMgr.Reload(); err != nil {
		a.logger.Error("reload config after update", err)
		renderHTMLStatus(w, http.StatusInternalServerError, templates.SettingsError("settings saved but verification failed: "+err.Error(), &cfg))
		return
	}

	// Get the freshly loaded config to verify all changes were persisted
	verifiedCfg := a.cfgMgr.Get()

	// Restart telegram service if any relevant settings changed
	usersChanged := !eqInt64Slices(oldUsers, verifiedCfg.Users)
	tokenChanged := oldToken != verifiedCfg.Token
	whoamiChanged := oldWhoami != verifiedCfg.Whoami

	if tokenChanged || usersChanged || whoamiChanged {
		if verifiedCfg.Token != "" {
			// Token is set - restart with new config
			if a.telegramCancel != nil {
				a.telegramCancel()
			}
			if a.telegram != nil {
				a.telegram.Shutdown()
			}
			a.telegram = telegram.New(verifiedCfg.Token, verifiedCfg.Users, a, a.logger, verifiedCfg.Whoami)
			a.telegramCtx, a.telegramCancel = context.WithCancel(context.Background())
			if err := a.telegram.Start(a.telegramCtx); err != nil {
				a.logger.Error("start telegram service after settings change", err)
			}
		} else {
			// Token was cleared - stop telegram service
			if a.telegramCancel != nil {
				a.telegramCancel()
			}
			if a.telegram != nil {
				a.telegram.Shutdown()
			}
		}
	}

	a.logger.Info("settings updated and verified successfully",
		"tokenChanged", tokenChanged,
		"usersChanged", usersChanged,
		"whoamiChanged", whoamiChanged,
		"network", verifiedCfg.Network,
		"heartbeat", verifiedCfg.Heartbeat,
		"timeout", verifiedCfg.Timeout,
		"configRefresh", verifiedCfg.ConfigRefresh,
		"whoami", verifiedCfg.Whoami,
		"usersCount", len(verifiedCfg.Users),
		"tokenSet", verifiedCfg.Token != "",
	)

	renderHTMLStatus(w, http.StatusOK, templates.SettingsUpdated())
}

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	cfg := a.cfgMgr.Get()
	response := settingsResponse{
		Network:       cfg.Network,
		Heartbeat:     cfg.Heartbeat.String(),
		Timeout:       cfg.Timeout.String(),
		ConfigRefresh: cfg.ConfigRefresh.String(),
		TokenSet:      cfg.Token != "",
		Users:         cfg.Users,
		Whoami:        cfg.Whoami,
	}
	a.writeJSON(w, http.StatusOK, response)
}

func (a *App) updateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsUpdateRequest
	if err := a.decodeJSON(r, &req); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate and update settings
	cfg := a.cfgMgr.Get()

	// Save original config values before modification
	oldToken := cfg.Token
	oldUsers := make([]int64, len(cfg.Users))
	copy(oldUsers, cfg.Users)
	oldWhoami := cfg.Whoami

	if err := internalwol.ValidateBroadcast(req.Settings.Network); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid network: "+err.Error())
		return
	}

	timeout, err := time.ParseDuration(req.Settings.Timeout)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid timeout: "+err.Error())
		return
	}
	if timeout <= 0 {
		a.writeError(w, http.StatusBadRequest, "timeout must be greater than 0")
		return
	}

	heartbeat, err := time.ParseDuration(req.Settings.Heartbeat)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid heartbeat: "+err.Error())
		return
	}
	if heartbeat <= 0 {
		a.writeError(w, http.StatusBadRequest, "heartbeat must be greater than 0")
		return
	}

	cfgRefresh, err := time.ParseDuration(req.Settings.ConfigRefresh)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid configRefresh: "+err.Error())
		return
	}
	if cfgRefresh <= 0 {
		a.writeError(w, http.StatusBadRequest, "configRefresh must be greater than 0")
		return
	}

	// Handle token - only allow setting it once
	newToken := req.Settings.Token
	if cfg.Token != "" && newToken != "" {
		// Token is already set and user is trying to change it - not allowed
		a.writeError(w, http.StatusForbidden, "token is already configured and cannot be changed via API")
		return
	}

	// Update config (port remains unchanged - requires server restart)
	cfg.Network = req.Settings.Network
	cfg.Timeout = timeout
	cfg.Heartbeat = heartbeat
	cfg.ConfigRefresh = cfgRefresh
	cfg.Whoami = req.Settings.Whoami
	if newToken != "" {
		cfg.Token = newToken
	}
	cfg.Users = req.Settings.Users

	if err := a.cfgMgr.Update(cfg); err != nil {
		a.logger.Error("update config", err)
		a.writeError(w, http.StatusInternalServerError, "failed to update config: "+err.Error())
		return
	}

	// Immediately reload config from disk to verify persistence and ensure consistency
	if err := a.cfgMgr.Reload(); err != nil {
		a.logger.Error("reload config after update", err)
		a.writeError(w, http.StatusInternalServerError, "settings saved but verification failed: "+err.Error())
		return
	}

	// Get the freshly loaded config to verify all changes were persisted
	verifiedCfg := a.cfgMgr.Get()

	// Restart telegram service if any relevant settings changed
	usersChanged := !eqInt64Slices(oldUsers, verifiedCfg.Users)
	tokenChanged := oldToken != verifiedCfg.Token
	whoamiChanged := oldWhoami != verifiedCfg.Whoami

	if tokenChanged || usersChanged || whoamiChanged {
		if verifiedCfg.Token != "" {
			// Token is set - restart with new config
			if a.telegramCancel != nil {
				a.telegramCancel()
			}
			if a.telegram != nil {
				a.telegram.Shutdown()
			}
			a.telegram = telegram.New(verifiedCfg.Token, verifiedCfg.Users, a, a.logger, verifiedCfg.Whoami)
			a.telegramCtx, a.telegramCancel = context.WithCancel(context.Background())
			if err := a.telegram.Start(a.telegramCtx); err != nil {
				a.logger.Error("start telegram service after settings change", err)
			}
		} else {
			// Token was cleared - stop telegram service
			if a.telegramCancel != nil {
				a.telegramCancel()
			}
			if a.telegram != nil {
				a.telegram.Shutdown()
			}
		}
	}

	a.logger.Info("settings updated and verified successfully",
		"tokenChanged", tokenChanged,
		"usersChanged", usersChanged,
		"whoamiChanged", whoamiChanged,
		"network", verifiedCfg.Network,
		"heartbeat", verifiedCfg.Heartbeat,
		"timeout", verifiedCfg.Timeout,
		"configRefresh", verifiedCfg.ConfigRefresh,
		"whoami", verifiedCfg.Whoami,
		"usersCount", len(verifiedCfg.Users),
		"tokenSet", verifiedCfg.Token != "",
	)

	response := settingsResponse{
		Network:       verifiedCfg.Network,
		Heartbeat:     verifiedCfg.Heartbeat.String(),
		Timeout:       verifiedCfg.Timeout.String(),
		ConfigRefresh: verifiedCfg.ConfigRefresh.String(),
		TokenSet:      verifiedCfg.Token != "",
		Users:         verifiedCfg.Users,
		Whoami:        verifiedCfg.Whoami,
	}
	a.writeJSON(w, http.StatusOK, response)
}

// Auth-related request/response types
type loginRequest struct {
	Password string `json:"password"`
}

type loginResponse struct {
	Token   string `json:"token"`
	Message string `json:"message"`
}

type setupRequest struct {
	Password string `json:"password"`
}

type setupResponse struct {
	Token   string `json:"token"`
	Message string `json:"message"`
}

type authStatusResponse struct {
	PasswordSet bool `json:"passwordSet"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type changePasswordResponse struct {
	Message string `json:"message"`
}

// handleLogin handles user login by validating password and returning JWT token
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.cfgMgr.Get()

	// If no password hash is set, return error
	if cfg.PasswordHash == "" {
		a.writeError(w, http.StatusUnauthorized, "server not yet configured with password")
		return
	}

	var req loginRequest
	if err := a.decodeJSON(r, &req); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Password == "" {
		a.writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	// Verify password using Argon2
	hasher := &auth.Hasher{}
	if err := hasher.VerifyPassword(cfg.PasswordHash, req.Password); err != nil {
		a.logger.Info("failed login attempt")
		a.writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	// Generate JWT token
	tokenManager := auth.NewTokenManager(cfg.JWTSecret)
	token, err := tokenManager.GenerateToken()
	if err != nil {
		a.logger.Error("generate token", err)
		a.writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	// Set secure cookie using gorilla/securecookie
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(auth.TokenExpiration.Seconds()),
		HttpOnly: true,
		Secure:   false, // Set to true in production with HTTPS
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)

	a.logger.Info("user logged in successfully")
	a.writeJSON(w, http.StatusOK, loginResponse{
		Token:   token,
		Message: "login successful",
	})
}

// handleSetupPassword handles initial password setup
func (a *App) handleSetupPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.cfgMgr.Get()

	// If password is already set, deny further setup
	if cfg.PasswordHash != "" {
		a.writeError(w, http.StatusForbidden, "password is already configured")
		return
	}

	var req setupRequest
	if err := a.decodeJSON(r, &req); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Password == "" {
		a.writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	// Hash password using Argon2id
	hasher := &auth.Hasher{}
	hash, err := hasher.HashPassword(req.Password)
	if err != nil {
		a.logger.Error("hash password", err)
		a.writeError(w, http.StatusInternalServerError, "failed to process password")
		return
	}

	// Update config with password hash
	cfg.PasswordHash = hash
	if err := a.cfgMgr.Update(cfg); err != nil {
		a.logger.Error("update config with password", err)
		a.writeError(w, http.StatusInternalServerError, "failed to save password")
		return
	}

	// Reload config to verify
	if err := a.cfgMgr.Reload(); err != nil {
		a.logger.Error("reload config after password setup", err)
		a.writeError(w, http.StatusInternalServerError, "password saved but verification failed")
		return
	}

	// Get fresh config after reload
	freshCfg := a.cfgMgr.Get()

	// Generate JWT token for immediate login
	tokenManager := auth.NewTokenManager(freshCfg.JWTSecret)
	token, err := tokenManager.GenerateToken()
	if err != nil {
		a.logger.Error("generate token after setup", err)
		a.writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	// Set secure cookie
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(auth.TokenExpiration.Seconds()),
		HttpOnly: true,
		Secure:   false, // Set to true in production with HTTPS
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)

	a.logger.Info("password configured successfully")
	a.writeJSON(w, http.StatusOK, setupResponse{
		Token:   token,
		Message: "password configured successfully",
	})
}

// handleLogout clears the authentication cookie
func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Clear cookie
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})

	a.logger.Info("user logged out")
	a.writeJSON(w, http.StatusOK, map[string]string{"message": "logout successful"})
}

// handleAuthStatus returns the authentication status
func (a *App) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.cfgMgr.Get()
	a.writeJSON(w, http.StatusOK, authStatusResponse{
		PasswordSet: cfg.PasswordHash != "",
	})
}

// handleChangePassword handles authenticated password changes
func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Verify authentication
	if !a.isAuthenticatedRequest(r) {
		a.writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	cfg := a.cfgMgr.Get()

	// If no password is currently set, this is an error
	if cfg.PasswordHash == "" {
		a.writeError(w, http.StatusForbidden, "no password currently configured")
		return
	}

	var req changePasswordRequest
	if err := a.decodeJSON(r, &req); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		a.writeError(w, http.StatusBadRequest, "current password and new password are required")
		return
	}

	// Verify current password
	hasher := &auth.Hasher{}
	if err := hasher.VerifyPassword(cfg.PasswordHash, req.CurrentPassword); err != nil {
		a.logger.Info("failed password change attempt - invalid current password")
		a.writeError(w, http.StatusUnauthorized, "invalid current password")
		return
	}

	// Hash new password
	newHash, err := hasher.HashPassword(req.NewPassword)
	if err != nil {
		a.logger.Error("hash new password", err)
		a.writeError(w, http.StatusInternalServerError, "failed to process new password")
		return
	}

	// Update config with new password hash
	cfg.PasswordHash = newHash
	if err := a.cfgMgr.Update(cfg); err != nil {
		a.logger.Error("update config with new password", err)
		a.writeError(w, http.StatusInternalServerError, "failed to save new password")
		return
	}

	// Reload config to verify
	if err := a.cfgMgr.Reload(); err != nil {
		a.logger.Error("reload config after password change", err)
		a.writeError(w, http.StatusInternalServerError, "password changed but verification failed")
		return
	}

	a.logger.Info("password changed successfully")
	a.writeJSON(w, http.StatusOK, changePasswordResponse{
		Message: "password changed successfully",
	})
}

func (a *App) handleStatusTable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	hosts := a.getHostStatuses()
	renderHTML(w, templates.StatusTableRows(hosts))
}
