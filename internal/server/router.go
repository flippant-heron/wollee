package server

import (
	"net/http"
	"strings"

	"github.com/flippant-heron/wollee/internal/auth"
)

func (a *App) newRouter() http.Handler {
	return &routerMiddleware{app: a, mux: a.createMux()}
}

type routerMiddleware struct {
	app *App
	mux *http.ServeMux
}

func (rm *routerMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if the path requires authentication
	if rm.isProtectedPath(r.URL.Path) && r.URL.Path != "/" {
		// Try to authenticate
		if !rm.isAuthenticated(r) {
			// Not authenticated - serve login page
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := w.Write(rm.app.loginHTML); err != nil {
				rm.app.logger.Error("write login response", err)
			}
			return
		}
		// Authenticated, allow access
		rm.mux.ServeHTTP(w, r)
	} else if r.URL.Path == "/" {
		// Root path requires authentication
		if !rm.isAuthenticated(r) {
			// Not authenticated - serve login page
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := w.Write(rm.app.loginHTML); err != nil {
				rm.app.logger.Error("write login response", err)
			}
			return
		}
		// Authenticated, allow access to index
		rm.mux.ServeHTTP(w, r)
	} else {
		// Public path, always allow
		rm.mux.ServeHTTP(w, r)
	}
}

func (rm *routerMiddleware) isProtectedPath(path string) bool {
	protectedPrefixes := []string{
		"/add-host",
		"/settings",
		"/wake",
		"/status",
		"/config/reload",
		"/hosts",
		"/api/settings",
	}
	for _, prefix := range protectedPrefixes {
		if path == prefix || (prefix[len(prefix)-1] == '/' && len(path) > len(prefix) && path[:len(prefix)] == prefix) {
			return true
		}
	}
	return false
}

func (rm *routerMiddleware) isAuthenticated(r *http.Request) bool {
	cfg := rm.app.cfgMgr.Get()
	tokenManager := auth.NewTokenManager(cfg.JWTSecret)

	// Check for token in cookie
	if cookie, err := r.Cookie(CookieName); err == nil && cookie.Value != "" {
		if err := tokenManager.VerifyToken(cookie.Value); err == nil {
			return true
		}
	}

	// Check for token in Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := authHeader[7:]
			if err := tokenManager.VerifyToken(token); err == nil {
				return true
			}
		}
	}

	return false
}

func (a *App) verifyToken(token string) error {
	cfg := a.cfgMgr.Get()
	tokenManager := auth.NewTokenManager(cfg.JWTSecret)
	return tokenManager.VerifyToken(token)
}

func (a *App) createMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Public endpoints (no auth required)
	mux.HandleFunc("/auth/status", a.handleAuthStatus)
	mux.HandleFunc("/auth/login", a.handleLogin)
	mux.HandleFunc("/auth/setup", a.handleSetupPassword)
	mux.HandleFunc("/auth/logout", a.handleLogout)

	// Protected auth endpoints
	mux.HandleFunc("/auth/change-password", a.handleChangePassword)

	// Rate-limited public endpoints (agent heartbeat)
	mux.Handle("/register", a.RateLimitMiddleware(a.registerLimiter)(http.HandlerFunc(a.handleRegister)))

	// Protected endpoints (auth required)
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/add-host", a.handleAddHostPage)
	mux.HandleFunc("/settings", a.handleSettingsPage)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(a.staticFS))))
	mux.Handle("/wake", a.RateLimitMiddleware(a.wakeLimiter)(http.HandlerFunc(a.handleWake)))
	mux.HandleFunc("/status", a.handleStatus)
	mux.HandleFunc("/config/reload", a.handleConfigReload)
	mux.HandleFunc("/hosts", a.handleAddHost)
	mux.HandleFunc("DELETE /hosts/{mac}", a.handleDeleteHost)
	mux.HandleFunc("PATCH /hosts/{mac}/disable", a.handleDisableHost)
	mux.HandleFunc("PATCH /hosts/{mac}/enable", a.handleEnableHost)
	mux.HandleFunc("/api/settings", a.handleSettings)

	return mux
}
