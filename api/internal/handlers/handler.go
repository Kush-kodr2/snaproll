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

	// any logged-in user / admins only
	protected := func(f http.HandlerFunc) http.Handler { return authed(f) }
	adminOnly := func(f http.HandlerFunc) http.Handler { return authed(auth.AdminOnly(f)) }

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /auth/bootstrap", h.Bootstrap)
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.Handle("GET /auth/me", protected(h.Me))
	mux.Handle("POST /auth/teachers", adminOnly(h.CreateTeacher))

	mux.Handle("GET /batches", protected(h.ListBatches))
	mux.Handle("POST /batches", adminOnly(h.CreateBatch))

	mux.Handle("GET /courses", protected(h.ListCourses))
	mux.Handle("POST /courses", adminOnly(h.CreateCourse))

	mux.Handle("GET /offerings", protected(h.ListOfferings))
	mux.Handle("POST /offerings", adminOnly(h.CreateOffering))

	mux.Handle("GET /students", protected(h.ListStudents))
	mux.Handle("POST /students", adminOnly(h.CreateStudent))

	mux.Handle("GET /sessions", protected(h.ListSessions))
	mux.Handle("POST /sessions", protected(h.CreateSession))
	mux.Handle("GET /sessions/{id}/attendance", protected(h.SessionAttendance))

	return mux
}