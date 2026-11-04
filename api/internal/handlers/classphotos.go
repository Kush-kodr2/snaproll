package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"

	"attendance-api/internal/auth"
	"attendance-api/internal/faceclient"
	"attendance-api/internal/match"
)

const (
	maxSessionPhotos = 4
	maxSessionUpload = 64 << 20

	// Provisional starting points, to be tuned in Phase 7 with real data.
	presentMin = 0.50 // at or above this AND a clear margin => present
	reviewMin  = 0.30 // at or above this => the teacher should look
	marginMin  = 0.08 // gap to the next-best student
)

// Attendance only moves upward and never overwrites a manual decision.
const upgradeAttendanceSQL = `
UPDATE attendance
SET status = $3::text, confidence = $4::real, updated_at = now()
WHERE session_id = $1 AND student_id = $2 AND method = 'auto'
  AND (
        CASE status WHEN 'present' THEN 2 WHEN 'needs_review' THEN 1 ELSE 0 END
      < CASE $3::text WHEN 'present' THEN 2 WHEN 'needs_review' THEN 1 ELSE 0 END
      OR (status = $3::text AND COALESCE(confidence, -1) < $4::real)
  )`

type studentInfo struct {
	EnrollmentNo string
	Name         string
}

type matchOut struct {
	StudentID    int64   `json:"student_id"`
	EnrollmentNo string  `json:"enrollment_no"`
	Name         string  `json:"name"`
	Status       string  `json:"status"` // present | needs_review
	Score        float32 `json:"score"`
	Margin       float32 `json:"margin"`
	Photo        int     `json:"photo"`
	FaceWidth    int     `json:"face_width"`
}

type photoOut struct {
	Photo     int `json:"photo"`
	Faces     int `json:"faces"`
	RotatedBy int `json:"rotated_by"`
	Matched   int `json:"matched"`
	Unmatched int `json:"unmatched"`
}

func rank(status string) int {
	switch status {
	case "present":
		return 2
	case "needs_review":
		return 1
	}
	return 0
}

func classify(score, margin float32) string {
	if score >= presentMin && margin >= marginMin {
		return "present"
	}
	return "needs_review"
}

// loadBatch returns every active student in the batch, and the enrolled face photos
// of those who have enrolled.
func (h *Handler) loadBatch(ctx context.Context, batchID int64) ([]match.Template, map[int64]studentInfo, error) {
	info := map[int64]studentInfo{}
	rows, err := h.DB.Query(ctx,
		`SELECT id, enrollment_no, name FROM students WHERE batch_id = $1 AND is_active`, batchID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id int64
		var si studentInfo
		if err := rows.Scan(&id, &si.EnrollmentNo, &si.Name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		info[id] = si
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	rows, err = h.DB.Query(ctx,
		`SELECT e.student_id, e.embedding
		 FROM face_embeddings e JOIN students s ON s.id = e.student_id
		 WHERE s.batch_id = $1 AND s.is_active
		 ORDER BY e.student_id`, batchID)
	if err != nil {
		return nil, nil, err
	}
	var templates []match.Template
	idx := map[int64]int{} // student id -> position in templates
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, nil, err
		}
		i, seen := idx[id]
		if !seen {
			i = len(templates)
			idx[id] = i
			templates = append(templates, match.Template{StudentID: id})
		}
		templates[i].Vecs = append(templates[i].Vecs, faceclient.Floats(raw))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return templates, info, nil
}

// POST /sessions/{id}/photos  (multipart, field "photos", 1 to 4 files)
// Do NOT shrink class photos before uploading: small back-row faces need every pixel.
func (h *Handler) SessionPhotos(w http.ResponseWriter, r *http.Request) {
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

	var status string
	var batchID int64
	if err := h.DB.QueryRow(ctx,
		`SELECT s.status, o.batch_id FROM sessions s
		 JOIN offerings o ON o.id = s.offering_id WHERE s.id = $1`, sessionID,
	).Scan(&status, &batchID); err != nil {
		serverError(w, err)
		return
	}
	if status == "closed" {
		writeError(w, http.StatusConflict, "this session is closed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSessionUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "upload too large or not a valid form")
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["photos"]
	if len(files) < 1 || len(files) > maxSessionPhotos {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("send between 1 and %d photos", maxSessionPhotos))
		return
	}

	templates, info, err := h.loadBatch(ctx, batchID)
	if err != nil {
		serverError(w, err)
		return
	}
	if len(templates) == 0 {
		writeError(w, http.StatusConflict, "no student in this batch has enrolled a face yet")
		return
	}

	// Analyze and match every photo BEFORE touching the database, so a failure halfway
	// through leaves the attendance untouched.
	best := map[int64]matchOut{} // best match per student across all photos
	photos := make([]photoOut, 0, len(files))
	facesTotal, unmatchedTotal := 0, 0

	for i, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("photo %d: could not read the file", i+1))
			return
		}
		res, err := h.Face.Analyze(ctx, fh.Filename, f)
		f.Close()
		var se *faceclient.StatusError
		if errors.As(err, &se) && se.Code == http.StatusBadRequest {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("photo %d is not a readable image", i+1))
			return
		}
		if err != nil {
			log.Printf("class photos: face service error: %v", err)
			writeError(w, http.StatusBadGateway, "face recognition is unavailable, try again later")
			return
		}

		vecs := make([][]float32, len(res.Faces))
		for k, face := range res.Faces {
			raw, err := face.EmbeddingBytes()
			if err != nil {
				serverError(w, err)
				return
			}
			vecs[k] = faceclient.Floats(raw)
		}
		results, unmatched := match.AssignPhoto(vecs, templates, reviewMin)

		for _, m := range results {
			face := res.Faces[m.Face]
			st := classify(m.Score, m.Margin)
			log.Printf("match: session=%d photo=%d student=%d status=%s score=%.2f margin=%.2f width=%d",
				sessionID, i+1, m.StudentID, st, m.Score, m.Margin, face.Width)
			cur, seen := best[m.StudentID]
			if !seen || rank(st) > rank(cur.Status) ||
				(rank(st) == rank(cur.Status) && m.Score > cur.Score) {
				si := info[m.StudentID]
				best[m.StudentID] = matchOut{
					StudentID: m.StudentID, EnrollmentNo: si.EnrollmentNo, Name: si.Name,
					Status: st, Score: m.Score, Margin: m.Margin, Photo: i + 1, FaceWidth: face.Width,
				}
			}
		}
		for _, u := range unmatched {
			log.Printf("unmatched face: session=%d photo=%d width=%d best=%.2f",
				sessionID, i+1, res.Faces[u.Face].Width, u.Best)
		}
		photos = append(photos, photoOut{
			Photo: i + 1, Faces: len(res.Faces), RotatedBy: res.RotatedBy,
			Matched: len(results), Unmatched: len(unmatched),
		})
		facesTotal += len(res.Faces)
		unmatchedTotal += len(unmatched)
	}

	// Save in one transaction.
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	// Students added after the session was created still need an attendance row.
	if _, err := tx.Exec(ctx,
		`INSERT INTO attendance (session_id, student_id)
		 SELECT $1, id FROM students WHERE batch_id = $2 AND is_active
		 ON CONFLICT (session_id, student_id) DO NOTHING`, sessionID, batchID); err != nil {
		serverError(w, err)
		return
	}
	for studentID, m := range best {
		if _, err := tx.Exec(ctx, upgradeAttendanceSQL, sessionID, studentID, m.Status, m.Score); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE sessions SET status = 'reviewing' WHERE id = $1 AND status = 'open'`, sessionID); err != nil {
		serverError(w, err)
		return
	}

	summary := map[string]int64{"present": 0, "needs_review": 0, "absent": 0}
	rows, err := tx.Query(ctx,
		`SELECT status, count(*) FROM attendance WHERE session_id = $1 GROUP BY status`, sessionID)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var s string
		var c int64
		if err := rows.Scan(&s, &c); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		summary[s] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}

	matches := make([]matchOut, 0, len(best))
	for _, m := range best {
		matches = append(matches, m)
	}
	sort.Slice(matches, func(a, b int) bool {
		if matches[a].Status != matches[b].Status {
			return rank(matches[a].Status) > rank(matches[b].Status)
		}
		return matches[a].EnrollmentNo < matches[b].EnrollmentNo
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"photos":                photos,
		"matches":               matches,
		"summary":               summary,
		"enrolled_students":     len(templates),
		"not_enrolled_students": len(info) - len(templates), // these stay absent until marked by hand
		"faces_detected":        facesTotal,
		"faces_unmatched":       unmatchedTotal,
		"thresholds": map[string]float64{
			"present_min": presentMin, "review_min": reviewMin, "margin_min": marginMin,
		},
	})
}