package handlers

import (
	"net/http"
	"net/mail"
	"strings"
)

type studentInput struct {
	EnrollmentNo string  `json:"enrollment_no"`
	Name         string  `json:"name"`
	Email        *string `json:"email"`
	BatchID      int64   `json:"batch_id"`
}

type studentOut struct {
	ID             int64   `json:"id"`
	EnrollmentNo   string  `json:"enrollment_no"`
	Name           string  `json:"name"`
	Email          *string `json:"email"`
	BatchID        int64   `json:"batch_id"`
	IsActive       bool    `json:"is_active"`
	ConsentGiven   bool    `json:"consent_given"`
	EnrolledPhotos int64   `json:"enrolled_photos"`
}

func (h *Handler) CreateStudent(w http.ResponseWriter, r *http.Request) {
	var in studentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.EnrollmentNo = strings.ToUpper(strings.TrimSpace(in.EnrollmentNo))
	in.Name = strings.TrimSpace(in.Name)
	if in.EnrollmentNo == "" || len(in.EnrollmentNo) > 50 {
		writeError(w, http.StatusUnprocessableEntity, "enrollment_no is required (max 50 characters)")
		return
	}
	if in.Name == "" || len(in.Name) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "name is required (max 100 characters)")
		return
	}
	if in.BatchID < 1 {
		writeError(w, http.StatusUnprocessableEntity, "batch_id is required")
		return
	}
	if in.Email != nil {
		e := strings.ToLower(strings.TrimSpace(*in.Email))
		if e == "" {
			in.Email = nil
		} else {
			if addr, err := mail.ParseAddress(e); err != nil || addr.Address != e {
				writeError(w, http.StatusUnprocessableEntity, "invalid email")
				return
			}
			in.Email = &e
		}
	}

	var s studentOut
	err := h.DB.QueryRow(r.Context(),
		`INSERT INTO students (enrollment_no, name, email, batch_id)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, enrollment_no, name, email, batch_id, is_active,
		           consent_given_at IS NOT NULL, 0::bigint`,
		in.EnrollmentNo, in.Name, in.Email, in.BatchID,
	).Scan(&s.ID, &s.EnrollmentNo, &s.Name, &s.Email, &s.BatchID,
		&s.IsActive, &s.ConsentGiven, &s.EnrolledPhotos)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "enrollment number already exists")
		return
	}
	if isForeignKeyViolation(err) {
		writeError(w, http.StatusUnprocessableEntity, "batch does not exist")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

// GET /students or GET /students?batch_id=1
func (h *Handler) ListStudents(w http.ResponseWriter, r *http.Request) {
	batchID, ok := optionalQueryID(r, "batch_id")
	if !ok {
		writeError(w, http.StatusBadRequest, "batch_id must be a positive number")
		return
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT s.id, s.enrollment_no, s.name, s.email, s.batch_id, s.is_active,
		        s.consent_given_at IS NOT NULL,
		        (SELECT count(*) FROM face_embeddings fe WHERE fe.student_id = s.id)
		 FROM students s
		 WHERE ($1::bigint = 0 OR s.batch_id = $1)
		 ORDER BY s.enrollment_no`, batchID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	out := []studentOut{}
	for rows.Next() {
		var s studentOut
		if err := rows.Scan(&s.ID, &s.EnrollmentNo, &s.Name, &s.Email, &s.BatchID,
			&s.IsActive, &s.ConsentGiven, &s.EnrolledPhotos); err != nil {
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