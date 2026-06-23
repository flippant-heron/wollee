package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/flippant-heron/wollee/internal/config"
	appservice "github.com/flippant-heron/wollee/internal/service"
	"github.com/flippant-heron/wollee/internal/telegram"
	webassets "github.com/flippant-heron/wollee/web"
)

type App struct {
	cfgMgr         *config.Manager
	logger         *appservice.Logger
	registry       *Registry
	httpSrv        *http.Server
	telegram       *telegram.Service
	telegramCtx    context.Context
	telegramCancel context.CancelFunc
	staticFS       fs.FS
	indexHTML      []byte
	addHostHTML    []byte
	settingsHTML   []byte
	reloadTicker   *time.Ticker
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
	Active            bool   `json:"active"`
	HeartbeatInterval string `json:"heartbeatInterval,omitempty"`
}

type serverSettingsRequest struct {
	Network       string  `json:"network"`
	Heartbeat     string  `json:"heartbeat"`
	Timeout       string  `json:"timeout"`
	ConfigRefresh string  `json:"configRefresh"`
	Token         string  `json:"token"`
	Users         []int64 `json:"users"`
}

type settingsUpdateRequest struct {
	Settings serverSettingsRequest `json:"settings"`
}

type settingsResponse struct {
	Network       string  `json:"network"`
	Heartbeat     string  `json:"heartbeat"`
	Timeout       string  `json:"timeout"`
	ConfigRefresh string  `json:"configRefresh"`
	TokenSet      bool    `json:"tokenSet"`
	Users         []int64 `json:"users"`
}

func New(cfgMgr *config.Manager, registry *Registry, logger *appservice.Logger) (*App, error) {
	if cfgMgr == nil {
		return nil, errors.New("config manager is required")
	}
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

	addHostHTML, err := webassets.Assets.ReadFile("add-host.html")
	if err != nil {
		return nil, fmt.Errorf("read add-host.html: %w", err)
	}

	settingsHTML, err := webassets.Assets.ReadFile("settings.html")
	if err != nil {
		return nil, fmt.Errorf("read settings.html: %w", err)
	}

	for _, requiredAsset := range []string{"alpine.min.js", "pico.min.css"} {
		if _, err := fs.ReadFile(staticFS, requiredAsset); err != nil {
			return nil, fmt.Errorf("missing embedded asset %q: run `task assets:dl` before build", requiredAsset)
		}
	}

	cfg := cfgMgr.Get()
	app := &App{
		cfgMgr:       cfgMgr,
		logger:       logger,
		registry:     registry,
		staticFS:     staticFS,
		indexHTML:    indexHTML,
		addHostHTML:  addHostHTML,
		settingsHTML: settingsHTML,
	}

	app.telegram = telegram.New(cfg.Token, cfg.Users, app, logger)
	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	cfg := a.cfgMgr.Get()
	a.httpSrv = &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           a.newRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Create a cancellable context for the telegram service so we can restart it
	a.telegramCtx, a.telegramCancel = context.WithCancel(ctx)

	if err := a.telegram.Start(a.telegramCtx); err != nil {
		return err
	}

	a.logger.Info("starting server", "port", cfg.Port, "broadcast", cfg.Network)

	// Start periodic config reload (every 300 seconds)
	a.reloadTicker = time.NewTicker(300 * time.Second)
	go a.reloadConfigPeriodically(ctx)

	go a.shutdownOnContext(ctx)

	if err := a.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}
	return nil
}

func (a *App) reloadConfigPeriodically(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.reloadTicker.C:
			if err := a.reloadConfig(); err != nil {
				a.logger.Error("periodic config reload failed", err)
			}
		}
	}
}

// reloadConfig reloads the configuration from disk and restarts services if needed.
func (a *App) reloadConfig() error {
	oldCfg := a.cfgMgr.Get()
	if err := a.cfgMgr.Reload(); err != nil {
		return err
	}
	newCfg := a.cfgMgr.Get()

	// If telegram settings changed, restart the telegram service
	if oldCfg.Token != newCfg.Token ||
		!eqInt64Slices(oldCfg.Users, newCfg.Users) {
		// Stop the old telegram service
		a.telegramCancel()
		a.telegram.Shutdown()

		// Create a new telegram service with updated config
		a.telegram = telegram.New(newCfg.Token, newCfg.Users, a, a.logger)

		// Create a new context for the telegram service (child of the main context through Run())
		// We create a new child context since the old one was cancelled
		// This allows telegram to restart while the server continues running
		a.telegramCtx, a.telegramCancel = context.WithCancel(context.Background())

		// Start the new telegram service
		if err := a.telegram.Start(a.telegramCtx); err != nil {
			a.logger.Error("restart telegram service after config reload", err)
			return err
		}

		a.logger.Info("telegram service restarted with new config")
	}

	return nil
}

func eqInt64Slices(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

func (a *App) Shutdown(ctx context.Context) error {
	if a.reloadTicker != nil {
		a.reloadTicker.Stop()
	}
	a.telegram.Shutdown()
	if a.httpSrv == nil {
		return nil
	}
	if err := a.httpSrv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *App) shutdownOnContext(ctx context.Context) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		a.logger.Error("shutdown server", err)
	}
}
