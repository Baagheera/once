package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	once "github.com/Baagheera/once/internal/once"
)

type Handler struct {
	store once.Store
}

func NewHandler(store once.Store) http.Handler {
	h := &Handler{store: store}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("POST /v1/reserve", h.reserve)
	mux.HandleFunc("POST /v1/commit", h.commit)
	mux.HandleFunc("GET /v1/records/", h.get)
	mux.HandleFunc("DELETE /v1/records/", h.forget)
	return mux
}

type reserveRequest struct {
	Key     string   `json:"key"`
	Command []string `json:"command,omitempty"`
}

type reserveResponse struct {
	Fresh  bool        `json:"fresh"`
	Record recordModel `json:"record"`
}

type commitRequest struct {
	Key       string     `json:"key"`
	State     once.State `json:"state"`
	ExitCode  int        `json:"exit_code"`
	StdoutB64 []byte     `json:"stdout_b64,omitempty"`
	StderrB64 []byte     `json:"stderr_b64,omitempty"`
	Error     string     `json:"error,omitempty"`
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
	var req reserveRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "missing key")
		return
	}

	rec, fresh, err := h.store.Reserve(req.Key, req.Command)
	if errors.Is(err, once.ErrConflict) {
		writeError(w, http.StatusConflict, "key already exists with a different command")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, reserveResponse{
		Fresh:  fresh,
		Record: modelRecord(rec),
	})
}

func (h *Handler) commit(w http.ResponseWriter, r *http.Request) {
	var req commitRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "missing key")
		return
	}

	rec, err := h.store.Commit(req.Key, req.State, req.ExitCode, req.StdoutB64, req.StderrB64, req.Error)
	if errors.Is(err, once.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if errors.Is(err, once.ErrConflict) {
		writeError(w, http.StatusConflict, "commit conflicts with existing record")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, modelRecord(rec))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	key := recordKey(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing key")
		return
	}

	rec, err := h.store.Get(key)
	if errors.Is(err, once.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, modelRecord(rec))
}

func (h *Handler) forget(w http.ResponseWriter, r *http.Request) {
	key := recordKey(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing key")
		return
	}

	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	ok, err := h.store.Forget(key, force)
	if errors.Is(err, once.ErrRunning) {
		writeError(w, http.StatusConflict, "key is still running")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func recordKey(r *http.Request) string {
	return strings.TrimPrefix(r.URL.Path, "/v1/records/")
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid json: %v", err))
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
