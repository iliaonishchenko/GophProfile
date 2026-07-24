package retry

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidAttempts = errors.New("число попыток должно быть положительным")

func Do(ctx context.Context, attempts int, initialDelay time.Duration, operation func() error) error {
	if attempts < 1 {
		return ErrInvalidAttempts
	}

	var lastErr error
	delay := initialDelay
	for attempt := 0; attempt < attempts; attempt++ {
		if lastErr = operation(); lastErr == nil {
			return nil
		}
		if attempt == attempts-1 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}
	return lastErr
}
