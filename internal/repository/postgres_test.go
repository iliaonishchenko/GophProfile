package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"github.com/stretchr/testify/require"
)

var avatarColumns = []string{
	"id", "user_id", "file_name", "mime_type", "size_bytes", "s3_key",
	"thumbnail_s3_keys", "upload_status", "processing_status", "width", "height",
	"created_at", "updated_at", "deleted_at",
}

func TestPostgresCreateAndGet(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewPostgres(db)
	now := time.Now().UTC()
	avatar := domain.Avatar{
		ID: "id", UserID: "user", FileName: "avatar.png", MIMEType: "image/png",
		SizeBytes: 42, S3Key: "key", ThumbnailS3Keys: map[string]string{},
		UploadStatus: domain.UploadStatusUploaded, ProcessingStatus: domain.ProcessingStatusPending,
		Width: 20, Height: 10, CreatedAt: now,
	}
	mock.ExpectExec("INSERT INTO avatars").
		WithArgs("id", "user", "avatar.png", "image/png", int64(42), "key", []byte(`{}`), "uploaded", "pending", 20, 10, now).
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.Create(context.Background(), avatar))

	mock.ExpectQuery("FROM avatars").
		WithArgs("id").WillReturnRows(avatarRow(now, nil))
	loaded, err := repo.Get(context.Background(), "id")
	require.NoError(t, err)
	require.Equal(t, avatar.ID, loaded.ID)
	require.Equal(t, map[string]string{"100x100": "thumb"}, loaded.ThumbnailS3Keys)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresListAndNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewPostgres(db)
	now := time.Now().UTC()
	mock.ExpectQuery("FROM avatars").WithArgs("user").WillReturnRows(avatarRow(now, nil))
	avatars, err := repo.ListByUser(context.Background(), "user")
	require.NoError(t, err)
	require.Len(t, avatars, 1)

	mock.ExpectQuery("FROM avatars").WithArgs("missing").WillReturnError(sql.ErrNoRows)
	_, err = repo.Get(context.Background(), "missing")
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestPostgresProcessingState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewPostgres(db)

	mock.ExpectExec(`processing_status IN \('pending', 'failed'\)`).
		WithArgs("id").
		WillReturnResult(sqlmock.NewResult(0, 1))
	claimed, err := repo.ClaimProcessing(context.Background(), "id")
	require.NoError(t, err)
	require.True(t, claimed)

	mock.ExpectExec("UPDATE avatars").WithArgs("id", []byte(`{"100x100":"thumb"}`)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.CompleteProcessing(context.Background(), "id", map[string]string{"100x100": "thumb"}))
	mock.ExpectExec("UPDATE avatars").WithArgs("id").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.FailProcessing(context.Background(), "id"))
}

func TestPostgresSoftDeleteForbidden(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewPostgres(db)
	mock.ExpectQuery("UPDATE avatars").WithArgs("id", "other-user").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT user_id FROM avatars WHERE id = \$1 AND deleted_at IS NULL`).
		WithArgs("id").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("owner"))
	_, err = repo.SoftDelete(context.Background(), "id", "other-user")
	require.True(t, errors.Is(err, domain.ErrForbidden))

	mock.ExpectQuery("UPDATE avatars").WithArgs("id", "owner").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT user_id FROM avatars WHERE id = \$1 AND deleted_at IS NULL`).
		WithArgs("id").
		WillReturnError(sql.ErrNoRows)
	_, err = repo.SoftDelete(context.Background(), "id", "owner")
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func avatarRow(now time.Time, deletedAt any) *sqlmock.Rows {
	return sqlmock.NewRows(avatarColumns).AddRow(
		"id", "user", "avatar.png", "image/png", int64(42), "key",
		[]byte(`{"100x100":"thumb"}`), "uploaded", "completed", 20, 10,
		now, now, deletedAt,
	)
}
