package server

import (
	"net/http"
	"strings"

	"github.com/flippant-heron/wollee/internal/auth"
)

const (
	// CookieName is the name of the authentication cookie
	CookieName = "wollee_token"
)

// AuthMiddleware returns a middleware that validates JWT tokens from cookies
func (a *App) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for token in cookie first
		cookie, err := r.Cookie(CookieName)
		if err == nil && cookie.Value != "" {
			if err := a.verifyToken(cookie.Value); err == nil {
				// Token is valid, proceed
				next.ServeHTTP(w, r)
				return
			}
		}

		// Check for token in Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				if err := a.verifyToken(parts[1]); err == nil {
					// Token is valid, proceed
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		// No valid token found, return 401
		a.writeError(w, http.StatusUnauthorized, "authentication required")
	})
}

// isAuthenticatedRequest is a helper that checks if the current request is authenticated
// It verifies the JWT token from either cookie or Authorization header
func (a *App) isAuthenticatedRequest(r *http.Request) bool {
	cfg := a.cfgMgr.Get()
	tm := auth.NewTokenManager(cfg.JWTSecret)

	// Check for token in cookie
	if cookie, err := r.Cookie(CookieName); err == nil && cookie.Value != "" {
		if err := tm.VerifyToken(cookie.Value); err == nil {
			return true
		}
	}

	// Check for token in Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "Bearer" {
			if err := tm.VerifyToken(parts[1]); err == nil {
				return true
			}
		}
	}

	return false
}
