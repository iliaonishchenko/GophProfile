package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"github.com/iliaonishchenko/GophProfile/internal/retry"
	_ "golang.org/x/image/webp"
)

const MaxAvatarSize int64 = 10 * 1024 * 1024

var allowedMIMETypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

type AvatarService struct {
	repository domain.AvatarRepository
	storage    domain.AvatarStorage
	publisher  domain.MessagePublisher
	baseURL    string
}

func NewAvatarService(
	repository domain.AvatarRepository,
	storage domain.AvatarStorage,
	publisher domain.MessagePublisher,
	baseURL string,
) *AvatarService {
	return &AvatarService{repository: repository, storage: storage, publisher: publisher, baseURL: strings.TrimRight(baseURL, "/")}
}

func (s *AvatarService) Upload(ctx context.Context, userID, fileName string, data []byte) (domain.Avatar, error) {
	if int64(len(data)) > MaxAvatarSize {
		return domain.Avatar{}, domain.ErrFileTooLarge
	}
	mimeType := http.DetectContentType(data)
	extension, ok := allowedMIMETypes[mimeType]
	if !ok {
		return domain.Avatar{}, domain.ErrInvalidImage
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return domain.Avatar{}, domain.ErrInvalidImage
	}

	id := uuid.NewString()
	cleanName := filepath.Base(fileName)
	if cleanName == "." || cleanName == "" {
		cleanName = "avatar" + extension
	}
	key := fmt.Sprintf("originals/%s/%s/%s", userID, id, cleanName)
	now := time.Now().UTC()
	avatar := domain.Avatar{
		ID: id, UserID: userID, FileName: cleanName, MIMEType: mimeType,
		SizeBytes: int64(len(data)), S3Key: key, ThumbnailS3Keys: map[string]string{},
		UploadStatus: domain.UploadStatusUploaded, ProcessingStatus: domain.ProcessingStatusPending,
		Width: config.Width, Height: config.Height, CreatedAt: now, UpdatedAt: now,
	}

	if err := s.storage.Put(ctx, key, bytes.NewReader(data), int64(len(data)), mimeType); err != nil {
		return domain.Avatar{}, err
	}
	if err := s.repository.Create(ctx, avatar); err != nil {
		if cleanupErr := s.storage.Delete(ctx, key); cleanupErr != nil {
			return domain.Avatar{}, errors.Join(
				fmt.Errorf("не удалось создать метаданные аватарки: %w", err),
				fmt.Errorf("не удалось удалить осиротевший объект аватарки: %w", cleanupErr),
			)
		}
		return domain.Avatar{}, fmt.Errorf("не удалось создать метаданные аватарки: %w", err)
	}
	event := domain.AvatarUploadEvent{MessageID: uuid.NewString(), AvatarID: id, UserID: userID, S3Key: key}
	if err := retry.Do(ctx, 3, 100*time.Millisecond, func() error { return s.publisher.PublishUpload(ctx, event) }); err != nil {
		publishErr := fmt.Errorf("не удалось опубликовать событие загрузки аватарки: %w", err)
		_, deleteErr := s.repository.SoftDelete(ctx, id, userID)
		objectErr := s.storage.Delete(ctx, key)
		if deleteErr != nil {
			deleteErr = fmt.Errorf("не удалось мягко удалить аватарку после ошибки публикации: %w", deleteErr)
		}
		if objectErr != nil {
			objectErr = fmt.Errorf("не удалось удалить объект аватарки после ошибки публикации: %w", objectErr)
		}
		return domain.Avatar{}, errors.Join(publishErr, deleteErr, objectErr)
	}
	return avatar, nil
}

func (s *AvatarService) Get(ctx context.Context, id, size string) (domain.Avatar, domain.Object, error) {
	avatar, err := s.repository.Get(ctx, id)
	if err != nil {
		return domain.Avatar{}, domain.Object{}, err
	}
	key := avatar.S3Key
	if size != "" && size != "original" {
		thumbnailKey, ok := avatar.ThumbnailS3Keys[size]
		if !ok {
			return domain.Avatar{}, domain.Object{}, domain.ErrNotFound
		}
		key = thumbnailKey
	}
	object, err := s.storage.Get(ctx, key)
	return avatar, object, err
}

func (s *AvatarService) Metadata(ctx context.Context, id string) (domain.Avatar, error) {
	return s.repository.Get(ctx, id)
}

func (s *AvatarService) List(ctx context.Context, userID string) ([]domain.Avatar, error) {
	return s.repository.ListByUser(ctx, userID)
}

func (s *AvatarService) Delete(ctx context.Context, id, userID string) error {
	avatar, err := s.repository.SoftDelete(ctx, id, userID)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(avatar.ThumbnailS3Keys)+1)
	keys = append(keys, avatar.S3Key)
	for _, key := range avatar.ThumbnailS3Keys {
		keys = append(keys, key)
	}

	for _, size := range []string{"100x100", "300x300"} {
		key := fmt.Sprintf("thumbnails/%s/%s.jpg", id, size)
		if !contains(keys, key) {
			keys = append(keys, key)
		}
	}
	event := domain.AvatarDeleteEvent{MessageID: uuid.NewString(), AvatarID: id, S3Keys: keys}
	if err := retry.Do(ctx, 3, 100*time.Millisecond, func() error { return s.publisher.PublishDelete(ctx, event) }); err != nil {
		publishErr := fmt.Errorf("не удалось опубликовать событие удаления аватарки: %w", err)
		var cleanupErr error
		for _, key := range keys {
			if err := s.storage.Delete(ctx, key); err != nil {
				cleanupErr = errors.Join(
					cleanupErr,
					fmt.Errorf("не удалось удалить объект %q после ошибки публикации: %w", key, err),
				)
			}
		}
		if cleanupErr == nil {
			return nil
		}
		return errors.Join(publishErr, cleanupErr)
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *AvatarService) URL(id string) string {
	return s.baseURL + "/api/v1/avatars/" + id
}

func (s *AvatarService) ThumbnailURL(id, size string) string {
	return s.URL(id) + "?size=" + size
}
