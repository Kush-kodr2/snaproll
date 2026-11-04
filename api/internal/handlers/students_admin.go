package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"attendance-api/internal/auth"
)

// DELETE /students/{id}/face-data  (admin)
// Removes a student's face templates and consent, e.g. when they lost their withdraw code.
// Attendance history is kept. The student can enroll again after a new invite code.
func (h *Handler) DeleteFaceData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin, _ := auth.FromContext(ctx)
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid student id")
		return
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	var found bool
	err = tx.QueryRow(ctx,
		`UPDATE students
		 SET consent_given_at = NULL, consent_version = NULL, withdraw_code_hash = NULL,
		     enroll_code_hash = NULL, enroll_code_expires_at = NULL
		 WHERE id = $1 RETURNING true`, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "student not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	tag, err := tx.Exec(ctx, `DELETE FROM face_embeddings WHERE student_id = $1`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (actor_id, action, target) VALUES ($1, $2, $3)`,
		admin.ID, "face_data_deleted_by_admin", fmt.Sprintf("student:%d", id)); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted_templates": tag.RowsAffected()})
}