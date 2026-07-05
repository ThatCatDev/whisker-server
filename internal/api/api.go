// Package api exposes the boards REST API consumed by the dashboard.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"whisker-server/internal/auth"
	"whisker-server/internal/store"
)

type API struct {
	store store.Store
	auth  *auth.Auth
	log   *slog.Logger
}

func New(st store.Store, a *auth.Auth, log *slog.Logger) *API {
	return &API{store: st, auth: a, log: log}
}

// Register mounts the routes on a Go 1.22+ pattern mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/boards", a.withUser(a.listBoards))
	mux.HandleFunc("POST /api/boards", a.withUser(a.createBoard))
	mux.HandleFunc("PATCH /api/boards/{id}", a.withUser(a.renameBoard))
	mux.HandleFunc("DELETE /api/boards/{id}", a.withUser(a.deleteBoard))
}

func (a *API) withUser(next func(w http.ResponseWriter, r *http.Request, userID string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.auth.UserID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r, userID)
	}
}

func (a *API) listBoards(w http.ResponseWriter, r *http.Request, userID string) {
	boards, err := a.store.ListBoards(r.Context(), userID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, boards)
}

func (a *API) createBoard(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	board, err := a.store.CreateBoard(r.Context(), userID, body.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, board)
}

func (a *API) renameBoard(w http.ResponseWriter, r *http.Request, userID string) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	a.finish(w, a.store.RenameBoard(r.Context(), r.PathValue("id"), userID, body.Name))
}

func (a *API) deleteBoard(w http.ResponseWriter, r *http.Request, userID string) {
	a.finish(w, a.store.DeleteBoard(r.Context(), r.PathValue("id"), userID))
}

func (a *API) finish(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, store.ErrForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		a.fail(w, err)
	}
}

func (a *API) fail(w http.ResponseWriter, err error) {
	a.log.Error("api error", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
