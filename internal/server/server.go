// Package server exposes Zima Connect's local web UI and JSON API on
// 127.0.0.1. This is the "localhost webpage where the user enters the Remote
// ID" from the product brief. The same server both renders the page and drives
// the ZeroTier backend, so opening the app is just opening this URL.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"

	"github.com/jirkacepelka/zimaos-on-linux/internal/autostart"
	"github.com/jirkacepelka/zimaos-on-linux/internal/config"
	"github.com/jirkacepelka/zimaos-on-linux/internal/zt"
)

//go:embed web
var webFS embed.FS

// BackendFactory builds a ZeroTier backend for a (possibly pinned) ZimaOS host.
type BackendFactory func(pinnedHost string) (zt.Backend, error)

// Server holds the running application state behind the HTTP handlers.
type Server struct {
	cfg     *config.Config
	execCmd string // command used for the autostart .desktop entry
	newB    BackendFactory

	mu      sync.Mutex
	backend zt.Backend
}

// New creates a Server. backend is the already-selected engine; execCmd is the
// command to register for login autostart; newB is used to rebuild the backend
// when the user pins a different ZimaOS host.
func New(cfg *config.Config, backend zt.Backend, execCmd string, newB BackendFactory) *Server {
	return &Server{cfg: cfg, backend: backend, execCmd: execCmd, newB: newB}
}

// Handler returns the HTTP handler for the whole UI + API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err) // embedded FS is a build-time constant; this cannot fail
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/autostart", s.handleAutostart)

	return mux
}

// AutoResume re-joins the saved network on startup so a background launch
// reconnects without user interaction.
func (s *Server) AutoResume(ctx context.Context) {
	if s.cfg.NetworkID == "" {
		return
	}
	s.mu.Lock()
	b := s.backend
	s.mu.Unlock()
	_ = b.Join(ctx, s.cfg.NetworkID)
}

// ---- API types -------------------------------------------------------------

type statusResponse struct {
	zt.Status
	NetworkID string `json:"configured_network_id"`
	ZimaHost  string `json:"configured_zima_host"`
	Autostart bool   `json:"autostart"`
	Backend   string `json:"backend"`
}

type connectRequest struct {
	NetworkID string `json:"network_id"`
	ZimaHost  string `json:"zima_host"`
	Autostart bool   `json:"autostart"`
}

// ---- handlers --------------------------------------------------------------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	b := s.backend
	s.mu.Unlock()

	resp := statusResponse{
		Status:    b.Status(r.Context()),
		NetworkID: s.cfg.NetworkID,
		ZimaHost:  s.cfg.ZimaHost,
		Autostart: s.cfg.Autostart,
		Backend:   b.Name(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req connectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	netID, err := config.NormalizeNetworkID(req.NetworkID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// If the user pinned a different ZimaOS host, rebuild the backend so the
	// new pin takes effect.
	hostChanged := req.ZimaHost != s.cfg.ZimaHost

	s.cfg.NetworkID = netID
	s.cfg.ZimaHost = req.ZimaHost
	s.cfg.Autostart = req.Autostart
	if err := s.cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save settings: "+err.Error())
		return
	}

	if err := autostart.Set(req.Autostart, s.execCmd); err != nil {
		// Non-fatal: connecting still works, autostart just did not stick.
		writeError(w, http.StatusInternalServerError, "connected settings saved but autostart failed: "+err.Error())
		return
	}

	s.mu.Lock()
	if hostChanged && s.newB != nil {
		if nb, err := s.newB(req.ZimaHost); err == nil {
			_ = s.backend.Close()
			s.backend = nb
		}
	}
	b := s.backend
	s.mu.Unlock()

	if err := b.Join(r.Context(), netID); err != nil {
		writeError(w, http.StatusBadGateway, "join failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "joining"})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	b := s.backend
	s.mu.Unlock()

	if err := b.Leave(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, "leave failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

func (s *Server) handleAutostart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := autostart.Set(req.Enabled, s.execCmd); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.cfg.Autostart = req.Enabled
	if err := s.cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"autostart": req.Enabled})
}

// ---- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
