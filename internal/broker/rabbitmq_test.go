package broker

import (
	"testing"

	"github.com/stretchr/testify/require"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestCheckReturnedMessage(t *testing.T) {
	t.Run("сообщение без маршрута", func(t *testing.T) {
		returns := make(chan amqp.Return, 1)
		returns <- amqp.Return{MessageId: "message-1", ReplyText: "NO_ROUTE"}
		rabbit := &RabbitMQ{returns: returns}

		err := rabbit.checkReturnedMessage("message-1")
		require.ErrorContains(t, err, "без подходящего маршрута")
	})

	t.Run("возврат другого сообщения пропускается", func(t *testing.T) {
		returns := make(chan amqp.Return, 1)
		returns <- amqp.Return{MessageId: "old-message", ReplyText: "NO_ROUTE"}
		rabbit := &RabbitMQ{returns: returns}

		require.NoError(t, rabbit.checkReturnedMessage("message-1"))
		require.Empty(t, returns)
	})

	t.Run("закрытый канал возвратов", func(t *testing.T) {
		returns := make(chan amqp.Return)
		close(returns)
		rabbit := &RabbitMQ{returns: returns}

		err := rabbit.checkReturnedMessage("message-1")
		require.ErrorContains(t, err, "канал возврата сообщений RabbitMQ закрыт")
	})
}
