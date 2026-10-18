package handlers

import (
	"net/http"
	"strings"
)

type courseInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type courseOut struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

func (h *Handler) CreateCourse(w http.ResponseWriter, r *http.Request) {
	var in courseInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	if in.Code == "" || len(in.Code) > 30 {
		writeError(w, http.StatusUnprocessableEntity, "code is required (max 30 characters)")
		return
	}
	if in.Name == "" || len(in.Name) > 150 {
		writeError(w, http.StatusUnprocessableEntity, "name is required (max 150 characters)")
		return
	}

	var c courseOut
	err := h.DB.QueryRow(r.Context(),
		`INSERT INTO courses (code, name) VALUES ($1, $2) RETURNING id, code, name`,
		in.Code, in.Name,
	).Scan(&c.ID, &c.Code, &c.Name)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "course code already exists")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) ListCourses(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(r.Context(), `SELECT id, code, name FROM courses ORDER BY code`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	out := []courseOut{}
	for rows.Next() {
		var c courseOut
		if err := rows.Scan(&c.ID, &c.Code, &c.Name); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}