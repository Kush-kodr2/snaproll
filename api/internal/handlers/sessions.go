package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"attendance-api/internal/auth"
)

var (
	errNotFound  = errors.New("not found")
	errForbidden = errors.New("forbidden")
)

type sessionDetail struct {
	ID           int64  `json:"id"`
	OfferingID   int64  `json:"offering_id"`
	SessionDate  string `json:"session_date"`
	Status       string `json:"status"`
	StudentCount int64  `json:"student_count"`
	CourseCode   string `json:"course_code"`
	CourseName   string `json:"course_name"`
	BatchName    string `json:"batch_name"`
}

// GET /sessions/{id}
func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.FromContext(ctx)
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if err := h.authorizeSession(ctx, p, id); err != nil {
		accessError(w, err)
		return
	}
	var s sessionDetail
	err := h.DB.QueryRow(ctx,
		`SELECT s.id, s.offering_id, s.session_date::text, s.status,
		        (SELECT count(*) FROM attendance a WHERE a.session_id = s.id),
		        c.code, c.name, b.name
		 FROM sessions s
		 JOIN offerings o ON o.id = s.offering_id
		 JOIN courses   c ON c.id = o.course_id
		 JOIN batches   b ON b.id = o.batch_id
		 WHERE s.id = $1`, id,
	).Scan(&s.ID, &s.OfferingID, &s.SessionDate, &s.Status, &s.StudentCount,
		&s.CourseCode, &s.CourseName, &s.BatchName)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// offeringBatch returns the offering's batch id if the user may use it:
// admins can use any offering, teachers only their own.
func (h *Handler) offeringBatch(ctx context.Context, p auth.Principal, offeringID int64) (int64, error) {
	var batchID, teacherID int64
	err := h.DB.QueryRow(ctx,
		`SELECT batch_id, teacher_id FROM offerings WHERE id = $1`, offeringID,
	).Scan(&batchID, &teacherID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errNotFound
	}
	if err != nil {
		return 0, err
	}
	if p.Role != "admin" && teacherID != p.ID {
		return 0, errForbidden
	}
	return batchID, nil
}

func (h *Handler) authorizeSession(ctx context.Context, p auth.Principal, sessionID int64) error {
	var offeringID int64
	err := h.DB.QueryRow(ctx,
		`SELECT offering_id FROM sessions WHERE id = $1`, sessionID).Scan(&offeringID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	_, err = h.offeringBatch(ctx, p, offeringID)
	return err
}

func accessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, errForbidden):
		writeError(w, http.StatusForbidden, "you don't have access to this")
	default:
		serverError(w, err)
	}
}

type sessionOut struct {
	ID           int64  `json:"id"`
	OfferingID   int64  `json:"offering_id"`
	SessionDate  string `json:"session_date"`
	Status       string `json:"status"`
	StudentCount int64  `json:"student_count"`
}

// POST /sessions  {"offering_id": 1, "session_date": "2026-10-19"}
func (h *Handler) CreateSession(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())

	var in struct {
		OfferingID  int64  `json:"offering_id"`
		SessionDate string `json:"session_date"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	date, err := time.Parse("2006-01-02", in.SessionDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "session_date must look like 2026-10-19")
		return
	}
	batchID, err := h.offeringBatch(r.Context(), p, in.OfferingID)
	if err != nil {
		accessError(w, err)
		return
	}

	// One transaction: create the session AND an "absent" row for every active
	// student in the batch. Either both happen or neither does.
	tx, err := h.DB.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context()) // harmless after a successful Commit

	var s sessionOut
	err = tx.QueryRow(r.Context(),
		`INSERT INTO sessions (offering_id, session_date) VALUES ($1, $2)
		 RETURNING id, offering_id, session_date::text, status`,
		in.OfferingID, date,
	).Scan(&s.ID, &s.OfferingID, &s.SessionDate, &s.Status)
	if err != nil {
		serverError(w, err)
		return
	}
	tag, err := tx.Exec(r.Context(),
		`INSERT INTO attendance (session_id, student_id)
		 SELECT $1, id FROM students WHERE batch_id = $2 AND is_active`,
		s.ID, batchID)
	if err != nil {
		serverError(w, err)
		return
	}
	s.StudentCount = tag.RowsAffected()

	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

// GET /sessions or GET /sessions?offering_id=1
func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	offeringID, ok := optionalQueryID(r, "offering_id")
	if !ok {
		writeError(w, http.StatusBadRequest, "offering_id must be a positive number")
		return
	}
	teacherFilter := p.ID
	if p.Role == "admin" {
		teacherFilter = 0
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT s.id, s.offering_id, s.session_date::text, s.status,
		        (SELECT count(*) FROM attendance a WHERE a.session_id = s.id)
		 FROM sessions s
		 JOIN offerings o ON o.id = s.offering_id
		 WHERE ($1::bigint = 0 OR o.teacher_id = $1)
		   AND ($2::bigint = 0 OR s.offering_id = $2)
		 ORDER BY s.session_date DESC, s.id DESC`,
		teacherFilter, offeringID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	out := []sessionOut{}
	for rows.Next() {
		var s sessionOut
		if err := rows.Scan(&s.ID, &s.OfferingID, &s.SessionDate, &s.Status, &s.StudentCount); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type attendanceOut struct {
	StudentID    int64    `json:"student_id"`
	EnrollmentNo string   `json:"enrollment_no"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Method       string   `json:"method"`
	Confidence   *float32 `json:"confidence"`
	Enrolled     bool     `json:"enrolled"`
}

// GET /sessions/{id}/attendance
func (h *Handler) SessionAttendance(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	sessionID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if err := h.authorizeSession(r.Context(), p, sessionID); err != nil {
		accessError(w, err)
		return
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT a.student_id, st.enrollment_no, st.name, a.status, a.method, a.confidence,
		        EXISTS (SELECT 1 FROM face_embeddings fe WHERE fe.student_id = a.student_id)
		 FROM attendance a
		 JOIN students st ON st.id = a.student_id
		 WHERE a.session_id = $1
		 ORDER BY st.enrollment_no`, sessionID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	out := []attendanceOut{}
	for rows.Next() {
		var a attendanceOut
		if err := rows.Scan(&a.StudentID, &a.EnrollmentNo, &a.Name,
			&a.Status, &a.Method, &a.Confidence, &a.Enrolled); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
