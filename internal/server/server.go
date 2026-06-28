package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	once "github.com/Baagheera/once/internal/once"
)

type Handler struct {
	store     once.Store
	authToken string
}

type Options struct {
	AuthToken string
}

func NewHandler(store once.Store, options Options) http.Handler {
	h := &Handler{
		store:     store,
		authToken: options.AuthToken,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("POST /v1/reserve", h.reserve)
	mux.HandleFunc("POST /v1/commit", h.commit)
	mux.HandleFunc("GET /v1/records/{key}", h.get)
	mux.HandleFunc("DELETE /v1/records/{key}", h.forget)
	return h.auth(mux)
}

type reserveRequest struct {
	Key     string   `json:"key"`
	Command []string `json:"command,omitempty"`
}

type reserveResponse struct {
	Fresh        bool        `json:"fresh"`
	AttemptToken string      `json:"attempt_token,omitempty"`
	Record       recordModel `json:"record"`
}

type commitRequest struct {
	Key          string     `json:"key"`
	AttemptToken string     `json:"attempt_token"`
	State        once.State `json:"state"`
	ExitCode     int        `json:"exit_code"`
	StdoutB64    []byte     `json:"stdout_b64,omitempty"`
	StderrB64    []byte     `json:"stderr_b64,omitempty"`
	Error        string     `json:"error,omitempty"`
}

type recordModel struct {
	Key       string     `json:"key"`
	State     once.State `json:"state"`
	ExitCode  int        `json:"exit_code"`
	StdoutB64 []byte     `json:"stdout_b64,omitempty"`
	StderrB64 []byte     `json:"stderr_b64,omitempty"`
	Error     string     `json:"error,omitempty"`
	Command   []string   `json:"command,omitempty"`
	StartedAt time.Time  `json:"started_at"`

	FinishedAt *time.Time `json:"finished_at,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type errorModel struct {
	Error string `json:"error"`
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) reserve(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req reserveRequest
	if !readJSON(w, r, &req) {
		return
	}
	key, ok := validKey(w, req.Key)
	if !ok {
		return
	}

	rec, fresh, err := h.store.Reserve(key, req.Command)
	if errors.Is(err, once.ErrConflict) {
		writeError(w, http.StatusConflict, "key already exists with a different command")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}

	resp := reserveResponse{
		Fresh:  fresh,
		Record: modelRecord(rec),
	}
	if fresh {
		resp.AttemptToken = rec.Attempt
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) commit(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req commitRequest
	if !readJSON(w, r, &req) {
		return
	}
	key, ok := validKey(w, req.Key)
	if !ok {
		return
	}

	rec, err := h.store.Commit(key, req.AttemptToken, req.State, req.ExitCode, req.StdoutB64, req.StderrB64, req.Error)
	if errors.Is(err, once.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if errors.Is(err, once.ErrConflict) {
		writeError(w, http.StatusConflict, "commit conflicts with existing record")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid commit")
		return
	}

	writeJSON(w, http.StatusOK, modelRecord(rec))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	key, ok := validKey(w, r.PathValue("key"))
	if !ok {
		return
	}

	rec, err := h.store.Get(key)
	if errors.Is(err, once.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}

	writeJSON(w, http.StatusOK, modelRecord(rec))
}

func (h *Handler) forget(w http.ResponseWriter, r *http.Request) {
	key, ok := validKey(w, r.PathValue("key"))
	if !ok {
		return
	}

	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	attempt := r.Header.Get("X-Once-Attempt-Token")
	if attempt == "" {
		writeError(w, http.StatusBadRequest, "delete requires X-Once-Attempt-Token")
		return
	}
	deleted, err := h.store.Forget(key, force, attempt)
	if errors.Is(err, once.ErrRunning) {
		writeError(w, http.StatusConflict, "key is still running")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid delete")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorModel{Error: msg})
}

func writeInternal(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal error")
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return false
	}
	return true
}

func validKey(w http.ResponseWriter, key string) (string, bool) {
	key = strings.TrimSpace(key)
	if err := once.ValidateKey(key); err != nil {
		writeError(w, http.StatusBadRequest, "invalid key")
		return "", false
	}
	return key, true
}

func (h *Handler) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if h.authToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token := strings.TrimPrefix(header, prefix)
		if subtle.ConstantTimeCompare([]byte(token), []byte(h.authToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func modelRecord(rec once.Record) recordModel {
	return recordModel{
		Key:        rec.Key,
		State:      rec.State,
		ExitCode:   rec.ExitCode,
		StdoutB64:  rec.Stdout,
		StderrB64:  rec.Stderr,
		Error:      rec.Error,
		Command:    rec.Command,
		StartedAt:  rec.StartedAt,
		FinishedAt: rec.FinishedAt,
		UpdatedAt:  rec.UpdatedAt,
	}
}
