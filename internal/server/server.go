package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/flippant-heron/wollee/internal/config"
	appservice "github.com/flippant-heron/wollee/internal/service"
	internalwol "github.com/flippant-heron/wollee/internal/wol"
	webassets "github.com/flippant-heron/wollee/web"
)

const activeWindow = 5 * time.Minute

type App struct {
	cfg       config.ServerConfig
	logger    *appservice.Logger
	registry  *Registry
	httpSrv   *http.Server
	bot       *tgbotapi.BotAPI
	allowed   map[int64]struct{}
	staticFS  fs.FS
	indexHTML []byte
}

type registerRequest struct {
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

type wakeRequest struct {
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
}

type statusResponse struct {
	Hosts []hostStatus `json:"hosts"`
}

type hostStatus struct {
	HostRecord
	Active bool `json:"active"`
}

func New(cfg config.ServerConfig, registry *Registry, logger *appservice.Logger) (*App, error) {
	if registry == nil {
		return nil, errors.New("registry is required")
	}

	staticFS, err := fs.Sub(webassets.Assets, "static")
	if err != nil {
		return nil, fmt.Errorf("open static assets: %w", err)
	}

	indexHTML, err := webassets.Assets.ReadFile("index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}

	allowed := make(map[int64]struct{}, len(cfg.AllowedTelegramUsers))
	for _, userID := range cfg.AllowedTelegramUsers {
		allowed[userID] = struct{}{}
	}

	return &App{
		cfg:       cfg,
		logger:    logger,
		registry:  registry,
		allowed:   allowed,
		staticFS:  staticFS,
		indexHTML: indexHTML,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(a.staticFS))))
	mux.HandleFunc("/register", a.handleRegister)
	mux.HandleFunc("/wake", a.handleWake)
	mux.HandleFunc("/status", a.handleStatus)

	a.httpSrv = &http.Server{
		Addr:              fmt.Sprintf(":%d", a.cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	if a.cfg.TelegramToken != "" {
		if err := a.startTelegram(ctx); err != nil {
			return err
		}
	}

	a.logger.Info("starting server", "port", a.cfg.Port, "broadcast", a.cfg.SubnetBroadcast)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("shutdown server", err)
		}
	}()

	if err := a.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	var errs []error

	if a.bot != nil {
		a.bot.StopReceivingUpdates()
	}

	if a.httpSrv != nil {
		if err := a.httpSrv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

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
	a.writeJSON(w, http.StatusOK, hostStatus{HostRecord: host, Active: true})
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
		hosts = append(hosts, hostStatus{
			HostRecord: host,
			Active:     host.LastSeen.After(cutoff),
		})
	}

	a.writeJSON(w, http.StatusOK, statusResponse{Hosts: hosts})
}

func (a *App) startTelegram(ctx context.Context) error {
	bot, err := tgbotapi.NewBotAPI(a.cfg.TelegramToken)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}

	a.bot = bot
	updates := bot.GetUpdatesChan(tgbotapi.NewUpdate(0))

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				if update.Message == nil {
					continue
				}
				a.handleTelegramMessage(update.Message)
			}
		}
	}()

	return nil
}

func (a *App) handleTelegramMessage(message *tgbotapi.Message) {
	if message.From == nil {
		return
	}

	if _, ok := a.allowed[message.From.ID]; !ok {
		a.replyTelegram(message.Chat.ID, "unauthorized")
		return
	}

	command := strings.ToLower(message.Command())
	switch command {
	case "list":
		hosts := a.registry.List()
		if len(hosts) == 0 {
			a.replyTelegram(message.Chat.ID, "No registered hosts.")
			return
		}

		var builder strings.Builder
		builder.WriteString("Registered hosts:\n")
		cutoff := time.Now().Add(-activeWindow)
		for _, host := range hosts {
			status := "offline"
			if host.LastSeen.After(cutoff) {
				status = "online"
			}
			builder.WriteString(fmt.Sprintf("- %s (%s) %s [%s]\n", host.Hostname, host.MAC, host.IP, status))
		}
		a.replyTelegram(message.Chat.ID, builder.String())
	case "wake":
		target := strings.TrimSpace(message.CommandArguments())
		if target == "" {
			a.replyTelegram(message.Chat.ID, "Usage: /wake <hostname>")
			return
		}
		host, err := a.resolveWakeTarget(wakeRequest{Hostname: target})
		if err != nil {
			if errors.Is(err, errHostNotFound) {
				host, err = a.resolveWakeTarget(wakeRequest{MAC: target})
			}
		}
		if err != nil {
			a.replyTelegram(message.Chat.ID, err.Error())
			return
		}
		if err := internalwol.SendMagicPacket(host.MAC, a.cfg.SubnetBroadcast); err != nil {
			a.logger.Error("send magic packet from telegram", err, "mac", host.MAC, "hostname", host.Hostname)
			a.replyTelegram(message.Chat.ID, "Failed to send Wake-on-LAN packet.")
			return
		}
		a.replyTelegram(message.Chat.ID, fmt.Sprintf("Sent wake signal to %s (%s).", host.Hostname, host.MAC))
	default:
		a.replyTelegram(message.Chat.ID, "Supported commands: /list, /wake <hostname>")
	}
}

func (a *App) replyTelegram(chatID int64, text string) {
	if a.bot == nil {
		return
	}

	if _, err := a.bot.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		a.logger.Error("send telegram reply", err, "chat_id", chatID)
	}
}

var errHostNotFound = errors.New("host not found")

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
