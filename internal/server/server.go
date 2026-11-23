// Package server exposes the meal plan over HTTP: a JSON API behind a session
// cookie, plus the embedded single-page web app.
package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"chishenme/internal/store"
	"chishenme/web"
)

// Config holds the runtime settings the server needs.
type Config struct {
	// Secret signs session cookies.
	Secret []byte
	// Secure marks the session cookie as HTTPS-only. It follows the TLS setup.
	Secure bool
	// DeepSeekKey enables recipe suggestions when set. It never goes to the browser.
	DeepSeekKey string
	// DeepSeekURL is the API origin. Empty uses the public DeepSeek endpoint.
	DeepSeekURL string
}

// Server wires the database, the session and the routes together.
type Server struct {
	store *store.Store
	cfg   Config
	limit *throttle
}

// New builds the HTTP handler for the whole application.
func New(st *store.Store, cfg Config) (http.Handler, error) {
	if st == nil {
		return nil, errors.New("store must not be nil")
	}
	if len(cfg.Secret) < 16 {
		return nil, errors.New("session secret must be at least 16 characters")
	}
	static, err := newStatic(web.FS)
	if err != nil {
		return nil, err
	}
	s := &Server{store: st, cfg: cfg, limit: newThrottle()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.guard(s.me))
	mux.HandleFunc("PUT /api/me/password", s.guard(s.changePassword))

	mux.HandleFunc("GET /api/week", s.guard(s.week))
	mux.HandleFunc("POST /api/dishes", s.guard(s.createDish))
	mux.HandleFunc("PUT /api/dishes/{id}", s.guard(s.updateDish))
	mux.HandleFunc("DELETE /api/dishes/{id}", s.guard(s.deleteDish))

	mux.HandleFunc("GET /api/pantry", s.guard(s.pantry))
	mux.HandleFunc("POST /api/pantry", s.guard(s.addPantry))
	mux.HandleFunc("PUT /api/pantry/{id}", s.guard(s.updatePantry))
	mux.HandleFunc("DELETE /api/pantry/{id}", s.guard(s.deletePantry))

	mux.HandleFunc("POST /api/allocate", s.guard(s.allocate))
	mux.HandleFunc("POST /api/return", s.guard(s.returnToPantry))
	mux.HandleFunc("POST /api/ingredients/ready", s.guard(s.setReady))
	mux.HandleFunc("POST /api/recipes", s.guard(s.recipe))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "接口不存在")
	})
	mux.Handle("/", static)
	return mux, nil
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	var invalid store.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeErr(w, http.StatusBadRequest, invalid.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "记录不存在")
	case errors.Is(err, store.ErrBadCredentials):
		writeErr(w, http.StatusUnauthorized, "密码不正确")
	default:
		log.Printf("chishenme: %v", err)
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("chishenme: write response: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func decodeBody(w http.ResponseWriter, r *http.Request, dest any) bool {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		writeErr(w, http.StatusBadRequest, "请求内容无法读取")
		return false
	}
	return true
}
