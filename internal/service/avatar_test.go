package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
	"time"

	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"github.com/iliaonishchenko/GophProfile/internal/mocks"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestAvatarServiceUpload(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	storage := mocks.NewMockAvatarStorage(controller)
	publisher := mocks.NewMockMessagePublisher(controller)
	service := NewAvatarService(repository, storage, publisher, "http://localhost:8080/")
	imageData := pngImage(t, 32, 24)
	var saved domain.Avatar

	storage.EXPECT().
		Put(gomock.Any(), gomock.Any(), gomock.Any(), int64(len(imageData)), "image/png").
		Return(nil)
	repository.EXPECT().
		Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, avatar domain.Avatar) error {
			saved = avatar
			return nil
		})
	publisher.EXPECT().
		PublishUpload(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, event domain.AvatarUploadEvent) error {
			require.NotEmpty(t, event.MessageID)
			require.Equal(t, saved.ID, event.AvatarID)
			return nil
		})

	avatar, err := service.Upload(context.Background(), "user-1", "face.png", imageData)
	require.NoError(t, err)
	require.Equal(t, "user-1", avatar.UserID)
	require.Equal(t, "image/png", avatar.MIMEType)
	require.Equal(t, 32, avatar.Width)
	require.Equal(t, 24, avatar.Height)
	require.Equal(t, saved, avatar)
	require.Equal(t, "http://localhost:8080/api/v1/avatars/"+avatar.ID, service.URL(avatar.ID))
}

func TestAvatarServiceUploadValidationAndCleanup(t *testing.T) {
	t.Run("неподдерживаемый формат", func(t *testing.T) {
		controller := gomock.NewController(t)
		service := NewAvatarService(
			mocks.NewMockAvatarRepository(controller),
			mocks.NewMockAvatarStorage(controller),
			mocks.NewMockMessagePublisher(controller),
			"",
		)

		_, err := service.Upload(context.Background(), "user", "file.txt", []byte("not an image"))
		require.ErrorIs(t, err, domain.ErrInvalidImage)
	})

	t.Run("слишком большой файл", func(t *testing.T) {
		controller := gomock.NewController(t)
		service := NewAvatarService(
			mocks.NewMockAvatarRepository(controller),
			mocks.NewMockAvatarStorage(controller),
			mocks.NewMockMessagePublisher(controller),
			"",
		)

		_, err := service.Upload(context.Background(), "user", "large.png", make([]byte, MaxAvatarSize+1))
		require.ErrorIs(t, err, domain.ErrFileTooLarge)
	})

	t.Run("ошибка репозитория удаляет объект", func(t *testing.T) {
		controller := gomock.NewController(t)
		repository := mocks.NewMockAvatarRepository(controller)
		storage := mocks.NewMockAvatarStorage(controller)
		publisher := mocks.NewMockMessagePublisher(controller)
		service := NewAvatarService(repository, storage, publisher, "")
		imageData := pngImage(t, 4, 4)
		var objectKey string

		storage.EXPECT().
			Put(gomock.Any(), gomock.Any(), gomock.Any(), int64(len(imageData)), "image/png").
			DoAndReturn(func(_ context.Context, key string, _ io.Reader, _ int64, _ string) error {
				objectKey = key
				return nil
			})
		repository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("база данных недоступна"))
		storage.EXPECT().
			Delete(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, key string) error {
				require.Equal(t, objectKey, key)
				return nil
			})

		_, err := service.Upload(context.Background(), "user", "face.png", imageData)
		require.Error(t, err)
	})

	t.Run("ошибка публикации очищает созданные данные", func(t *testing.T) {
		controller := gomock.NewController(t)
		repository := mocks.NewMockAvatarRepository(controller)
		storage := mocks.NewMockAvatarStorage(controller)
		publisher := mocks.NewMockMessagePublisher(controller)
		service := NewAvatarService(repository, storage, publisher, "")
		imageData := pngImage(t, 4, 4)

		storage.EXPECT().
			Put(gomock.Any(), gomock.Any(), gomock.Any(), int64(len(imageData)), "image/png").
			Return(nil)
		repository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
		publisher.EXPECT().
			PublishUpload(gomock.Any(), gomock.Any()).
			Return(errors.New("брокер недоступен")).
			Times(3)
		repository.EXPECT().
			SoftDelete(gomock.Any(), gomock.Any(), "user").
			Return(domain.Avatar{}, nil)
		storage.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil)

		_, err := service.Upload(context.Background(), "user", "face.png", imageData)
		require.ErrorContains(t, err, "не удалось опубликовать событие загрузки аватарки")
	})
}

func TestAvatarServiceReadListDelete(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	storage := mocks.NewMockAvatarStorage(controller)
	publisher := mocks.NewMockMessagePublisher(controller)
	service := NewAvatarService(repository, storage, publisher, "")
	avatar := domain.Avatar{
		ID: "avatar-1", UserID: "user-1", S3Key: "original",
		ThumbnailS3Keys: map[string]string{"100x100": "thumb"},
		CreatedAt:       time.Now(), ProcessingStatus: domain.ProcessingStatusCompleted,
	}

	repository.EXPECT().Get(gomock.Any(), avatar.ID).Return(avatar, nil).Times(2)
	storage.EXPECT().
		Get(gomock.Any(), "thumb").
		Return(domain.Object{
			Body:        io.NopCloser(bytes.NewReader([]byte("thumb"))),
			Size:        5,
			ContentType: "image/jpeg",
		}, nil)
	repository.EXPECT().ListByUser(gomock.Any(), "user-1").Return([]domain.Avatar{avatar}, nil)
	repository.EXPECT().SoftDelete(gomock.Any(), avatar.ID, "user-1").Return(avatar, nil)
	publisher.EXPECT().
		PublishDelete(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, event domain.AvatarDeleteEvent) error {
			require.ElementsMatch(t, []string{
				"original", "thumb",
				"thumbnails/avatar-1/100x100.jpg",
				"thumbnails/avatar-1/300x300.jpg",
			}, event.S3Keys)
			return nil
		})

	_, object, err := service.Get(context.Background(), avatar.ID, "100x100")
	require.NoError(t, err)
	data, err := io.ReadAll(object.Body)
	require.NoError(t, err)
	require.Equal(t, []byte("thumb"), data)

	_, _, err = service.Get(context.Background(), avatar.ID, "300x300")
	require.ErrorIs(t, err, domain.ErrNotFound)

	listed, err := service.List(context.Background(), "user-1")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.NoError(t, service.Delete(context.Background(), avatar.ID, "user-1"))
	require.Equal(t, "/api/v1/avatars/avatar-1?size=100x100", service.ThumbnailURL(avatar.ID, "100x100"))
}

func TestAvatarServiceDeleteFallsBackToStorage(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	storage := mocks.NewMockAvatarStorage(controller)
	publisher := mocks.NewMockMessagePublisher(controller)
	service := NewAvatarService(repository, storage, publisher, "")
	avatar := domain.Avatar{
		ID:              "avatar-1",
		UserID:          "user-1",
		S3Key:           "original",
		ThumbnailS3Keys: map[string]string{"100x100": "thumb"},
	}

	repository.EXPECT().SoftDelete(gomock.Any(), avatar.ID, avatar.UserID).Return(avatar, nil)
	publisher.EXPECT().
		PublishDelete(gomock.Any(), gomock.Any()).
		Return(errors.New("брокер недоступен")).
		Times(3)
	storage.EXPECT().Delete(gomock.Any(), "original").Return(nil)
	storage.EXPECT().Delete(gomock.Any(), "thumb").Return(nil)
	storage.EXPECT().Delete(gomock.Any(), "thumbnails/avatar-1/100x100.jpg").Return(nil)
	storage.EXPECT().Delete(gomock.Any(), "thumbnails/avatar-1/300x300.jpg").Return(nil)

	require.NoError(t, service.Delete(context.Background(), avatar.ID, avatar.UserID))
}

func pngImage(t *testing.T, width, height int) []byte {
	t.Helper()
	imageValue := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			imageValue.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 180, A: 255})
		}
	}
	var output bytes.Buffer
	require.NoError(t, png.Encode(&output, imageValue))
	return output.Bytes()
}
