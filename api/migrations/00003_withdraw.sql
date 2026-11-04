-- +goose Up
ALTER TABLE students ADD COLUMN withdraw_code_hash TEXT;

-- +goose Down
ALTER TABLE students DROP COLUMN withdraw_code_hash;