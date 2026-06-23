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
		HeartbeatInterval: cfg.DefaultHeartbeatInterval.String(),
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
	if err := internalwol.SendMagicPacket(host.MAC, cfg.SubnetBroadcast); err != nil {
		a.logger.Error("send magic packet", err, "mac", host.MAC, "hostname", host.Hostname, "broadcast", cfg.SubnetBroadcast)
		a.writeError(w, http.StatusBadGateway, "failed to send magic packet")
		return
	}

	a.logger.Info("sent magic packet", "mac", host.MAC, "hostname", host.Hostname, "broadcast", cfg.SubnetBroadcast)
	a.writeJSON(w, http.StatusOK, hostStatus{HostRecord: host, Active: host.LastSeen.After(time.Now().Add(-cfg.ActiveTimeout))})
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.cfgMgr.Get()
	records := a.registry.List()
	hosts := make([]hostStatus, 0, len(records))
	cutoff := time.Now().Add(-cfg.ActiveTimeout)
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

	cutoff := time.Now().Add(-cfg.ActiveTimeout)
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
	if err := internalwol.SendMagicPacket(host.MAC, cfg.SubnetBroadcast); err != nil {
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
		HeartbeatInterval: cfg.DefaultHeartbeatInterval.String(),
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
		SubnetBroadcast:          cfg.SubnetBroadcast,
		ActiveTimeout:            cfg.ActiveTimeout.String(),
		DefaultHeartbeatInterval: cfg.DefaultHeartbeatInterval.String(),
		TelegramTokenSet:         cfg.TelegramToken != "",
		AllowedTelegramUsers:     cfg.AllowedTelegramUsers,
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

	if err := internalwol.ValidateBroadcast(req.Settings.SubnetBroadcast); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid subnet broadcast: "+err.Error())
		return
	}

	activeTimeout, err := time.ParseDuration(req.Settings.ActiveTimeout)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid activeTimeout: "+err.Error())
		return
	}
	if activeTimeout <= 0 {
		a.writeError(w, http.StatusBadRequest, "activeTimeout must be greater than 0")
		return
	}

	heartbeatInterval, err := time.ParseDuration(req.Settings.DefaultHeartbeatInterval)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid defaultHeartbeatInterval: "+err.Error())
		return
	}
	if heartbeatInterval <= 0 {
		a.writeError(w, http.StatusBadRequest, "defaultHeartbeatInterval must be greater than 0")
		return
	}

	// Handle telegram token - only allow setting it once
	newToken := req.Settings.TelegramToken
	if cfg.TelegramToken != "" && newToken != "" {
		// Token is already set and user is trying to change it - not allowed
		a.writeError(w, http.StatusForbidden, "telegram token is already configured and cannot be changed via API")
		return
	}

	// If trying to set a token, validate that users are configured
	if newToken != "" && len(req.Settings.AllowedTelegramUsers) == 0 {
		a.writeError(w, http.StatusBadRequest, "allowedTelegramUsers must contain at least one user when telegramToken is set")
		return
	}

	// Update config (port remains unchanged - requires server restart)
	cfg.SubnetBroadcast = req.Settings.SubnetBroadcast
	cfg.ActiveTimeout = activeTimeout
	cfg.DefaultHeartbeatInterval = heartbeatInterval
	if newToken != "" {
		cfg.TelegramToken = newToken
	}
	cfg.AllowedTelegramUsers = req.Settings.AllowedTelegramUsers

	if err := a.cfgMgr.Update(cfg); err != nil {
		a.logger.Error("update config", err)
		a.writeError(w, http.StatusInternalServerError, "failed to update config: "+err.Error())
		return
	}

	// Restart telegram service if token was just set
	if newToken != "" && cfg.TelegramToken == newToken {
		if a.telegramCancel != nil {
			a.telegramCancel()
		}
		a.telegram = telegram.New(cfg.TelegramToken, cfg.AllowedTelegramUsers, a, a.logger)
		a.telegramCtx, a.telegramCancel = context.WithCancel(context.Background())
		if err := a.telegram.Start(a.telegramCtx); err != nil {
			a.logger.Error("start telegram service", err)
		}
	} else if cfg.TelegramToken == "" {
		// Token was cleared - stop telegram service
		if a.telegramCancel != nil {
			a.telegramCancel()
		}
	}

	a.logger.Info("settings updated successfully")
	response := settingsResponse{
		SubnetBroadcast:          cfg.SubnetBroadcast,
		ActiveTimeout:            cfg.ActiveTimeout.String(),
		DefaultHeartbeatInterval: cfg.DefaultHeartbeatInterval.String(),
		TelegramTokenSet:         cfg.TelegramToken != "",
		AllowedTelegramUsers:     cfg.AllowedTelegramUsers,
	}
	a.writeJSON(w, http.StatusOK, response)
}
