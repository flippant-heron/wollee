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

	"github.com/flippant-heron/wollee/internal/telegram"
	internalwol "github.com/flippant-heron/wollee/internal/wol"
)

var errHostNotFound = errors.New("host not found")

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(a.indexHTML); err != nil {
		a.logger.Error("write index response", err)
	}
}

func (a *App) handleAddHostPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(a.addHostHTML); err != nil {
		a.logger.Error("write add-host response", err)
	}
}

func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req registerRequest
	if err := a.decodeJSON(r, &req); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	mac, err := internalwol.NormalizeMAC(req.MAC)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid MAC address")
		return
	}
	if req.Hostname == "" {
		req.Hostname = mac
	}

	ip := net.ParseIP(req.IP)
	if ip == nil || ip.To4() == nil {
		a.writeError(w, http.StatusBadRequest, "invalid IPv4 address")
		return
	}

	host := HostRecord{
		MAC:      mac,
		Hostname: strings.TrimSpace(req.Hostname),
		IP:       ip.String(),
		LastSeen: time.Now().UTC(),
	}
	if err := a.registry.Upsert(host); err != nil {
		a.logger.Error("persist host registration", err, "mac", mac)
		a.writeError(w, http.StatusInternalServerError, "failed to store host")
		return
	}

	a.logger.Info("registered host", "mac", host.MAC, "hostname", host.Hostname, "ip", host.IP)
	cfg := a.cfgMgr.Get()
	a.writeJSON(w, http.StatusOK, hostStatus{
		HostRecord:        host,
		Active:            true,
		HeartbeatInterval: cfg.Heartbeat.String(),
	})
}

func (a *App) handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req wakeRequest
	if err := a.decodeJSON(r, &req); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	host, err := a.resolveWakeTarget(req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errHostNotFound) {
			status = http.StatusNotFound
		}
		a.writeError(w, status, err.Error())
		return
	}

	cfg := a.cfgMgr.Get()
	if err := internalwol.SendMagicPacket(host.MAC, cfg.Network); err != nil {
		a.logger.Error("send magic packet", err, "mac", host.MAC, "hostname", host.Hostname, "broadcast", cfg.Network)
		a.writeError(w, http.StatusBadGateway, "failed to send magic packet")
		return
	}

	a.logger.Info("sent magic packet", "mac", host.MAC, "hostname", host.Hostname, "broadcast", cfg.Network)
	a.writeJSON(w, http.StatusOK, hostStatus{HostRecord: host, Active: host.LastSeen.After(time.Now().Add(-cfg.Timeout))})
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.cfgMgr.Get()
	records := a.registry.List()
	hosts := make([]hostStatus, 0, len(records))
	cutoff := time.Now().Add(-cfg.Timeout)
	for _, host := range records {
		hosts = append(hosts, hostStatus{HostRecord: host, Active: host.LastSeen.After(cutoff)})
	}
	a.writeJSON(w, http.StatusOK, statusResponse{Hosts: hosts})
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

	var req registerRequest
	if err := a.decodeJSON(r, &req); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	mac, err := internalwol.NormalizeMAC(req.MAC)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid MAC address")
		return
	}

	if req.Hostname == "" {
		req.Hostname = mac
	}

	ip := net.ParseIP(req.IP)
	if ip == nil || ip.To4() == nil {
		a.writeError(w, http.StatusBadRequest, "invalid IPv4 address")
		return
	}

	host := HostRecord{
		MAC:      mac,
		Hostname: strings.TrimSpace(req.Hostname),
		IP:       ip.String(),
		LastSeen: time.Now().UTC(),
	}
	if err := a.registry.Upsert(host); err != nil {
		a.logger.Error("persist host addition", err, "mac", mac)
		a.writeError(w, http.StatusInternalServerError, "failed to store host")
		return
	}

	a.logger.Info("added host", "mac", host.MAC, "hostname", host.Hostname, "ip", host.IP)
	cfg := a.cfgMgr.Get()
	a.writeJSON(w, http.StatusOK, hostStatus{
		HostRecord:        host,
		Active:            true,
		HeartbeatInterval: cfg.Heartbeat.String(),
	})
}

func (a *App) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract MAC from path: /hosts/{mac}
	mac := strings.TrimSpace(r.PathValue("mac"))
	if mac == "" {
		a.writeError(w, http.StatusBadRequest, "MAC address is required")
		return
	}

	normalized, err := internalwol.NormalizeMAC(mac)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid MAC address")
		return
	}

	// Check host exists before deleting
	if _, ok := a.registry.FindByMAC(normalized); !ok {
		a.writeError(w, http.StatusNotFound, "host not found")
		return
	}

	if err := a.registry.Delete(normalized); err != nil {
		a.logger.Error("delete host", err, "mac", normalized)
		a.writeError(w, http.StatusInternalServerError, "failed to delete host")
		return
	}

	a.logger.Info("deleted host", "mac", normalized)
	a.writeJSON(w, http.StatusOK, map[string]string{"message": "host deleted"})
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(a.settingsHTML); err != nil {
		a.logger.Error("write settings response", err)
	}
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
