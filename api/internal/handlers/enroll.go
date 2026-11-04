package handlers

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"attendance-api/internal/faceclient"
	"attendance-api/internal/match"
)

const (
	consentVersion = "2026-10-v1"
	minPhotos      = 3
	maxPhotos      = 5
	maxUploadBytes = 40 << 20

	minFaceWidth     = 200   // px, in the uploaded photo
	minDetScore      = 0.45  // your good selfies scored 0.83-0.85, the dim one 0.54
	minSharpness     = 400.0 // provisional: only logged until we have volunteer data
	enforceSharpness = false
	samePersonMin    = 0.40 // two photos of the same person should score at least this
	nearDuplicate    = 0.97 // above this, two "different" photos are really the same picture
	duplicateFace    = 0.50 // new face vs another student's enrolled photos
	calibrateMode = false // TEMPORARY: accept any single-face photo and only log the numbers
	
)

var errBadInvite = errors.New("invalid enrollment number or code")

type enrolledPhoto struct {
	raw []byte
	vec []float32
	det float64
}

// verifyInvite returns the student's id if enrollment number + code are valid right now.
func (h *Handler) verifyInvite(ctx context.Context, enrollmentNo, code string) (int64, error) {
	var id int64
	var hash *string
	var expires *time.Time
	err := h.DB.QueryRow(ctx,
		`SELECT id, enroll_code_hash, enroll_code_expires_at FROM students
		 WHERE enrollment_no = $1 AND is_active AND consent_given_at IS NULL`, enrollmentNo,
	).Scan(&id, &hash, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errBadInvite
	}
	if err != nil {
		return 0, err
	}
	if hash == nil || expires == nil || time.Now().After(*expires) {
		return 0, errBadInvite
	}
	if !hmac.Equal([]byte(h.hashInviteCode(code)), []byte(*hash)) {
		return 0, errBadInvite
	}
	return id, nil
}

// POST /enroll  (multipart form, no login: the invite code is the credential)
// fields: enrollment_no, code, consent=true, photos (3 to 5 files)
func (h *Handler) Enroll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "upload too large or not a valid form")
		return
	}
	defer r.MultipartForm.RemoveAll() // delete temp files created for big uploads

	enrollmentNo := strings.ToUpper(strings.TrimSpace(r.FormValue("enrollment_no")))
	code := strings.ToUpper(strings.TrimSpace(r.FormValue("code")))
	if enrollmentNo == "" || len(enrollmentNo) > 50 || code == "" || len(code) > 20 {
		writeError(w, http.StatusUnprocessableEntity, "enrollment number and code are required")
		return
	}
	if r.FormValue("consent") != "true" {
		writeError(w, http.StatusUnprocessableEntity, "you must tick the consent box to enroll")
		return
	}
	files := r.MultipartForm.File["photos"]
	if len(files) < minPhotos || len(files) > maxPhotos {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("send between %d and %d photos", minPhotos, maxPhotos))
		return
	}

	// 1. Who is this, and is the code valid?
	if enrollLimiter.locked(enrollmentNo) {
		writeError(w, http.StatusTooManyRequests, "too many wrong attempts, try again in 15 minutes")
		return
	}
	studentID, err := h.verifyInvite(ctx, enrollmentNo, code)
	if errors.Is(err, errBadInvite) {
		enrollLimiter.fail(enrollmentNo)
		writeError(w, http.StatusUnauthorized, "invalid enrollment number or code")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	// 2. Check every photo with the face service.
	var photos []enrolledPhoto
	var problems []string
	for i, fh := range files {
		label := fmt.Sprintf("photo %d", i+1)
		f, err := fh.Open()
		if err != nil {
			problems = append(problems, label+": could not read the file")
			continue
		}
		res, err := h.Face.Analyze(ctx, fh.Filename, f)
		f.Close()
		var se *faceclient.StatusError
		if errors.As(err, &se) && se.Code == http.StatusBadRequest {
			problems = append(problems, label+": this file is not a readable image")
			continue
		}
		if err != nil {
			log.Printf("enroll: face service error: %v", err)
			writeError(w, http.StatusBadGateway, "face recognition is unavailable, try again later")
			return
		}

		switch {
		case len(res.Faces) == 0:
			problems = append(problems, label+": no clear face found. Face the camera in good light.")
		case len(res.Faces) > 1:
			problems = append(problems, label+": more than one face found. Only you should be in the photo.")
		default:
			face := res.Faces[0]
			raw, err := face.EmbeddingBytes()
			if err != nil {
				serverError(w, err)
				return
			}
			reason := ""
			switch {
			case face.Width < minFaceWidth:
				reason = "your face is too small. Move closer."
			case face.DetScore < minDetScore:
				reason = "the face is not clear enough. Face the camera straight on, in good light."
			case enforceSharpness && face.Sharpness < minSharpness:
				reason = "the photo is blurry. Hold the phone steady."
			}
			log.Printf("enroll check: student=%d photo=%d width=%d det=%.2f sharp=%.0f rotated=%d reject=%q",
				studentID, i+1, face.Width, face.DetScore, face.Sharpness, res.RotatedBy, reason)
			if reason != "" && !calibrateMode {
				problems = append(problems, label+": "+reason)
				continue
			}
			photos = append(photos, enrolledPhoto{raw: raw, vec: faceclient.Floats(raw), det: face.DetScore})
		}
	}
	if len(problems) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "some photos need to be retaken", "problems": problems})
		return
	}

	// 3. Are the photos different from each other, and do they show the same person?
	vecs := make([][]float32, len(photos))
	for i := range photos {
		vecs[i] = photos[i].vec
	}
	n := len(vecs)
	flagged := make([]bool, n) // report each duplicate photo once, not once per pair
	for i := 0; i < n; i++ {
		agree := 0 // how many other photos look like the same person as photo i
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			sim := match.Dot(vecs[i], vecs[j])
			if sim >= samePersonMin {
				agree++
			}
			if i < j {
				log.Printf("enroll pair: student=%d photos=%d,%d sim=%.2f", studentID, i+1, j+1, sim)
				if sim >= nearDuplicate && !flagged[j] {
					flagged[j] = true
					problems = append(problems, fmt.Sprintf(
						"photo %d is almost the same picture as photo %d. Use a different photo.", j+1, i+1))
				}
			}
		}
		if agree < n/2 { // at least half of the other photos must match this one
			problems = append(problems, fmt.Sprintf(
				"photo %d does not look like the same person as the other photos", i+1))
		}
	}
	// 4. Is this face already enrolled under a different student?
	mean := match.MeanUnit(vecs)
	rows, err := h.DB.Query(ctx,
		`SELECT student_id, embedding FROM face_embeddings WHERE student_id <> $1`, studentID)
	if err != nil {
		serverError(w, err)
		return
	}
	var best float32
	var bestID int64
	for rows.Next() {
		var otherID int64
		var raw []byte
		if err := rows.Scan(&otherID, &raw); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		if sim := match.Dot(mean, faceclient.Floats(raw)); sim > best {
			best, bestID = sim, otherID
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	log.Printf("enroll duplicate check: student=%d best_other=%.2f (student=%d)", studentID, best, bestID)
	if best >= duplicateFace {
		log.Printf("enroll: duplicate face: student=%d vs student=%d sim=%.2f", studentID, bestID, best)
		_, _ = h.DB.Exec(ctx, `INSERT INTO audit_log (action, target) VALUES ($1, $2)`,
			"enroll_duplicate_face", fmt.Sprintf("student:%d ~ student:%d", studentID, bestID))
		writeError(w, http.StatusConflict,
			"this face is already registered for someone else. Please contact the admin.")
		return
	}

	// 5. Save everything in one transaction and burn the invite code.
	withdrawCode, err := newInviteCode()
	if err != nil {
		serverError(w, err)
		return
	}
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	// Lock the row and make sure nobody enrolled this student while we were busy.
		var studentName string
	err = tx.QueryRow(ctx,
		`SELECT name FROM students WHERE id = $1 AND consent_given_at IS NULL FOR UPDATE`, studentID,
	).Scan(&studentName)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "this student is already enrolled")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	for _, p := range photos {
		if _, err := tx.Exec(ctx,
			`INSERT INTO face_embeddings (student_id, embedding, quality_score) VALUES ($1, $2, $3)`,
			studentID, p.raw, float32(p.det)); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE students
		 SET consent_given_at = now(), consent_version = $2,
		     enroll_code_hash = NULL, enroll_code_expires_at = NULL, withdraw_code_hash = $3
		 WHERE id = $1`,
		studentID, consentVersion, h.hashCode("withdraw", withdrawCode)); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log (action, target) VALUES ($1, $2)`,
		"enrolled", fmt.Sprintf("student:%d", studentID)); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}

	enrollLimiter.reset(enrollmentNo)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"name" : studentName,
		"message":       "enrolled",
		"photos_used":   len(photos),
		"withdraw_code": withdrawCode, // shown once: lets the student delete their data later
	})
}

// POST /enroll/withdraw  {"enrollment_no": "...", "withdraw_code": "..."}
// Deletes the student's face data and consent record. Attendance history is kept.
func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		EnrollmentNo string `json:"enrollment_no"`
		WithdrawCode string `json:"withdraw_code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	no := strings.ToUpper(strings.TrimSpace(in.EnrollmentNo))
	code := strings.ToUpper(strings.TrimSpace(in.WithdrawCode))
	key := "withdraw:" + no
	if enrollLimiter.locked(key) {
		writeError(w, http.StatusTooManyRequests, "too many wrong attempts, try again in 15 minutes")
		return
	}

	var id int64
	var hash *string
	err := h.DB.QueryRow(ctx,
		`SELECT id, withdraw_code_hash FROM students WHERE enrollment_no = $1`, no,
	).Scan(&id, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	if err != nil || hash == nil || !hmac.Equal([]byte(h.hashCode("withdraw", code)), []byte(*hash)) {
		enrollLimiter.fail(key)
		writeError(w, http.StatusUnauthorized, "invalid enrollment number or withdraw code")
		return
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM face_embeddings WHERE student_id = $1`, id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE students SET consent_given_at = NULL, consent_version = NULL, withdraw_code_hash = NULL
		 WHERE id = $1`, id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log (action, target) VALUES ($1, $2)`,
		"withdrawn", fmt.Sprintf("student:%d", id)); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	enrollLimiter.reset(key)
	writeJSON(w, http.StatusOK, map[string]string{"message": "your face data has been deleted"})
}
