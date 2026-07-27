package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
	"time"

	"github.com/iliaonishchenko/GophProfile/internal/broker"
	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"github.com/iliaonishchenko/GophProfile/internal/mocks"
	"github.com/iliaonishchenko/GophProfile/internal/observability"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestWorkerProcessesUploadAndIsIdempotent(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	storage := mocks.NewMockAvatarStorage(controller)
	processor := New(repository, storage, observability.NewMetrics())
	processor.baseDelay = time.Millisecond
	avatar := domain.Avatar{
		ID:               "id-1",
		S3Key:            "original",
		ProcessingStatus: domain.ProcessingStatusPending,
	}
	event, err := json.Marshal(domain.AvatarUploadEvent{
		MessageID: "message-1",
		AvatarID:  avatar.ID,
		S3Key:     avatar.S3Key,
	})
	require.NoError(t, err)
	thumbnails := make(map[string][]byte)

	repository.EXPECT().Get(gomock.Any(), avatar.ID).Return(avatar, nil)
	repository.EXPECT().ClaimProcessing(gomock.Any(), avatar.ID).Return(true, nil)
	storage.EXPECT().
		Get(gomock.Any(), avatar.S3Key).
		Return(domain.Object{Body: io.NopCloser(bytes.NewReader(testPNG(t)))}, nil)
	storage.EXPECT().
		Put(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "image/jpeg").
		DoAndReturn(func(_ context.Context, key string, reader io.Reader, _ int64, _ string) error {
			data, readErr := io.ReadAll(reader)
			require.NoError(t, readErr)
			thumbnails[key] = data
			return nil
		}).
		Times(2)
	repository.EXPECT().
		CompleteProcessing(gomock.Any(), avatar.ID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, keys map[string]string) error {
			require.Len(t, keys, 2)
			for size, key := range keys {
				decoded, format, decodeErr := image.Decode(bytes.NewReader(thumbnails[key]))
				require.NoError(t, decodeErr)
				require.Equal(t, "jpeg", format)
				require.Equal(t, size, dimension(decoded.Bounds().Dx(), decoded.Bounds().Dy()))
			}
			return nil
		})

	require.NoError(t, processor.Handle(context.Background(), broker.RoutingKeyUploaded, event, "message-1"))

	completed := avatar
	completed.ProcessingStatus = domain.ProcessingStatusCompleted
	repository.EXPECT().Get(gomock.Any(), avatar.ID).Return(completed, nil)
	require.NoError(t, processor.Handle(context.Background(), broker.RoutingKeyUploaded, event, "message-1"))
}

func TestWorkerSkipsUploadClaimedByAnotherWorker(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	storage := mocks.NewMockAvatarStorage(controller)
	processor := New(repository, storage, observability.NewMetrics())
	event, err := json.Marshal(domain.AvatarUploadEvent{
		MessageID: "message-1",
		AvatarID:  "id-1",
		S3Key:     "original",
	})
	require.NoError(t, err)

	repository.EXPECT().
		Get(gomock.Any(), "id-1").
		Return(domain.Avatar{ID: "id-1", ProcessingStatus: domain.ProcessingStatusPending}, nil)
	repository.EXPECT().ClaimProcessing(gomock.Any(), "id-1").Return(false, nil)

	require.NoError(t, processor.Handle(context.Background(), broker.RoutingKeyUploaded, event, "message-1"))
}

func TestWorkerReturnsProcessingAndStatusErrors(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	storage := mocks.NewMockAvatarStorage(controller)
	processor := New(repository, storage, observability.NewMetrics())
	processor.baseDelay = time.Millisecond
	event, err := json.Marshal(domain.AvatarUploadEvent{
		MessageID: "message-1",
		AvatarID:  "id-1",
		S3Key:     "original",
	})
	require.NoError(t, err)
	processingErr := errors.New("хранилище недоступно")
	statusErr := errors.New("база данных недоступна")

	repository.EXPECT().
		Get(gomock.Any(), "id-1").
		Return(domain.Avatar{ID: "id-1", ProcessingStatus: domain.ProcessingStatusPending}, nil)
	repository.EXPECT().ClaimProcessing(gomock.Any(), "id-1").Return(true, nil)
	storage.EXPECT().Get(gomock.Any(), "original").Return(domain.Object{}, processingErr).Times(3)
	repository.EXPECT().FailProcessing(gomock.Any(), "id-1").Return(statusErr)

	err = processor.Handle(context.Background(), broker.RoutingKeyUploaded, event, "message-1")
	require.ErrorIs(t, err, processingErr)
	require.ErrorIs(t, err, statusErr)
}

func TestWorkerDeleteAndInvalidMessages(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	storage := mocks.NewMockAvatarStorage(controller)
	processor := New(repository, storage, observability.NewMetrics())
	processor.baseDelay = time.Millisecond
	deleteEvent, err := json.Marshal(domain.AvatarDeleteEvent{
		AvatarID: "id",
		S3Keys:   []string{"one", "two"},
	})
	require.NoError(t, err)

	storage.EXPECT().Delete(gomock.Any(), "one").Return(nil)
	storage.EXPECT().Delete(gomock.Any(), "two").Return(nil)
	require.NoError(t, processor.Handle(context.Background(), broker.RoutingKeyDeleted, deleteEvent, "message"))
	require.Error(t, processor.Handle(context.Background(), "unknown", []byte(`{}`), "message"))
	require.Error(t, processor.Handle(context.Background(), broker.RoutingKeyUploaded, []byte(`{`), "message"))
}

func TestCreateThumbnailCenterCrops(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 30, 10))
	data, err := createThumbnail(source, 16)
	require.NoError(t, err)
	result, _, err := image.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 16, 16), result.Bounds())
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	imageValue := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 60; x++ {
			imageValue.Set(x, y, color.RGBA{R: 50, G: uint8(x), B: uint8(y), A: 255})
		}
	}
	var output bytes.Buffer
	require.NoError(t, png.Encode(&output, imageValue))
	return output.Bytes()
}

func dimension(width, height int) string {
	return fmt.Sprintf("%dx%d", width, height)
}
