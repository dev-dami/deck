package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/web"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// CSP and the other headers of §3.6.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// Handler is the HTTP surface: health, pairing, the socket and the client.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("POST /api/v1/pair", s.handlePair)
	mux.HandleFunc("GET /api/v1/ws", s.handleWS)
	mux.HandleFunc("GET /{path...}", s.handleStatic)
	return s.secure(mux)
}

// secure rejects a Host that is not ours (DNS rebinding) and sets the
// headers every response carries.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wire.Health{OK: true, Protocol: wire.Protocol})
}

// handlePair is POST /api/v1/pair (§3.5, R7, R17): same origin, JSON,
// 4 KiB, the per-source lockout consulted first since the code is short.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, wire.PairError{Error: wire.CodeForbidden})
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		writeJSON(w, http.StatusForbidden, wire.PairError{Error: wire.CodeForbidden})
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, wire.PairError{Error: wire.CodeInvalid})
		return
	}
	addr := clientAddr(r)
	if wait, locked := s.limits.Locked(addr, ""); locked {
		w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(wait)))
		writeJSON(w, http.StatusTooManyRequests, wire.PairError{Error: wire.CodeRateLimited})
		return
	}
	var req wire.PairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, wire.MaxPairBodyBytes)).Decode(&req); err != nil || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, wire.PairError{Error: wire.CodeInvalid})
		return
	}
	token, dev, err := s.store.Pair(req.Code, req.DeviceName)
	switch {
	case errors.Is(err, auth.ErrInvalidCode):
		s.limits.Fail(addr, "")
		writeJSON(w, http.StatusBadRequest, wire.PairError{Error: wire.CodeUnauthorized})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, wire.PairError{Error: wire.CodeInternal})
	default:
		s.limits.OK(addr, "")
		writeJSON(w, http.StatusOK, wire.PairResponse{Token: token, DeviceID: dev.ID, ServerID: s.store.ServerID(), ExpiresAt: dev.ExpiresAt()})
		s.refreshDevices()
	}
}

func retrySeconds(d time.Duration) int {
	n := int(d / time.Second)
	if d%time.Second != 0 {
		n++
	}
	if n < 1 {
		n = 1
	}
	return n
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	fsys := s.opts.Static
	if fsys == nil {
		fsys = web.FS()
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if strings.HasPrefix(name, "api/") || name == "ws" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil && path.Ext(name) == "" {
		// A client-side route: the app shell handles it.
		name = "index.html"
		data, err = fs.ReadFile(fsys, name)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if name == "index.html" || name == "sw.js" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
