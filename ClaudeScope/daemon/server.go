package daemon

import (
	"fmt"
	"net/http"
)

const DaemonAddr = "localhost:5812"

// NewServer builds the HTTP mux with all routes registered.
func NewServer(reg *Registry, factory NTSessionFactory) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", HandlePing)
	mux.HandleFunc("GET /sessions", HandleSessions(reg))
	mux.HandleFunc("POST /connect", HandleConnect(reg, factory))
	mux.HandleFunc("POST /load", HandleLoad(reg))
	mux.HandleFunc("POST /disconnect", HandleDisconnect(reg))
	mux.HandleFunc("GET /info", HandleInfo(reg))
	mux.HandleFunc("POST /get", HandleGet(reg))
	mux.HandleFunc("POST /range", HandleRange(reg))
	mux.HandleFunc("POST /find-bool", HandleFindBool(reg))
	mux.HandleFunc("POST /find-threshold", HandleFindThreshold(reg))
	mux.HandleFunc("POST /stats", HandleStats(reg))
	mux.HandleFunc("POST /set", HandleSet(reg))
	return mux
}

// Run starts the daemon HTTP server. Blocks until exit.
func Run(reg *Registry, factory NTSessionFactory) error {
	mux := NewServer(reg, factory)
	fmt.Printf("ClaudeScope daemon listening on %s\n", DaemonAddr)
	return http.ListenAndServe(DaemonAddr, mux)
}
