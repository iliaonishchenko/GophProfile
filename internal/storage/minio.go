package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIO struct {
	client *minio.Client
	bucket string
}

func NewMinIO(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*MinIO, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("не удалось создать клиент S3: %w", err)
	}
	return &MinIO{client: client, bucket: bucket}, nil
}

func (s *MinIO) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("не удалось проверить бакет %q: %w", s.bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("не удалось создать бакет %q: %w", s.bucket, err)
	}
	return nil
}

func (s *MinIO) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, body, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("не удалось загрузить объект %q: %w", key, err)
	}
	return nil
}

func (s *MinIO) Get(ctx context.Context, key string) (domain.Object, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return domain.Object{}, fmt.Errorf("не удалось получить объект %q: %w", key, err)
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		response := minio.ToErrorResponse(err)
		if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" {
			return domain.Object{}, domain.ErrNotFound
		}
		return domain.Object{}, fmt.Errorf("не удалось получить метаданные объекта %q: %w", key, err)
	}
	return domain.Object{Body: object, Size: info.Size, ContentType: info.ContentType, ETag: info.ETag}, nil
}

func (s *MinIO) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("не удалось удалить объект %q: %w", key, err)
	}
	return nil
}

func (s *MinIO) Health(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("проверка состояния S3 завершилась ошибкой: %w", err)
	}
	if !exists {
		return fmt.Errorf("проверка состояния S3: бакет %q не существует", s.bucket)
	}
	return nil
}
