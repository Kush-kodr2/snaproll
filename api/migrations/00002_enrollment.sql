-- +goose Up
ALTER TABLE students
    ADD COLUMN enroll_code_hash TEXT,
    ADD COLUMN enroll_code_expires_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE students
    DROP COLUMN enroll_code_hash,
    DROP COLUMN enroll_code_expires_at;