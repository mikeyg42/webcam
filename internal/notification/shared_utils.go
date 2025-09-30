package notification

import (
	"context"
	"time"
)


// SendWithRetry executes a function with retry logic using the specified retry configuration
func SendWithRetry(ctx context.Context, config RetryConfig, sendFunc func(context.Context) error) error {
	var lastErr error

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Check context before each attempt
		if err := ctx.Err(); err != nil {
			return err
		}

		// Add delay before retry (except first attempt)
		if attempt > 0 {
			delay := time.Duration(attempt) * config.Delay
			if delay > config.MaxDelay {
				delay = config.MaxDelay
			}

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Execute the function
		if err := sendFunc(ctx); err == nil {
			return nil // Success
		} else {
			lastErr = err
		}
	}

	return lastErr
}