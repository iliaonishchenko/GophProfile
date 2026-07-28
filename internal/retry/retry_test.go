package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDo(t *testing.T) {
	t.Run("успех после повторной попытки", func(t *testing.T) {
		attempts := 0
		err := Do(context.Background(), 3, time.Millisecond, func() error {
			attempts++
			if attempts < 2 {
				return errors.New("временная ошибка")
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 2, attempts)
	})

	t.Run("исчерпание попыток", func(t *testing.T) {
		expectedErr := errors.New("постоянная ошибка")
		attempts := 0
		err := Do(context.Background(), 3, time.Millisecond, func() error {
			attempts++
			return expectedErr
		})
		require.ErrorIs(t, err, expectedErr)
		require.Equal(t, 3, attempts)
	})

	t.Run("отмена контекста", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		err := Do(ctx, 3, time.Hour, func() error {
			attempts++
			cancel()
			return errors.New("временная ошибка")
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, attempts)
	})

	t.Run("некорректное число попыток", func(t *testing.T) {
		err := Do(context.Background(), 0, time.Millisecond, func() error { return nil })
		require.ErrorIs(t, err, ErrInvalidAttempts)
	})
}
