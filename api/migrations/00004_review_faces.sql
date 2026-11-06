-- +goose Up
CREATE TABLE review_faces (
    id         BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    student_id BIGINT NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    photo_no   INT NOT NULL,
    score      REAL NOT NULL,
    margin     REAL NOT NULL,
    face_width INT NOT NULL,
    crop       BYTEA NOT NULL,          -- small JPEG thumbnail, deleted after review
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, student_id)     -- one candidate face per student per session
);
CREATE INDEX idx_review_faces_created ON review_faces(created_at);

-- +goose Down
DROP TABLE review_faces;