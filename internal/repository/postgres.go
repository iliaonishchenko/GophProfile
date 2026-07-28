package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iliaonishchenko/GophProfile/internal/domain"
)

type Postgres struct {
	db *sql.DB
}

func NewPostgres(db *sql.DB) *Postgres {
	return &Postgres{db: db}
}

func (r *Postgres) Create(ctx context.Context, avatar domain.Avatar) error {
	thumbnailKeys, err := json.Marshal(avatar.ThumbnailS3Keys)
	if err != nil {
		return fmt.Errorf("не удалось сериализовать ключи миниатюр: %w", err)
	}

	const query = `
		INSERT INTO avatars (
			id, user_id, file_name, mime_type, size_bytes, s3_key,
			thumbnail_s3_keys, upload_status, processing_status, width, height,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)`

	if _, err := r.db.ExecContext(ctx, query,
		avatar.ID, avatar.UserID, avatar.FileName, avatar.MIMEType,
		avatar.SizeBytes, avatar.S3Key, thumbnailKeys, avatar.UploadStatus,
		avatar.ProcessingStatus, avatar.Width, avatar.Height, avatar.CreatedAt,
	); err != nil {
		return fmt.Errorf("не удалось добавить аватарку: %w", err)
	}
	return nil
}

func (r *Postgres) Get(ctx context.Context, id string) (domain.Avatar, error) {
	const query = `
		SELECT id, user_id, file_name, mime_type, size_bytes, s3_key,
		       thumbnail_s3_keys, upload_status, processing_status,
		       width, height, created_at, updated_at, deleted_at
		FROM avatars
		WHERE id = $1 AND deleted_at IS NULL`
	return scanAvatar(r.db.QueryRowContext(ctx, query, id))
}

func (r *Postgres) ListByUser(ctx context.Context, userID string) ([]domain.Avatar, error) {
	const query = `
		SELECT id, user_id, file_name, mime_type, size_bytes, s3_key,
		       thumbnail_s3_keys, upload_status, processing_status,
		       width, height, created_at, updated_at, deleted_at
		FROM avatars
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список аватарок: %w", err)
	}
	defer func() { _ = rows.Close() }()

	avatars := make([]domain.Avatar, 0)
	for rows.Next() {
		avatar, err := scanAvatar(rows)
		if err != nil {
			return nil, err
		}
		avatars = append(avatars, avatar)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("не удалось прочитать строки списка аватарок: %w", err)
	}
	return avatars, nil
}

func (r *Postgres) SoftDelete(ctx context.Context, id, userID string) (domain.Avatar, error) {
	const query = `
		UPDATE avatars
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		RETURNING id, user_id, file_name, mime_type, size_bytes, s3_key,
		          thumbnail_s3_keys, upload_status, processing_status,
		          width, height, created_at, updated_at, deleted_at`

	avatar, err := scanAvatar(r.db.QueryRowContext(ctx, query, id, userID))
	if !errors.Is(err, domain.ErrNotFound) {
		return avatar, err
	}

	var owner string
	ownerErr := r.db.QueryRowContext(
		ctx,
		`SELECT user_id FROM avatars WHERE id = $1 AND deleted_at IS NULL`,
		id,
	).Scan(&owner)
	if errors.Is(ownerErr, sql.ErrNoRows) {
		return domain.Avatar{}, domain.ErrNotFound
	}
	if ownerErr != nil {
		return domain.Avatar{}, fmt.Errorf("не удалось определить владельца аватарки: %w", ownerErr)
	}
	return domain.Avatar{}, domain.ErrForbidden
}

func (r *Postgres) ClaimProcessing(ctx context.Context, id string) (bool, error) {
	const query = `
		UPDATE avatars
		SET processing_status = 'processing', updated_at = NOW()
		WHERE id = $1
		  AND deleted_at IS NULL
		  AND processing_status IN ('pending', 'failed')`
	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return false, fmt.Errorf("не удалось захватить аватарку для обработки: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("не удалось получить число захваченных строк: %w", err)
	}
	return count == 1, nil
}

func (r *Postgres) CompleteProcessing(ctx context.Context, id string, keys map[string]string) error {
	payload, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("не удалось сериализовать ключи миниатюр: %w", err)
	}
	const query = `
		UPDATE avatars
		SET thumbnail_s3_keys = $2, processing_status = 'completed', updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`
	result, err := r.db.ExecContext(ctx, query, id, payload)
	if err != nil {
		return fmt.Errorf("не удалось завершить обработку аватарки: %w", err)
	}
	return requireUpdated(result)
}

func (r *Postgres) FailProcessing(ctx context.Context, id string) error {
	const query = `
		UPDATE avatars SET processing_status = 'failed', updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL AND processing_status <> 'completed'`
	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("не удалось отметить обработку аватарки как неуспешную: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanAvatar(row scanner) (domain.Avatar, error) {
	var avatar domain.Avatar
	var thumbnailKeys []byte
	err := row.Scan(
		&avatar.ID, &avatar.UserID, &avatar.FileName, &avatar.MIMEType,
		&avatar.SizeBytes, &avatar.S3Key, &thumbnailKeys, &avatar.UploadStatus,
		&avatar.ProcessingStatus, &avatar.Width, &avatar.Height, &avatar.CreatedAt,
		&avatar.UpdatedAt, &avatar.DeletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Avatar{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Avatar{}, fmt.Errorf("не удалось прочитать данные аватарки: %w", err)
	}
	if len(thumbnailKeys) > 0 && string(thumbnailKeys) != "null" {
		if err := json.Unmarshal(thumbnailKeys, &avatar.ThumbnailS3Keys); err != nil {
			return domain.Avatar{}, fmt.Errorf("не удалось десериализовать ключи миниатюр: %w", err)
		}
	}
	if avatar.ThumbnailS3Keys == nil {
		avatar.ThumbnailS3Keys = map[string]string{}
	}
	return avatar, nil
}

func requireUpdated(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("не удалось получить число изменённых строк: %w", err)
	}
	if count == 0 {
		return domain.ErrNotFound
	}
	return nil
}
