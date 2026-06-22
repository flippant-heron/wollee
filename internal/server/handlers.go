package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

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
	a.writeJSON(w, http.StatusOK, hostStatus{
		HostRecord:        host,
		Active:            true,
		HeartbeatInterval: a.cfg.DefaultHeartbeatInterval.String(),
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

	if err := internalwol.SendMagicPacket(host.MAC, a.cfg.SubnetBroadcast); err != nil {
		a.logger.Error("send magic packet", err, "mac", host.MAC, "hostname", host.Hostname, "broadcast", a.cfg.SubnetBroadcast)
		a.writeError(w, http.StatusBadGateway, "failed to send magic packet")
		return
	}

	a.logger.Info("sent magic packet", "mac", host.MAC, "hostname", host.Hostname, "broadcast", a.cfg.SubnetBroadcast)
	a.writeJSON(w, http.StatusOK, hostStatus{HostRecord: host, Active: host.LastSeen.After(time.Now().Add(-activeWindow))})
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	records := a.registry.List()
	hosts := make([]hostStatus, 0, len(records))
	cutoff := time.Now().Add(-activeWindow)
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
	hosts := a.registry.List()
	if len(hosts) == 0 {
		return "No registered hosts."
	}

	cutoff := time.Now().Add(-activeWindow)
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
	if err := internalwol.SendMagicPacket(host.MAC, a.cfg.SubnetBroadcast); err != nil {
		a.logger.Error("send magic packet from telegram", err, "mac", host.MAC, "hostname", host.Hostname)
		return "Failed to send Wake-on-LAN packet."
	}
	return fmt.Sprintf("Sent wake signal to %s (%s).", host.Hostname, host.MAC)
}
