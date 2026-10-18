package handlers

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"attendance-api/internal/auth"
)

type Handler struct {
	DB       *pgxpool.Pool
	Secret   []byte
	TokenTTL time.Duration
}

func Routes(h *Handler) http.Handler {
	mux := http.NewServeMux()
	authed := auth.Authenticated(h.Secret)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /auth/bootstrap", h.Bootstrap)
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.Handle("GET /auth/me", authed(http.HandlerFunc(h.Me)))
	mux.Handle("POST /auth/teachers", authed(auth.AdminOnly(http.HandlerFunc(h.CreateTeacher))))

	mux.Handle("GET /batches", authed(http.HandlerFunc(h.ListBatches)))
	mux.Handle("POST /batches", authed(auth.AdminOnly(http.HandlerFunc(h.CreateBatch))))

	return mux
}