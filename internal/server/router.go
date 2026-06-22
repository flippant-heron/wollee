package server

import "net/http"

func (a *App) newRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(a.staticFS))))
	mux.HandleFunc("/register", a.handleRegister)
	mux.HandleFunc("/wake", a.handleWake)
	mux.HandleFunc("/status", a.handleStatus)
	return mux
}
