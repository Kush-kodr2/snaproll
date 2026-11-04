package handlers

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"attendance-api/internal/auth"

	"attendance-api/internal/faceclient"
)

type Handler struct {
	DB       *pgxpool.Pool
	Secret   []byte
	TokenTTL time.Duration
	Face *faceclient.Client
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
	
	mux.HandleFunc("POST /enroll", h.Enroll)
	mux.HandleFunc("POST /enroll/withdraw", h.Withdraw)

	mux.Handle("GET /auth/me", protected(h.Me))
	mux.Handle("POST /auth/teachers", adminOnly(h.CreateTeacher))

	mux.Handle("GET /batches", protected(h.ListBatches))
	mux.Handle("POST /batches", adminOnly(h.CreateBatch))

	mux.Handle("GET /courses", protected(h.ListCourses))
	mux.Handle("POST /courses", adminOnly(h.CreateCourse))

	mux.Handle("POST /batches/{id}/invite-codes", adminOnly(h.CreateInviteCodes))
	mux.Handle("POST /tools/analyze", adminOnly(h.AnalyzeTool))

	mux.Handle("GET /offerings", protected(h.ListOfferings))
	mux.Handle("POST /offerings", adminOnly(h.CreateOffering))

	mux.Handle("GET /students", protected(h.ListStudents))
	mux.Handle("POST /students", adminOnly(h.CreateStudent))
	mux.Handle("POST /students/{id}/invite-code", adminOnly(h.CreateStudentInvite))
	
	mux.Handle("POST /batches/{id}/students/import", adminOnly(h.ImportStudents))
	mux.Handle("GET /sessions", protected(h.ListSessions))
	mux.Handle("POST /sessions", protected(h.CreateSession))
	mux.Handle("GET /sessions/{id}/attendance", protected(h.SessionAttendance))

	mux.HandleFunc("GET /enroll", servePage("web/enroll.html"))
	mux.HandleFunc("GET /withdraw", servePage("web/withdraw.html"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	
	mux.Handle("POST /sessions/{id}/photos", protected(h.SessionPhotos))
	mux.Handle("DELETE /students/{id}/face-data", adminOnly(h.DeleteFaceData))
	return mux
}