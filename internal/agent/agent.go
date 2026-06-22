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

	appservice "github.com/flippant-heron/wollee/internal/service"
	internalwol "github.com/flippant-heron/wollee/internal/wol"
)

const (
	maxBackoff     = 5 * time.Minute
	defaultRegPath = "/register"
)

type Options struct {
	Upstreams        []string
	RegisterPath     string
	RequestTimeout   time.Duration
	InitialHeartbeat time.Duration
}

type App struct {
	opts       Options
	httpClient *http.Client
	logger     *appservice.Logger
	interval   time.Duration
}

type heartbeatPayload struct {
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

type registerResponse struct {
	HeartbeatInterval string `json:"heartbeatInterval"`
}

func New(opts Options, logger *appservice.Logger) *App {
	registerPath := strings.TrimSpace(opts.RegisterPath)
	if registerPath == "" {
		registerPath = defaultRegPath
	}
	if !strings.HasPrefix(registerPath, "/") {
		registerPath = "/" + registerPath
	}

	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 10 * time.Second
	}
	if opts.InitialHeartbeat <= 0 {
		opts.InitialHeartbeat = 30 * time.Second
	}
	opts.RegisterPath = registerPath

	return &App{
		opts:   opts,
		logger: logger,
		httpClient: &http.Client{
			Timeout: opts.RequestTimeout,
		},
		interval: opts.InitialHeartbeat,
	}
}

func (a *App) Run(ctx context.Context) error {
	backoff := time.Duration(0)
	for {
		wait := a.interval
		if backoff > 0 {
			wait = backoff
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}

		nextInterval, err := a.sendHeartbeat(ctx)
		if err != nil {
			if backoff == 0 {
				backoff = a.interval
			} else {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			a.logger.Warning("heartbeat failed; backing off", "backoff", backoff.String(), "error", err.Error())
			continue
		}

		backoff = 0
		a.interval = nextInterval
	}
}

func (a *App) Shutdown(context.Context) error {
	return nil
}

func (a *App) sendHeartbeat(ctx context.Context) (time.Duration, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return 0, fmt.Errorf("lookup hostname: %w", err)
	}

	var errs []error
	for _, upstream := range a.opts.Upstreams {
		interval, sendErr := a.sendHeartbeatToUpstream(ctx, strings.TrimSpace(upstream), hostname)
		if sendErr == nil {
			return interval, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", upstream, sendErr))
	}

	return 0, errors.Join(errs...)
}

func (a *App) sendHeartbeatToUpstream(ctx context.Context, upstream string, hostname string) (time.Duration, error) {
	if upstream == "" {
		return 0, errors.New("upstream is empty")
	}

	registerURL, err := endpointURL(upstream, a.opts.RegisterPath)
	if err != nil {
		return 0, fmt.Errorf("build register endpoint: %w", err)
	}

	ipAddress, err := resolveLocalIP(upstream)
	if err != nil {
		return 0, fmt.Errorf("resolve local IP: %w", err)
	}

	macAddress, err := resolveLocalMAC(ipAddress)
	if err != nil {
		return 0, fmt.Errorf("resolve local MAC: %w", err)
	}

	payload, err := json.Marshal(heartbeatPayload{
		MAC:      macAddress,
		Hostname: hostname,
		IP:       ipAddress,
	})
	if err != nil {
		return 0, fmt.Errorf("marshal heartbeat payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("create register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send register request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			a.logger.Warning("close register response body", "error", closeErr.Error())
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return 0, fmt.Errorf("read register response body: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("unexpected register status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	interval := a.interval
	if parsed, parseErr := parseHeartbeatResponse(body); parseErr == nil {
		interval = parsed
	}

	a.logger.Info("heartbeat sent", "upstream", upstream, "hostname", hostname, "ip", ipAddress, "mac", macAddress, "interval", interval.String())
	return interval, nil
}

func parseHeartbeatResponse(body []byte) (time.Duration, error) {
	var response registerResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, err
	}
	if strings.TrimSpace(response.HeartbeatInterval) == "" {
		return 0, errors.New("heartbeatInterval missing")
	}
	interval, err := time.ParseDuration(response.HeartbeatInterval)
	if err != nil {
		return 0, err
	}
	if interval <= 0 {
		return 0, errors.New("heartbeatInterval must be positive")
	}
	return interval, nil
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
	if host == "" {
		return "", errors.New("upstream host is required")
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
		if strings.EqualFold(parsed.Scheme, "https") {
			port = "443"
		}
	}

	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, port), 2*time.Second)
	if err == nil {
		defer func() {
			_ = conn.Close()
		}()
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

func resolveLocalMAC(ipAddress string) (string, error) {
	target := net.ParseIP(ipAddress)
	if target == nil {
		return "", errors.New("invalid local IP")
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}

		addresses, addrErr := iface.Addrs()
		if addrErr != nil {
			continue
		}

		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok || ipNet.IP == nil {
				continue
			}
			if ipNet.Contains(target) {
				return internalwol.NormalizeMAC(iface.HardwareAddr.String())
			}
		}
	}

	return "", errors.New("unable to resolve local interface MAC")
}

func ParseUpstreams(raw []string) []string {
	parts := make([]string, 0, len(raw))
	for _, value := range raw {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		}) {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
	}
	return parts
}
