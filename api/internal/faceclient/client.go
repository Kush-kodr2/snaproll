package faceclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

const EmbeddingBytesLen = 512 * 4 // 512 float32 values

type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

func New(baseURL, key string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		key:     key,
		http:    &http.Client{Timeout: 60 * time.Second}, // big class photos take a while on CPU
	}
}

type Face struct {
	BBox         []int   `json:"bbox"`
	Width        int     `json:"width"`
	DetScore     float64 `json:"det_score"`
	Sharpness    float64 `json:"sharpness"`
	EmbeddingB64 string  `json:"embedding_b64"`
}

type Result struct {
	ImageWidth  int    `json:"image_width"`
	ImageHeight int    `json:"image_height"`
	RotatedBy   int    `json:"rotated_by"`
	Faces       []Face `json:"faces"`
}

// EmbeddingBytes decodes the embedding into the 2048 raw bytes stored in the BYTEA column.
func (f Face) EmbeddingBytes() ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(f.EmbeddingB64)
	if err != nil {
		return nil, err
	}
	if len(raw) != EmbeddingBytesLen {
		return nil, fmt.Errorf("embedding is %d bytes, want %d", len(raw), EmbeddingBytesLen)
	}
	return raw, nil
}

// Floats turns stored embedding bytes back into float32 values for matching.
func Floats(raw []byte) []float32 {
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out
}

// StatusError is returned when the face service answers with a non-200 status.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("face service returned %d: %s", e.Code, e.Body)
}

func (c *Client) Analyze(ctx context.Context, filename string, image io.Reader) (*Result, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, image); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/analyze", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Internal-Key", c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, &StatusError{Code: resp.StatusCode, Body: string(msg)}
	}

	var res Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}