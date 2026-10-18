package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"

	"attendance-api/internal/auth"
)

type teacherInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type teacherOut struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// validate cleans the input in place and returns an error message, or "" if OK.
func (in *teacherInput) validate() string {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))

	if in.Name == "" || len(in.Name) > 100 {
		return "name is required (max 100 characters)"
	}
	if addr, err := mail.ParseAddress(in.Email); err != nil || addr.Address != in.Email {
		return "invalid email"
	}
	if len(in.Password) < 8 || len(in.Password) > 72 { // bcrypt limit is 72 bytes
		return "password must be 8 to 72 bytes"
	}
	return ""
}

func (h *Handler) insertTeacher(ctx context.Context, in teacherInput, role string) (teacherOut, error) {
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return teacherOut{}, err
	}
	var t teacherOut
	err = h.DB.QueryRow(ctx,
		`INSERT INTO teachers (name, email, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, name, email, role`,
		in.Name, in.Email, hash, role,
	).Scan(&t.ID, &t.Name, &t.Email, &t.Role)
	return t, err
}

// Bootstrap creates the first admin. It only works while the teachers table is empty.
func (h *Handler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	var in teacherInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if msg := in.validate(); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}

	var count int
	if err := h.DB.QueryRow(r.Context(), `SELECT count(*) FROM teachers`).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count > 0 {
		writeError(w, http.StatusForbidden, "already initialized")
		return
	}

	t, err := h.insertTeacher(r.Context(), in, "admin")
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) CreateTeacher(w http.ResponseWriter, r *http.Request) {
	var in teacherInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if msg := in.validate(); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	t, err := h.insertTeacher(r.Context(), in, "teacher")
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "email already registered")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))

	var id int64
	var hash, role string
	err := h.DB.QueryRow(r.Context(),
		`SELECT id, password_hash, role FROM teachers WHERE email = $1`, email,
	).Scan(&id, &hash, &role)

	// Same message for "no such user" and "wrong password" so emails can't be probed.
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !auth.CheckPassword(hash, in.Password)) {
		writeError(w, http.StatusUnauthorized, "incorrect email or password")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	token, err := auth.NewToken(h.Secret, id, role, h.TokenTTL)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"access_token": token, "token_type": "bearer"})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	var t teacherOut
	err := h.DB.QueryRow(r.Context(),
		`SELECT id, name, email, role FROM teachers WHERE id = $1`, p.ID,
	).Scan(&t.ID, &t.Name, &t.Email, &t.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "account no longer exists")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}