package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"time"
)

const (
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 32 symbols, no 0/O/1/I to avoid mix-ups
	codeLength   = 10
	inviteTTL    = 7 * 24 * time.Hour
)

func newInviteCode() (string, error) {
	b := make([]byte, codeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = codeAlphabet[b[i]%32] // 256 is divisible by 32, so every symbol is equally likely
	}
	return string(b), nil
}

// hashInviteCode is a keyed hash, so a leaked database alone isn't enough to brute-force codes.
func (h *Handler) hashCode(purpose, code string) string {
	m := hmac.New(sha256.New, h.Secret)
	m.Write([]byte(purpose + ":" + code))
	return hex.EncodeToString(m.Sum(nil))
}

func (h *Handler) hashInviteCode(code string) string { return h.hashCode("invite", code) }

type inviteOut struct {
	StudentID    int64     `json:"student_id"`
	EnrollmentNo string    `json:"enrollment_no"`
	Name         string    `json:"name"`
	Code         string    `json:"code"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// POST /batches/{id}/invite-codes
// Creates a fresh single-use code for every active student in the batch who hasn't
// enrolled yet. Codes are shown ONCE in this response; only a hash is stored.
// Calling it again replaces the earlier codes.
func (h *Handler) CreateInviteCodes(w http.ResponseWriter, r *http.Request) {
	batchID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid batch id")
		return
	}
	ctx := r.Context()

	var exists bool
	if err := h.DB.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM batches WHERE id = $1)`, batchID).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "batch not found")
		return
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`SELECT id, enrollment_no, name FROM students
		 WHERE batch_id = $1 AND is_active AND consent_given_at IS NULL
		 ORDER BY enrollment_no`, batchID)
	if err != nil {
		serverError(w, err)
		return
	}
	out := []inviteOut{}
	for rows.Next() {
		var s inviteOut
		if err := rows.Scan(&s.StudentID, &s.EnrollmentNo, &s.Name); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		out = append(out, s)
	}
	rows.Close() // must close before running more statements in this transaction
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}

	expires := time.Now().Add(inviteTTL)
	for i := range out {
		code, err := newInviteCode()
		if err != nil {
			serverError(w, err)
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE students SET enroll_code_hash = $1, enroll_code_expires_at = $2 WHERE id = $3`,
			h.hashInviteCode(code), expires, out[i].StudentID); err != nil {
			serverError(w, err)
			return
		}
		out[i].Code = code
		out[i].ExpiresAt = expires
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store") // these codes must not be cached anywhere
	writeJSON(w, http.StatusCreated, out)
}

// POST /students/{id}/invite-code
// Issues a fresh code for ONE student: a lost code, a new student, or re-enrollment
// after a withdrawal. Other students' codes are not touched.
func (h *Handler) CreateStudentInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid student id")
		return
	}
	code, err := newInviteCode()
	if err != nil {
		serverError(w, err)
		return
	}
	expires := time.Now().Add(inviteTTL)

	out := inviteOut{Code: code, ExpiresAt: expires}
	err = h.DB.QueryRow(r.Context(),
		`UPDATE students SET enroll_code_hash = $2, enroll_code_expires_at = $3
		 WHERE id = $1 AND is_active AND consent_given_at IS NULL
		 RETURNING id, enrollment_no, name`,
		id, h.hashInviteCode(code), expires,
	).Scan(&out.StudentID, &out.EnrollmentNo, &out.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict,
			"student not found, inactive, or still enrolled (they must withdraw first)")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
}
