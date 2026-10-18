-- +goose Up
CREATE TABLE teachers (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'teacher' CHECK (role IN ('teacher', 'admin')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE batches (
    id      BIGSERIAL PRIMARY KEY,
    name    TEXT NOT NULL UNIQUE,
    year    INT NOT NULL CHECK (year BETWEEN 1 AND 6),
    section TEXT
);

CREATE TABLE students (
    id               BIGSERIAL PRIMARY KEY,
    enrollment_no    TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    email            TEXT,
    batch_id         BIGINT NOT NULL REFERENCES batches(id),
    consent_given_at TIMESTAMPTZ,
    consent_version  TEXT,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE face_embeddings (
    id            BIGSERIAL PRIMARY KEY,
    student_id    BIGINT NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    embedding     BYTEA NOT NULL,   -- 512 float32 values = 2048 bytes
    quality_score REAL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_face_embeddings_student ON face_embeddings(student_id);

CREATE TABLE courses (
    id   BIGSERIAL PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL
);

CREATE TABLE offerings (
    id         BIGSERIAL PRIMARY KEY,
    course_id  BIGINT NOT NULL REFERENCES courses(id),
    teacher_id BIGINT NOT NULL REFERENCES teachers(id),
    batch_id   BIGINT NOT NULL REFERENCES batches(id),
    UNIQUE (course_id, teacher_id, batch_id)
);

CREATE TABLE sessions (
    id           BIGSERIAL PRIMARY KEY,
    offering_id  BIGINT NOT NULL REFERENCES offerings(id),
    session_date DATE NOT NULL,
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'reviewing', 'closed')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE session_photos (
    id          BIGSERIAL PRIMARY KEY,
    session_id  BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    file_ref    TEXT NOT NULL,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE attendance (
    id          BIGSERIAL PRIMARY KEY,
    session_id  BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    student_id  BIGINT NOT NULL REFERENCES students(id),
    status      TEXT NOT NULL DEFAULT 'absent' CHECK (status IN ('present', 'absent', 'needs_review')),
    method      TEXT NOT NULL DEFAULT 'auto' CHECK (method IN ('auto', 'manual')),
    confidence  REAL,
    reviewed_by BIGINT REFERENCES teachers(id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, student_id)
);

CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    actor_id   BIGINT REFERENCES teachers(id),
    action     TEXT NOT NULL,
    target     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE attendance;
DROP TABLE session_photos;
DROP TABLE sessions;
DROP TABLE offerings;
DROP TABLE courses;
DROP TABLE face_embeddings;
DROP TABLE students;
DROP TABLE batches;
DROP TABLE teachers;