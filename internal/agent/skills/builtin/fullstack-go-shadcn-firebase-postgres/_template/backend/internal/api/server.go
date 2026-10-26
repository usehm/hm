// Package api serves the JSON API the admin UI uses.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"__MODULE__/backend/internal/auth"
	"__MODULE__/backend/internal/db/sqlc"
)

// Server holds the API's dependencies.
type Server struct {
	q      *sqlc.Queries
	auth   *auth.Verifier
	origin string
	log    *slog.Logger
}

// New returns the API.
func New(q *sqlc.Queries, v *auth.Verifier, corsOrigin string, log *slog.Logger) *Server {
	return &Server{q: q, auth: v, origin: corsOrigin, log: log}
}

// Routes is the HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /api/me", s.authed(s.me))
	mux.Handle("GET /api/items", s.authed(s.listItems))
	mux.Handle("POST /api/items", s.authed(s.createItem))
	mux.Handle("PATCH /api/items/{id}", s.authed(s.updateItem))
	mux.Handle("DELETE /api/items/{id}", s.authed(s.deleteItem))
	return s.cors(mux)
}

type userKey struct{}

// authed verifies the Firebase ID token and records the user.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, sqlc.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.auth.Verify(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "sign in again")
			return
		}
		user, err := s.q.UpsertUser(r.Context(), sqlc.UpsertUserParams{FirebaseUid: u.UID, Email: u.Email})
		if err != nil {
			s.fail(w, err)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)), user)
	})
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, u sqlc.User) {
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request, u sqlc.User) {
	items, err := s.q.ListItems(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if items == nil {
		items = []sqlc.Item{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createItem(w http.ResponseWriter, r *http.Request, u sqlc.User) {
	var in struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	item, err := s.q.CreateItem(r.Context(), sqlc.CreateItemParams{OwnerID: u.ID, Title: in.Title})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateItem(w http.ResponseWriter, r *http.Request, u sqlc.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	var in struct {
		Done bool `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad body")
		return
	}
	item, err := s.q.SetItemDone(r.Context(), sqlc.SetItemDoneParams{ID: id, OwnerID: u.ID, Done: in.Done})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "no such item")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request, u sqlc.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	n, err := s.q.DeleteItem(r.Context(), sqlc.DeleteItemParams{ID: id, OwnerID: u.ID})
	if err != nil {
		s.fail(w, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "no such item")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.origin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "something went wrong")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
