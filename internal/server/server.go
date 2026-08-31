package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/flippant-heron/wollee/internal/config"
	appservice "github.com/flippant-heron/wollee/internal/service"
	"github.com/flippant-heron/wollee/internal/telegram"
	webassets "github.com/flippant-heron/wollee/web"
)

type App struct {
	cfgMgr             *config.Manager
	logger             *appservice.Logger
	registry           *Registry
	httpSrv            *http.Server
	telegram           *telegram.Service
	telegramCtx        context.Context
	telegramCancel     context.CancelFunc
	staticFS           fs.FS
	logoDataURI        string
	faviconData        []byte
	faviconContentType string
	reloadTicker       *time.Ticker
	registerLimiter    *RateLimiter
	wakeLimiter        *RateLimiter
}

// registerRequest is the JSON body downstream agents POST to /register on
// every heartbeat. This is a machine-to-machine endpoint (no browser/HTMX
// involvement), so it stays JSON.
type registerRequest struct {
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

// registerResponse mirrors agent.registerResponse's expected shape.
type registerResponse struct {
	HeartbeatInterval string `json:"heartbeatInterval"`
}

type wakeRequest struct {
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
}

func New(cfgMgr *config.Manager, registry *Registry, logger *appservice.Logger, logo config.LogoConfig) (*App, error) {
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

	requiredAssets := []string{
		"alpine.min.js",
		"blades.min.css",
		"app.css",
		"material-symbols.css",
		"material-symbols-outlined.woff2",
	}
	for _, requiredAsset := range requiredAssets {
		if _, err := fs.ReadFile(staticFS, requiredAsset); err != nil {
			return nil, fmt.Errorf("missing embedded asset %q: run `task assets:get` before build", requiredAsset)
		}
	}

	cfg := cfgMgr.Get()
	logoDataURI, faviconData, faviconContentType := resolveLogo(logo, staticFS, logger)
	app := &App{
		cfgMgr:             cfgMgr,
		logger:             logger,
		registry:           registry,
		staticFS:           staticFS,
		logoDataURI:        logoDataURI,
		faviconData:        faviconData,
		faviconContentType: faviconContentType,
		registerLimiter:    NewRateLimiter(60, time.Minute), // 10 registrations per minute per IP
		wakeLimiter:        NewRateLimiter(30, time.Minute), // 30 wake requests per minute per IP
	}

	app.telegram = telegram.New(cfg.Token, cfg.Users, app, logger, cfg.Whoami)
	return app, nil
}

// resolveLogo turns the configured logo into a src for the nav bar image,
// preferring an inline base64 payload over a static asset path, plus a
// favicon resized from the same source image (see generateFavicon) reused to
// serve /favicon.ico. A path refers to a filename already embedded under
// web/static (served at /static/), avoiding any dependence on the process's
// working directory. Any failure to resolve a usable image falls back to
// empty results so the caller shows the "wollee" text brand and skips the
// favicon link instead.
func resolveLogo(logo config.LogoConfig, staticFS fs.FS, logger *appservice.Logger) (dataURI string, faviconData []byte, faviconContentType string) {
	if b64 := strings.TrimSpace(logo.Base64); b64 != "" {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			logger.Error("decode logo.base64", err)
			return "", nil, ""
		}
		favData, favType := generateFavicon(data, logo.FaviconMode, logger)
		return buildDataURI(data), favData, favType
	}

	if path := strings.TrimSpace(logo.Path); path != "" {
		path = strings.TrimPrefix(path, "/")
		data, err := fs.ReadFile(staticFS, path)
		if err != nil {
			logger.Error("find logo.path in static assets, falling back to text brand", err, "path", path)
			return "", nil, ""
		}
		favData, favType := generateFavicon(data, logo.FaviconMode, logger)
		return "/static/" + path, favData, favType
	}

	return "", nil, ""
}

func buildDataURI(data []byte) string {
	mimeType := http.DetectContentType(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))
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
		!eqInt64Slices(oldCfg.Users, newCfg.Users) ||
		oldCfg.Whoami != newCfg.Whoami {
		// Stop the old telegram service
		a.telegramCancel()
		a.telegram.Shutdown()

		// Create a new telegram service with updated config
		a.telegram = telegram.New(newCfg.Token, newCfg.Users, a, a.logger, newCfg.Whoami)

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
