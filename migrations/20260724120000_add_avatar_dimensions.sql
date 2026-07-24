-- +goose Up
ALTER TABLE avatars
    ADD COLUMN width INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN height INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE avatars
    DROP COLUMN height,
    DROP COLUMN width;
