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
	cfg        config.ServerConfig
	logger     *appservice.Logger
	registry   *Registry
	httpSrv    *http.Server
	telegram   *telegram.Service
	staticFS   fs.FS
	indexHTML  []byte
	addHostHTML []byte
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

	addHostHTML, err := webassets.Assets.ReadFile("add-host.html")
	if err != nil {
		return nil, fmt.Errorf("read add-host.html: %w", err)
	}

	for _, requiredAsset := range []string{"alpine.min.js", "pico.min.css"} {
		if _, err := fs.ReadFile(staticFS, requiredAsset); err != nil {
			return nil, fmt.Errorf("missing embedded asset %q: run `task assets:dl` before build", requiredAsset)
		}
	}

	app := &App{
		cfg:        cfg,
		logger:     logger,
		registry:   registry,
		staticFS:   staticFS,
		indexHTML:  indexHTML,
		addHostHTML: addHostHTML,
	}

	app.telegram = telegram.New(cfg.TelegramToken, cfg.AllowedTelegramUsers, app, logger)
	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	a.httpSrv = &http.Server{
		Addr:              fmt.Sprintf(":%d", a.cfg.Port),
		Handler:           a.newRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := a.telegram.Start(ctx); err != nil {
		return err
	}

	a.logger.Info("starting server", "port", a.cfg.Port, "broadcast", a.cfg.SubnetBroadcast)
	go a.shutdownOnContext(ctx)

	if err := a.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
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
