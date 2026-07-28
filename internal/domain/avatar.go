package domain

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrNotFound     = errors.New("аватарка не найдена")
	ErrForbidden    = errors.New("доступ запрещён")
	ErrInvalidImage = errors.New("недопустимый формат изображения")
	ErrFileTooLarge = errors.New("файл слишком большой")
)

const (
	UploadStatusUploaded      = "uploaded"
	ProcessingStatusPending   = "pending"
	ProcessingStatusRunning   = "processing"
	ProcessingStatusCompleted = "completed"
	ProcessingStatusFailed    = "failed"
)

type Avatar struct {
	ID               string
	UserID           string
	FileName         string
	MIMEType         string
	SizeBytes        int64
	S3Key            string
	ThumbnailS3Keys  map[string]string
	UploadStatus     string
	ProcessingStatus string
	Width            int
	Height           int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

type Object struct {
	Body        io.ReadCloser
	Size        int64
	ContentType string
	ETag        string
}

type AvatarRepository interface {
	Create(context.Context, Avatar) error
	Get(context.Context, string) (Avatar, error)
	ListByUser(context.Context, string) ([]Avatar, error)
	SoftDelete(context.Context, string, string) (Avatar, error)
	ClaimProcessing(context.Context, string) (bool, error)
	CompleteProcessing(context.Context, string, map[string]string) error
	FailProcessing(context.Context, string) error
}

type AvatarStorage interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (Object, error)
	Delete(context.Context, string) error
	Health(context.Context) error
}

type MessagePublisher interface {
	PublishUpload(context.Context, AvatarUploadEvent) error
	PublishDelete(context.Context, AvatarDeleteEvent) error
	Health(context.Context) error
}

type AvatarUploadEvent struct {
	MessageID string `json:"message_id"`
	AvatarID  string `json:"avatar_id"`
	UserID    string `json:"user_id"`
	S3Key     string `json:"s3_key"`
}

type ProcessingOp struct {
	Size   string `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type AvatarProcessEvent struct {
	MessageID  string         `json:"message_id"`
	AvatarID   string         `json:"avatar_id"`
	Operations []ProcessingOp `json:"operations"`
}

type AvatarDeleteEvent struct {
	MessageID string   `json:"message_id"`
	AvatarID  string   `json:"avatar_id"`
	S3Keys    []string `json:"s3_keys"`
}
