package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/iliaonishchenko/GophProfile/internal/api"
	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"github.com/iliaonishchenko/GophProfile/internal/observability"
	"github.com/iliaonishchenko/GophProfile/internal/service"
)

const multipartOverhead int64 = 1 << 20

type Handler struct {
	avatars *service.AvatarService
	health  *service.HealthService
	metrics *observability.Metrics
}

func NewHandler(
	avatars *service.AvatarService,
	health *service.HealthService,
	metrics *observability.Metrics,
) *Handler {
	return &Handler{avatars: avatars, health: health, metrics: metrics}
}

func (h *Handler) ListAvatars(w http.ResponseWriter, r *http.Request, params api.ListAvatarsParams) {
	if strings.TrimSpace(params.XUserID) == "" {
		writeError(w, http.StatusBadRequest, "Некорректный X-User-ID", "Заголовок не должен быть пустым")
		return
	}
	avatars, err := h.avatars.List(r.Context(), params.XUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Внутренняя ошибка сервера", "")
		return
	}
	response := make([]api.Avatar, 0, len(avatars))
	for _, avatar := range avatars {
		item, err := h.avatarResponse(avatar)
		if err != nil {
			slog.Error("не удалось сформировать ответ с аватаркой", "avatar_id", avatar.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "Внутренняя ошибка сервера", "")
			return
		}
		response = append(response, item)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) UploadAvatar(w http.ResponseWriter, r *http.Request, params api.UploadAvatarParams) {
	userID := strings.TrimSpace(params.XUserID)
	if userID == "" {
		writeError(w, http.StatusBadRequest, "Некорректный X-User-ID", "Заголовок не должен быть пустым")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxAvatarSize+multipartOverhead)
	if err := r.ParseMultipartForm(service.MaxAvatarSize); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(
				w,
				http.StatusRequestEntityTooLarge,
				api.FileTooLargeError{Error: "Файл слишком большой", MaxSize: service.MaxAvatarSize},
			)
			return
		}
		writeError(
			w,
			http.StatusBadRequest,
			"Некорректная multipart-форма",
			"Тело запроса должно быть в формате multipart/form-data",
		)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		file, header, err = r.FormFile("image")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "Файл обязателен", "Используйте multipart-поле 'file'")
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, service.MaxAvatarSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Не удалось прочитать файл", "Не удалось прочитать содержимое загруженного файла")
		return
	}

	avatar, err := h.avatars.Upload(r.Context(), userID, header.Filename, data)
	h.metrics.RecordUpload(err)
	if err != nil {
		handleDomainError(w, err)
		return
	}
	response, err := h.avatarResponse(avatar)
	if err != nil {
		slog.Error("не удалось сформировать ответ с аватаркой", "avatar_id", avatar.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "Внутренняя ошибка сервера", "")
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) DeleteAvatar(w http.ResponseWriter, r *http.Request, avatarID api.AvatarId, params api.DeleteAvatarParams) {
	if strings.TrimSpace(params.XUserID) == "" {
		writeError(w, http.StatusBadRequest, "Некорректный X-User-ID", "Заголовок не должен быть пустым")
		return
	}
	err := h.avatars.Delete(r.Context(), avatarID.String(), params.XUserID)
	h.metrics.RecordDelete(err)
	if err != nil {
		handleDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetAvatar(w http.ResponseWriter, r *http.Request, avatarID api.AvatarId, params api.GetAvatarParams) {
	size := "original"
	if params.Size != nil {
		size = string(*params.Size)
	}
	_, object, err := h.avatars.Get(r.Context(), avatarID.String(), size)
	if err != nil {
		handleDomainError(w, err)
		return
	}
	defer func() {
		if err := object.Body.Close(); err != nil {
			slog.Warn("не удалось закрыть поток объекта", "avatar_id", avatarID.String(), "error", err)
		}
	}()
	contentType := object.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if object.ETag != "" {
		w.Header().Set("ETag", `"`+strings.Trim(object.ETag, `"`)+`"`)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, object.Body); err != nil {
		slog.Error("не удалось отправить аватар клиенту", "avatar_id", avatarID.String(), "error", err)
	}
}

func (h *Handler) GetAvatarMetadata(w http.ResponseWriter, r *http.Request, avatarID api.AvatarId) {
	avatar, err := h.avatars.Metadata(r.Context(), avatarID.String())
	if err != nil {
		handleDomainError(w, err)
		return
	}
	id, err := uuid.Parse(avatar.ID)
	if err != nil {
		slog.Error("репозиторий вернул некорректный идентификатор аватарки", "avatar_id", avatar.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "Внутренняя ошибка сервера", "")
		return
	}
	response := api.AvatarMetadata{
		Id: id, UserId: avatar.UserID, FileName: avatar.FileName,
		MimeType: api.AvatarMetadataMimeType(avatar.MIMEType), Size: avatar.SizeBytes,
		Dimensions: api.ImageDimensions{Width: avatar.Width, Height: avatar.Height},
		Thumbnails: []api.Thumbnail{}, CreatedAt: avatar.CreatedAt, UpdatedAt: avatar.UpdatedAt,
	}
	for _, size := range []string{"100x100", "300x300"} {
		if _, ok := avatar.ThumbnailS3Keys[size]; ok {
			response.Thumbnails = append(response.Thumbnails, api.Thumbnail{
				Size: api.ThumbnailSize(size), Url: h.avatars.ThumbnailURL(avatar.ID, size),
			})
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) GetHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	result := h.health.Check(ctx)
	response := api.HealthResponse{Status: api.HealthResponseStatusHealthy}
	response.Components.Database = componentHealth(result.Database)
	response.Components.S3 = componentHealth(result.S3)
	response.Components.Broker = componentHealth(result.Broker)
	status := http.StatusOK
	if result.Database != nil || result.S3 != nil || result.Broker != nil {
		response.Status = api.HealthResponseStatusUnhealthy
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, response)
}

func (h *Handler) avatarResponse(avatar domain.Avatar) (api.Avatar, error) {
	id, err := uuid.Parse(avatar.ID)
	if err != nil {
		return api.Avatar{}, fmt.Errorf("некорректный идентификатор аватарки %q: %w", avatar.ID, err)
	}

	var status api.AvatarStatus
	switch avatar.ProcessingStatus {
	case domain.ProcessingStatusCompleted:
		status = api.Ready
	case domain.ProcessingStatusFailed:
		status = api.Failed
	default:
		status = api.Processing
	}
	return api.Avatar{
		Id: id, UserId: avatar.UserID, Url: h.avatars.URL(avatar.ID),
		Status: status, CreatedAt: avatar.CreatedAt,
	}, nil
}

func componentHealth(err error) api.ComponentHealth {
	if err == nil {
		return api.ComponentHealth{Status: api.ComponentHealthStatusHealthy}
	}
	message := err.Error()
	return api.ComponentHealth{Status: api.ComponentHealthStatusUnhealthy, Error: &message}
}

func handleDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "Аватарка не найдена", "")
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "Доступ запрещён", "Можно удалять только собственные аватарки")
	case errors.Is(err, domain.ErrInvalidImage):
		writeError(w, http.StatusBadRequest, "Недопустимый формат файла", "Поддерживаемые форматы: jpeg, png, webp")
	case errors.Is(err, domain.ErrFileTooLarge):
		writeJSON(
			w,
			http.StatusRequestEntityTooLarge,
			api.FileTooLargeError{Error: "Файл слишком большой", MaxSize: service.MaxAvatarSize},
		)
	default:
		writeError(w, http.StatusInternalServerError, "Внутренняя ошибка сервера", "")
	}
}

func writeError(w http.ResponseWriter, status int, message, details string) {
	if details != "" {
		writeJSON(w, status, api.ErrorWithDetails{Error: message, Details: details})
		return
	}
	writeJSON(w, status, api.Error{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("не удалось записать JSON-ответ", "error", err)
	}
}
