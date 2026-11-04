package handlers

import (
	"log"
	"net/http"
)

type faceSummary struct {
	Width     int     `json:"width"`
	DetScore  float64 `json:"det_score"`
	Sharpness float64 `json:"sharpness"`
}

// POST /tools/analyze  (multipart form, field "file")
func (h *Handler) AnalyzeTool(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "send a multipart form with a 'file' field (max 16 MB)")
		return
	}
	defer file.Close()

	res, err := h.Face.Analyze(r.Context(), hdr.Filename, file)
	if err != nil {
		log.Printf("face service error: %v", err)
		writeError(w, http.StatusBadGateway, "face service error")
		return
	}

	faces := make([]faceSummary, 0, len(res.Faces))
	for _, f := range res.Faces {
		if _, err := f.EmbeddingBytes(); err != nil { // proves the embedding decodes cleanly
			serverError(w, err)
			return
		}
		faces = append(faces, faceSummary{Width: f.Width, DetScore: f.DetScore, Sharpness: f.Sharpness})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"image_width": res.ImageWidth,
		"rotated_by":  res.RotatedBy,
		"face_count":  len(faces),
		"faces":       faces,
	})
}