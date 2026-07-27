package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/iliaonishchenko/GophProfile/internal/domain"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	RoutingKeyUploaded = "avatar.uploaded"
	RoutingKeyDeleted  = "avatar.deleted"
)

type RabbitMQ struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	exchange   string
	queue      string
	returns    <-chan amqp.Return
	publishMu  sync.Mutex
}

func NewRabbitMQ(url, exchange, queue string) (*RabbitMQ, error) {
	connection, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("не удалось подключиться к RabbitMQ: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("не удалось открыть канал RabbitMQ: %w", err)
	}
	if err := channel.Confirm(false); err != nil {
		_ = channel.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("не удалось включить подтверждения публикации RabbitMQ: %w", err)
	}

	rabbit := &RabbitMQ{
		connection: connection,
		channel:    channel,
		exchange:   exchange,
		queue:      queue,
		returns:    channel.NotifyReturn(make(chan amqp.Return, 16)),
	}
	if err := rabbit.declare(); err != nil {
		_ = rabbit.Close()
		return nil, err
	}
	return rabbit, nil
}

func (r *RabbitMQ) declare() error {
	if err := r.channel.ExchangeDeclare(r.exchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("не удалось создать обменник RabbitMQ: %w", err)
	}
	deadLetterExchange := r.exchange + ".dlx"
	deadLetterQueue := r.queue + ".dlq"
	const deadLetterRoutingKey = "failed"
	if err := r.channel.ExchangeDeclare(deadLetterExchange, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("не удалось создать обменник недоставленных сообщений RabbitMQ: %w", err)
	}
	if _, err := r.channel.QueueDeclare(deadLetterQueue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("не удалось создать очередь недоставленных сообщений RabbitMQ: %w", err)
	}
	if err := r.channel.QueueBind(deadLetterQueue, deadLetterRoutingKey, deadLetterExchange, false, nil); err != nil {
		return fmt.Errorf("не удалось привязать очередь недоставленных сообщений RabbitMQ: %w", err)
	}
	queue, err := r.channel.QueueDeclare(r.queue, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("не удалось создать очередь RabbitMQ: %w", err)
	}
	if err := r.channel.QueueBind(queue.Name, "avatar.*", r.exchange, false, nil); err != nil {
		return fmt.Errorf("не удалось привязать очередь RabbitMQ: %w", err)
	}
	return nil
}

func (r *RabbitMQ) PublishUpload(ctx context.Context, event domain.AvatarUploadEvent) error {
	return r.publish(ctx, RoutingKeyUploaded, event.MessageID, event)
}

func (r *RabbitMQ) PublishDelete(ctx context.Context, event domain.AvatarDeleteEvent) error {
	return r.publish(ctx, RoutingKeyDeleted, event.MessageID, event)
}

func (r *RabbitMQ) publish(ctx context.Context, routingKey, messageID string, event any) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("не удалось сериализовать событие брокера: %w", err)
	}
	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, tableCarrier(headers))
	return r.publishMessage(ctx, r.exchange, routingKey, amqp.Publishing{
		Headers:      headers,
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    messageID,
		Body:         body,
	})
}

func (r *RabbitMQ) publishMessage(
	ctx context.Context,
	exchange string,
	routingKey string,
	message amqp.Publishing,
) error {
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	confirmation, err := r.channel.PublishWithDeferredConfirmWithContext(
		ctx,
		exchange,
		routingKey,
		true,
		false,
		message,
	)
	if err != nil {
		return fmt.Errorf("не удалось опубликовать событие %s: %w", routingKey, err)
	}
	if confirmation == nil {
		return errors.New("RabbitMQ не вернул объект подтверждения публикации")
	}
	for {
		select {
		case returned, ok := <-r.returns:
			if !ok {
				return errors.New("канал возврата сообщений RabbitMQ закрыт")
			}
			if returned.MessageId != message.MessageId {
				slog.Warn(
					"получен возврат другого сообщения RabbitMQ",
					"expected_message_id", message.MessageId,
					"actual_message_id", returned.MessageId,
				)
				continue
			}
			return fmt.Errorf(
				"RabbitMQ вернул сообщение %s без подходящего маршрута: %s",
				returned.MessageId,
				returned.ReplyText,
			)
		case <-confirmation.Done():
			if !confirmation.Acked() {
				return fmt.Errorf("RabbitMQ отклонил событие %s", routingKey)
			}
			return r.checkReturnedMessage(message.MessageId)
		case <-ctx.Done():
			return fmt.Errorf("не удалось дождаться подтверждения публикации RabbitMQ: %w", ctx.Err())
		}
	}
}

func (r *RabbitMQ) checkReturnedMessage(messageID string) error {
	for {
		select {
		case returned, ok := <-r.returns:
			if !ok {
				return errors.New("канал возврата сообщений RabbitMQ закрыт")
			}
			if returned.MessageId == messageID {
				return fmt.Errorf(
					"RabbitMQ вернул сообщение %s без подходящего маршрута: %s",
					returned.MessageId,
					returned.ReplyText,
				)
			}
			slog.Warn(
				"получен возврат другого сообщения RabbitMQ",
				"expected_message_id", messageID,
				"actual_message_id", returned.MessageId,
			)
		default:
			return nil
		}
	}
}

func (r *RabbitMQ) publishDeadLetter(ctx context.Context, delivery amqp.Delivery, handlerErr error) error {
	headers := delivery.Headers
	if headers == nil {
		headers = amqp.Table{}
	}
	headers["x-original-routing-key"] = delivery.RoutingKey
	headers["x-processing-error"] = handlerErr.Error()
	return r.publishMessage(ctx, r.exchange+".dlx", "failed", amqp.Publishing{
		Headers:      headers,
		ContentType:  delivery.ContentType,
		DeliveryMode: amqp.Persistent,
		MessageId:    delivery.MessageId,
		Timestamp:    delivery.Timestamp,
		Body:         delivery.Body,
	})
}

// Consume запускает постоянного потребителя worker-а до отмены контекста.
func (r *RabbitMQ) Consume(ctx context.Context, handler func(context.Context, string, []byte, string) error) error {
	if err := r.channel.Qos(1, 0, false); err != nil {
		return fmt.Errorf("не удалось настроить QoS RabbitMQ: %w", err)
	}
	deliveries, err := r.channel.Consume(r.queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("не удалось запустить потребителя RabbitMQ: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("канал доставки сообщений RabbitMQ закрыт")
			}
			messageCtx := otel.GetTextMapPropagator().Extract(ctx, tableCarrier(delivery.Headers))
			if err := handler(messageCtx, delivery.RoutingKey, delivery.Body, delivery.MessageId); err != nil {
				slog.ErrorContext(
					messageCtx,
					"обработка сообщения завершилась ошибкой",
					"message_id", delivery.MessageId,
					"routing_key", delivery.RoutingKey,
					"error", err,
				)
				if publishErr := r.publishDeadLetter(ctx, delivery, err); publishErr != nil {
					slog.Error(
						"не удалось отправить сообщение в DLQ",
						"message_id", delivery.MessageId,
						"error", publishErr,
					)
					if nackErr := delivery.Nack(false, true); nackErr != nil {
						return errors.Join(
							fmt.Errorf("не удалось опубликовать ошибочное сообщение в DLQ: %w", publishErr),
							fmt.Errorf("не удалось вернуть ошибочное сообщение в очередь: %w", nackErr),
						)
					}
					continue
				}
				if ackErr := delivery.Ack(false); ackErr != nil {
					return fmt.Errorf("не удалось подтвердить сообщение, отправленное в DLQ: %w", ackErr)
				}
				continue
			}
			if err := delivery.Ack(false); err != nil {
				return fmt.Errorf("не удалось подтвердить обработку сообщения: %w", err)
			}
		}
	}
}

type tableCarrier amqp.Table

func (c tableCarrier) Get(key string) string {
	if value, ok := c[key].(string); ok {
		return value
	}
	return ""
}

func (c tableCarrier) Set(key, value string) {
	c[key] = value
}

func (c tableCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

var _ propagation.TextMapCarrier = tableCarrier{}

func (r *RabbitMQ) Health(context.Context) error {
	if r.connection.IsClosed() || r.channel.IsClosed() {
		return errors.New("подключение к RabbitMQ закрыто")
	}
	return nil
}

func (r *RabbitMQ) Close() error {
	var result error
	if r.channel != nil && !r.channel.IsClosed() {
		result = r.channel.Close()
	}
	if r.connection != nil && !r.connection.IsClosed() {
		result = errors.Join(result, r.connection.Close())
	}
	return result
}
