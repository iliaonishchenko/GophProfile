package observability

import (
	"context"
	"io"

	"github.com/iliaonishchenko/GophProfile/internal/domain"
	"go.opentelemetry.io/otel/attribute"
)

type Repository struct {
	next domain.AvatarRepository
}

func TraceRepository(next domain.AvatarRepository) *Repository {
	return &Repository{next: next}
}

func (r *Repository) Create(ctx context.Context, avatar domain.Avatar) (err error) {
	ctx, span := Start(ctx, "postgres.avatar.create", attribute.String("avatar.id", avatar.ID))
	defer func() { End(span, err) }()
	return r.next.Create(ctx, avatar)
}

func (r *Repository) Get(ctx context.Context, id string) (avatar domain.Avatar, err error) {
	ctx, span := Start(ctx, "postgres.avatar.get", attribute.String("avatar.id", id))
	defer func() { End(span, err) }()
	return r.next.Get(ctx, id)
}

func (r *Repository) ListByUser(ctx context.Context, userID string) (avatars []domain.Avatar, err error) {
	ctx, span := Start(ctx, "postgres.avatar.list", attribute.String("user.id", userID))
	defer func() { End(span, err) }()
	return r.next.ListByUser(ctx, userID)
}

func (r *Repository) SoftDelete(ctx context.Context, id, userID string) (avatar domain.Avatar, err error) {
	ctx, span := Start(ctx, "postgres.avatar.soft_delete",
		attribute.String("avatar.id", id),
		attribute.String("user.id", userID),
	)
	defer func() { End(span, err) }()
	return r.next.SoftDelete(ctx, id, userID)
}

func (r *Repository) ClaimProcessing(ctx context.Context, id string) (claimed bool, err error) {
	ctx, span := Start(ctx, "postgres.avatar.claim", attribute.String("avatar.id", id))
	defer func() { End(span, err) }()
	return r.next.ClaimProcessing(ctx, id)
}

func (r *Repository) CompleteProcessing(ctx context.Context, id string, keys map[string]string) (err error) {
	ctx, span := Start(ctx, "postgres.avatar.complete", attribute.String("avatar.id", id))
	defer func() { End(span, err) }()
	return r.next.CompleteProcessing(ctx, id, keys)
}

func (r *Repository) FailProcessing(ctx context.Context, id string) (err error) {
	ctx, span := Start(ctx, "postgres.avatar.fail", attribute.String("avatar.id", id))
	defer func() { End(span, err) }()
	return r.next.FailProcessing(ctx, id)
}

type Storage struct {
	next domain.AvatarStorage
}

func TraceStorage(next domain.AvatarStorage) *Storage {
	return &Storage{next: next}
}

func (s *Storage) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (err error) {
	ctx, span := Start(ctx, "s3.put", attribute.String("s3.key", key), attribute.Int64("s3.size", size))
	defer func() { End(span, err) }()
	return s.next.Put(ctx, key, body, size, contentType)
}

func (s *Storage) Get(ctx context.Context, key string) (object domain.Object, err error) {
	ctx, span := Start(ctx, "s3.get", attribute.String("s3.key", key))
	defer func() { End(span, err) }()
	return s.next.Get(ctx, key)
}

func (s *Storage) Delete(ctx context.Context, key string) (err error) {
	ctx, span := Start(ctx, "s3.delete", attribute.String("s3.key", key))
	defer func() { End(span, err) }()
	return s.next.Delete(ctx, key)
}

func (s *Storage) Health(ctx context.Context) (err error) {
	ctx, span := Start(ctx, "s3.health")
	defer func() { End(span, err) }()
	return s.next.Health(ctx)
}

type Publisher struct {
	next domain.MessagePublisher
}

func TracePublisher(next domain.MessagePublisher) *Publisher {
	return &Publisher{next: next}
}

func (p *Publisher) PublishUpload(ctx context.Context, event domain.AvatarUploadEvent) (err error) {
	ctx, span := Start(ctx, "rabbitmq.publish avatar.uploaded",
		attribute.String("messaging.message.id", event.MessageID),
		attribute.String("avatar.id", event.AvatarID),
	)
	defer func() { End(span, err) }()
	return p.next.PublishUpload(ctx, event)
}

func (p *Publisher) PublishDelete(ctx context.Context, event domain.AvatarDeleteEvent) (err error) {
	ctx, span := Start(ctx, "rabbitmq.publish avatar.deleted",
		attribute.String("messaging.message.id", event.MessageID),
		attribute.String("avatar.id", event.AvatarID),
	)
	defer func() { End(span, err) }()
	return p.next.PublishDelete(ctx, event)
}

func (p *Publisher) Health(ctx context.Context) (err error) {
	ctx, span := Start(ctx, "rabbitmq.health")
	defer func() { End(span, err) }()
	return p.next.Health(ctx)
}
