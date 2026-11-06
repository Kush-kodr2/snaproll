package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"attendance-api/internal/auth"
)

type reviewItem struct {
	StudentID    int64   `json:"student_id"`
	EnrollmentNo string  `json:"enrollment_no"`
	Name         string  `json:"name"`
	Score        float32 `json:"score"`
	Margin       float32 `json:"margin"`
	FaceWidth    int     `json:"face_width"`
	Photo        int     `json:"photo"`
	CropURL      string  `json:"crop_url"`
}

// GET /sessions/{id}/review  (students waiting for the teacher's decision)
func (h *Handler) SessionReview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.FromContext(ctx)
	sessionID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if err := h.authorizeSession(ctx, p, sessionID); err != nil {
		accessError(w, err)
		return
	}

	rows, err := h.DB.Query(ctx,
		`SELECT rf.student_id, st.enrollment_no, st.name, rf.score, rf.margin, rf.face_width, rf.photo_no
		 FROM review_faces rf JOIN students st ON st.id = rf.student_id
		 WHERE rf.session_id = $1
		 ORDER BY rf.score DESC`, sessionID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	out := []reviewItem{}
	for rows.Next() {
		var it reviewItem
		if err := rows.Scan(&it.StudentID, &it.EnrollmentNo, &it.Name,
			&it.Score, &it.Margin, &it.FaceWidth, &it.Photo); err != nil {
			serverError(w, err)
			return
		}
		it.CropURL = fmt.Sprintf("/sessions/%d/review/%d/crop", sessionID, it.StudentID)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// GET /sessions/{id}/review/{student_id}/crop  (the thumbnail, as a JPEG)
func (h *Handler) ReviewCrop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.FromContext(ctx)
	sessionID, ok1 := pathID(r)
	studentID, ok2 := pathIDNamed(r, "student_id")
	if !ok1 || !ok2 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.authorizeSession(ctx, p, sessionID); err != nil {
		accessError(w, err)
		return
	}

	var crop []byte
	err := h.DB.QueryRow(ctx,
		`SELECT crop FROM review_faces WHERE session_id = $1 AND student_id = $2`,
		sessionID, studentID).Scan(&crop)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "no thumbnail for this student")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(crop)
}

// PUT /sessions/{id}/attendance/{student_id}   {"status": "present" | "absent"}
// A manual decision. Later photo uploads never overwrite it.
func (h *Handler) SetAttendance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.FromContext(ctx)
	sessionID, ok1 := pathID(r)
	studentID, ok2 := pathIDNamed(r, "student_id")
	if !ok1 || !ok2 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Status != "present" && in.Status != "absent" {
		writeError(w, http.StatusUnprocessableEntity, "status must be present or absent")
		return
	}
	if err := h.authorizeSession(ctx, p, sessionID); err != nil {
		accessError(w, err)
		return
	}

	var sessionStatus string
	if err := h.DB.QueryRow(ctx,
		`SELECT status FROM sessions WHERE id = $1`, sessionID).Scan(&sessionStatus); err != nil {
		serverError(w, err)
		return
	}
	if sessionStatus == "closed" {
		writeError(w, http.StatusConflict, "this session is closed")
		return
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	var updated int64
	err = tx.QueryRow(ctx,
		`UPDATE attendance
		 SET status = $3, method = 'manual', reviewed_by = $4, updated_at = now()
		 WHERE session_id = $1 AND student_id = $2
		 RETURNING student_id`,
		sessionID, studentID, in.Status, p.ID).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "this student is not in this session")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM review_faces WHERE session_id = $1 AND student_id = $2`,
		sessionID, studentID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"student_id": studentID, "status": in.Status, "method": "manual"})
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func attendanceSummary(ctx context.Context, q queryer, sessionID int64) (map[string]int64, error) {
	out := map[string]int64{"present": 0, "needs_review": 0, "absent": 0}
	rows, err := q.Query(ctx,
		`SELECT status, count(*) FROM attendance WHERE session_id = $1 GROUP BY status`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var c int64
		if err := rows.Scan(&s, &c); err != nil {
			return nil, err
		}
		out[s] = c
	}
	return out, rows.Err()
}

// POST /sessions/{id}/close[?force=true]
// Refuses while students still need review. With force=true they are marked absent.
// Closing deletes the session's thumbnails.
func (h *Handler) CloseSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.FromContext(ctx)
	sessionID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if err := h.authorizeSession(ctx, p, sessionID); err != nil {
		accessError(w, err)
		return
	}
	force := r.URL.Query().Get("force") == "true"

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	var pending int64
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM attendance WHERE session_id = $1 AND status = 'needs_review'`,
		sessionID).Scan(&pending); err != nil {
		serverError(w, err)
		return
	}
	if pending > 0 {
		if !force {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "some students still need review", "pending": pending})
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE attendance SET status = 'absent', method = 'manual', reviewed_by = $2, updated_at = now()
			 WHERE session_id = $1 AND status = 'needs_review'`, sessionID, p.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET status = 'closed' WHERE id = $1`, sessionID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM review_faces WHERE session_id = $1`, sessionID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}

	summary, err := attendanceSummary(ctx, h.DB, sessionID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "closed", "summary": summary})
}