package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/flippant-heron/wollee/internal/config"
	appservice "github.com/flippant-heron/wollee/internal/service"
)

const (
	maxBackoff        = 5 * time.Minute
	requestTimeout    = 10 * time.Second
	defaultServerPort = "80"
	defaultTLSPort    = "443"
)

type App struct {
	cfg        config.ClientConfig
	httpClient *http.Client
	logger     *appservice.Logger
}

type heartbeatPayload struct {
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

func New(cfg config.ClientConfig, logger *appservice.Logger) *App {
	return &App{
		cfg:    cfg,
		logger: logger,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}
}

func (a *App) Run(ctx context.Context) error {
	backoff := time.Duration(0)

	for {
		if backoff > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}

		if err := a.sendHeartbeat(ctx); err != nil {
			if backoff == 0 {
				backoff = a.cfg.HeartbeatInterval
			} else {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			a.logger.Warning("heartbeat failed; backing off", "backoff", backoff.String(), "server_url", a.cfg.ServerURL, "mac", a.cfg.MACAddress, "error", err.Error())
			continue
		}

		backoff = a.cfg.HeartbeatInterval
	}
}

func (a *App) Shutdown(context.Context) error {
	return nil
}

func (a *App) sendHeartbeat(ctx context.Context) error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("lookup hostname: %w", err)
	}

	ipAddress, err := resolveLocalIP(a.cfg.ServerURL)
	if err != nil {
		return fmt.Errorf("resolve local IP: %w", err)
	}

	payload, err := json.Marshal(heartbeatPayload{
		MAC:      a.cfg.MACAddress,
		Hostname: hostname,
		IP:       ipAddress,
	})
	if err != nil {
		return fmt.Errorf("marshal heartbeat payload: %w", err)
	}

	registerURL, err := endpointURL(a.cfg.ServerURL, "/register")
	if err != nil {
		return fmt.Errorf("build register endpoint: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send register request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected register status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	a.logger.Info("heartbeat sent", "server_url", a.cfg.ServerURL, "hostname", hostname, "ip", ipAddress, "mac", a.cfg.MACAddress)
	return nil
}

func endpointURL(base string, path string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	ref, err := url.Parse(path)
	if err != nil {
		return "", err
	}

	return parsed.ResolveReference(ref).String(), nil
}

func resolveLocalIP(serverURL string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}

	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		port = defaultServerPort
		if parsed.Scheme == "https" {
			port = defaultTLSPort
		}
	}

	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, port), 2*time.Second)
	if err == nil {
		defer conn.Close()
		udpAddr, ok := conn.LocalAddr().(*net.UDPAddr)
		if ok && udpAddr.IP != nil {
			return udpAddr.IP.String(), nil
		}
	}

	interfaces, ifaceErr := net.Interfaces()
	if ifaceErr != nil {
		if err != nil {
			return "", errors.Join(err, ifaceErr)
		}
		return "", ifaceErr
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, addrErr := iface.Addrs()
		if addrErr != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
				continue
			}
			if ipv4 := ipNet.IP.To4(); ipv4 != nil {
				return ipv4.String(), nil
			}
		}
	}

	if err != nil {
		return "", err
	}

	return "", errors.New("no non-loopback IPv4 address found")
}
