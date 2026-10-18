package handlers

import (
	"net/http"

	"attendance-api/internal/auth"
)

type offeringInput struct {
	CourseID  int64 `json:"course_id"`
	TeacherID int64 `json:"teacher_id"`
	BatchID   int64 `json:"batch_id"`
}

type offeringOut struct {
	ID          int64  `json:"id"`
	CourseID    int64  `json:"course_id"`
	CourseCode  string `json:"course_code"`
	CourseName  string `json:"course_name"`
	TeacherID   int64  `json:"teacher_id"`
	TeacherName string `json:"teacher_name"`
	BatchID     int64  `json:"batch_id"`
	BatchName   string `json:"batch_name"`
}

// anything with a Scan method: a single row or one row of a result set
type scanner interface {
	Scan(dest ...any) error
}

func scanOffering(row scanner, o *offeringOut) error {
	return row.Scan(&o.ID, &o.CourseID, &o.CourseCode, &o.CourseName,
		&o.TeacherID, &o.TeacherName, &o.BatchID, &o.BatchName)
}

func (h *Handler) CreateOffering(w http.ResponseWriter, r *http.Request) {
	var in offeringInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.CourseID < 1 || in.TeacherID < 1 || in.BatchID < 1 {
		writeError(w, http.StatusUnprocessableEntity, "course_id, teacher_id and batch_id are required")
		return
	}

	// The CTE inserts the row, then the SELECT joins in the names for the response.
	var o offeringOut
	err := scanOffering(h.DB.QueryRow(r.Context(),
		`WITH o AS (
		     INSERT INTO offerings (course_id, teacher_id, batch_id)
		     VALUES ($1, $2, $3)
		     RETURNING id, course_id, teacher_id, batch_id
		 )
		 SELECT o.id, c.id, c.code, c.name, t.id, t.name, b.id, b.name
		 FROM o
		 JOIN courses  c ON c.id = o.course_id
		 JOIN teachers t ON t.id = o.teacher_id
		 JOIN batches  b ON b.id = o.batch_id`,
		in.CourseID, in.TeacherID, in.BatchID), &o)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "this offering already exists")
		return
	}
	if isForeignKeyViolation(err) {
		writeError(w, http.StatusUnprocessableEntity, "course, teacher or batch does not exist")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

// Admins see every offering; teachers see only their own.
func (h *Handler) ListOfferings(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	teacherFilter := p.ID
	if p.Role == "admin" {
		teacherFilter = 0 // 0 means "no filter"
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT o.id, c.id, c.code, c.name, t.id, t.name, b.id, b.name
		 FROM offerings o
		 JOIN courses  c ON c.id = o.course_id
		 JOIN teachers t ON t.id = o.teacher_id
		 JOIN batches  b ON b.id = o.batch_id
		 WHERE ($1::bigint = 0 OR o.teacher_id = $1)
		 ORDER BY c.code, b.name`, teacherFilter)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	out := []offeringOut{}
	for rows.Next() {
		var o offeringOut
		if err := scanOffering(rows, &o); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}