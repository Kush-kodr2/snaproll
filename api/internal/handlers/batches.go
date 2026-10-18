package handlers

import (
	"net/http"
	"strings"
)

type batchInput struct {
	Name    string  `json:"name"`
	Year    int     `json:"year"`
	Section *string `json:"section"`
}

type batchOut struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Year    int     `json:"year"`
	Section *string `json:"section"`
}

func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) {
	var in batchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "name is required (max 100 characters)")
		return
	}
	if in.Year < 1 || in.Year > 6 {
		writeError(w, http.StatusUnprocessableEntity, "year must be between 1 and 6")
		return
	}
	if in.Section != nil && len(*in.Section) > 10 {
		writeError(w, http.StatusUnprocessableEntity, "section is too long (max 10)")
		return
	}

	var b batchOut
	err := h.DB.QueryRow(r.Context(),
		`INSERT INTO batches (name, year, section) VALUES ($1, $2, $3)
		 RETURNING id, name, year, section`,
		in.Name, in.Year, in.Section,
	).Scan(&b.ID, &b.Name, &b.Year, &b.Section)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "batch name already exists")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(r.Context(),
		`SELECT id, name, year, section FROM batches ORDER BY year, name`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()

	out := []batchOut{} // empty slice, so the JSON is [] and not null
	for rows.Next() {
		var b batchOut
		if err := rows.Scan(&b.ID, &b.Name, &b.Year, &b.Section); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}