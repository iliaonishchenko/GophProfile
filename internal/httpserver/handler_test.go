package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iliaonishchenko/GophProfile/internal/api"
	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"github.com/iliaonishchenko/GophProfile/internal/mocks"
	"github.com/iliaonishchenko/GophProfile/internal/service"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestHandlerUploadListAndGet(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	storage := mocks.NewMockAvatarStorage(controller)
	publisher := mocks.NewMockMessagePublisher(controller)
	avatarService := service.NewAvatarService(repository, storage, publisher, "")
	handler := NewHandler(avatarService, nil)
	imageData := testImage(t)
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
	publisher.EXPECT().PublishUpload(gomock.Any(), gomock.Any()).Return(nil)

	body, contentType := multipartImage(t, "file", imageData)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	handler.UploadAvatar(recorder, request, api.UploadAvatarParams{XUserID: "user-1"})
	require.Equal(t, http.StatusCreated, recorder.Code)
	var created api.Avatar
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	require.Equal(t, saved.ID, created.Id.String())

	repository.EXPECT().ListByUser(gomock.Any(), "user-1").Return([]domain.Avatar{saved}, nil)
	listRecorder := httptest.NewRecorder()
	handler.ListAvatars(
		listRecorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/avatars", nil),
		api.ListAvatarsParams{XUserID: "user-1"},
	)
	require.Equal(t, http.StatusOK, listRecorder.Code)

	repository.EXPECT().Get(gomock.Any(), saved.ID).Return(saved, nil)
	storage.EXPECT().
		Get(gomock.Any(), saved.S3Key).
		Return(domain.Object{
			Body:        io.NopCloser(bytes.NewReader(imageData)),
			Size:        int64(len(imageData)),
			ContentType: "image/png",
			ETag:        "abc",
		}, nil)
	getRecorder := httptest.NewRecorder()
	handler.GetAvatar(
		getRecorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
		created.Id,
		api.GetAvatarParams{},
	)
	require.Equal(t, http.StatusOK, getRecorder.Code)
	require.Equal(t, "public, max-age=86400", getRecorder.Header().Get("Cache-Control"))
	require.Equal(t, `"abc"`, getRecorder.Header().Get("ETag"))
}

func TestHandlerUploadErrorsAndForbiddenDelete(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	handler := NewHandler(
		service.NewAvatarService(
			repository,
			mocks.NewMockAvatarStorage(controller),
			mocks.NewMockMessagePublisher(controller),
			"",
		),
		nil,
	)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", bytes.NewBufferString("broken"))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=missing")
	recorder := httptest.NewRecorder()
	handler.UploadAvatar(recorder, request, api.UploadAvatarParams{XUserID: "user"})
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var formError api.ErrorWithDetails
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &formError))
	require.Equal(t, "Некорректная multipart-форма", formError.Error)
	require.Equal(t, "Тело запроса должно быть в формате multipart/form-data", formError.Details)

	id := api.AvatarId([16]byte{1})
	repository.EXPECT().
		SoftDelete(gomock.Any(), id.String(), "other").
		Return(domain.Avatar{}, domain.ErrForbidden)
	deleteRecorder := httptest.NewRecorder()
	handler.DeleteAvatar(
		deleteRecorder,
		httptest.NewRequest(http.MethodDelete, "/", nil),
		id,
		api.DeleteAvatarParams{XUserID: "other"},
	)
	require.Equal(t, http.StatusForbidden, deleteRecorder.Code)
	var forbiddenError api.ErrorWithDetails
	require.NoError(t, json.Unmarshal(deleteRecorder.Body.Bytes(), &forbiddenError))
	require.Equal(t, "Доступ запрещён", forbiddenError.Error)
	require.Equal(t, "Можно удалять только собственные аватарки", forbiddenError.Details)
}

func TestHandlerInvalidAvatarIDFromRepository(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockAvatarRepository(controller)
	handler := NewHandler(
		service.NewAvatarService(
			repository,
			mocks.NewMockAvatarStorage(controller),
			mocks.NewMockMessagePublisher(controller),
			"",
		),
		nil,
	)
	avatar := domain.Avatar{ID: "invalid-uuid", UserID: "user"}

	repository.EXPECT().ListByUser(gomock.Any(), "user").Return([]domain.Avatar{avatar}, nil)
	listRecorder := httptest.NewRecorder()
	handler.ListAvatars(
		listRecorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/avatars", nil),
		api.ListAvatarsParams{XUserID: "user"},
	)
	require.Equal(t, http.StatusInternalServerError, listRecorder.Code)

	id := api.AvatarId([16]byte{1})
	repository.EXPECT().Get(gomock.Any(), id.String()).Return(avatar, nil)
	metadataRecorder := httptest.NewRecorder()
	handler.GetAvatarMetadata(
		metadataRecorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
		id,
	)
	require.Equal(t, http.StatusInternalServerError, metadataRecorder.Code)
}

func multipartImage(t *testing.T, field string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(field, "avatar.png")
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body, writer.FormDataContentType()
}

func testImage(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	require.NoError(t, png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 8, 8))))
	return output.Bytes()
}
