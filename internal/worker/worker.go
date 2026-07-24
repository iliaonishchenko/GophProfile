package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"time"

	"github.com/iliaonishchenko/GophProfile/internal/broker"
	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"github.com/iliaonishchenko/GophProfile/internal/retry"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Worker struct {
	repository domain.AvatarRepository
	storage    domain.AvatarStorage
	attempts   int
	baseDelay  time.Duration
}

func New(repository domain.AvatarRepository, storage domain.AvatarStorage) *Worker {
	return &Worker{repository: repository, storage: storage, attempts: 3, baseDelay: 250 * time.Millisecond}
}

func (w *Worker) Handle(ctx context.Context, routingKey string, body []byte, messageID string) error {
	switch routingKey {
	case broker.RoutingKeyUploaded:
		var event domain.AvatarUploadEvent
		if err := json.Unmarshal(body, &event); err != nil {
			return fmt.Errorf("не удалось декодировать событие загрузки: %w", err)
		}
		if event.MessageID == "" {
			event.MessageID = messageID
		}
		return w.handleUpload(ctx, event)
	case broker.RoutingKeyDeleted:
		var event domain.AvatarDeleteEvent
		if err := json.Unmarshal(body, &event); err != nil {
			return fmt.Errorf("не удалось декодировать событие удаления: %w", err)
		}
		return retry.Do(ctx, w.attempts, w.baseDelay, func() error { return w.handleDelete(ctx, event) })
	default:
		return fmt.Errorf("неподдерживаемый ключ маршрутизации %q", routingKey)
	}
}

func (w *Worker) handleUpload(ctx context.Context, event domain.AvatarUploadEvent) error {
	avatar, err := w.repository.Get(ctx, event.AvatarID)
	if err != nil {
		return err
	}
	if avatar.ProcessingStatus == domain.ProcessingStatusCompleted {
		return nil
	}
	claimed, err := w.repository.ClaimProcessing(ctx, event.AvatarID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	err = retry.Do(ctx, w.attempts, w.baseDelay, func() error { return w.processUpload(ctx, event) })
	if err == nil {
		return nil
	}
	if statusErr := w.repository.FailProcessing(ctx, event.AvatarID); statusErr != nil {
		return errors.Join(err, fmt.Errorf("не удалось отметить обработку аватарки как неуспешную: %w", statusErr))
	}
	return err
}

func (w *Worker) processUpload(ctx context.Context, event domain.AvatarUploadEvent) error {
	object, err := w.storage.Get(ctx, event.S3Key)
	if err != nil {
		return err
	}
	defer func() { _ = object.Body.Close() }()
	imageData, err := io.ReadAll(object.Body)
	if err != nil {
		return fmt.Errorf("не удалось прочитать исходное изображение аватарки: %w", err)
	}
	source, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return fmt.Errorf("не удалось декодировать исходное изображение аватарки: %w", err)
	}

	thumbnailKeys := make(map[string]string, 2)
	for _, dimension := range []int{100, 300} {
		size := fmt.Sprintf("%dx%d", dimension, dimension)
		data, err := createThumbnail(source, dimension)
		if err != nil {
			return err
		}
		key := fmt.Sprintf("thumbnails/%s/%s.jpg", event.AvatarID, size)
		if err := w.storage.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/jpeg"); err != nil {
			return err
		}
		thumbnailKeys[size] = key
	}
	return w.repository.CompleteProcessing(ctx, event.AvatarID, thumbnailKeys)
}

func (w *Worker) handleDelete(ctx context.Context, event domain.AvatarDeleteEvent) error {
	for _, key := range event.S3Keys {
		if err := w.storage.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func createThumbnail(source image.Image, size int) ([]byte, error) {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	cropSize := min(sourceWidth, sourceHeight)
	startX := bounds.Min.X + (sourceWidth-cropSize)/2
	startY := bounds.Min.Y + (sourceHeight-cropSize)/2
	cropped := image.NewRGBA(image.Rect(0, 0, cropSize, cropSize))
	draw.Draw(cropped, cropped.Bounds(), source, image.Pt(startX, startY), draw.Src)

	destination := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(destination, destination.Bounds(), cropped, cropped.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, destination, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("не удалось закодировать миниатюру %dx%d: %w", size, size, err)
	}
	return output.Bytes(), nil
}
