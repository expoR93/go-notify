package notify

import (
	"context"
	"log/slog"
)

// Handler defines the function signature for processing a notification.
type Handler[T any] func(ctx context.Context, event *NotificationEvent[T]) error

// Middleware defines the function signature for wrapping a Handler.
// A middleware function receives a Handler and returns a new Handler that wraps it.
// This allows building composable layers of processing logic around event handling.
//
// Example usage:
//
//	loggingMiddleware := func(next Handler[MyPayload]) Handler[MyPayload] {
//	    return func(ctx context.Context, event *NotificationEvent[MyPayload]) error {
//	        log.Println("Processing event:", event.EventID)
//	        err := next(ctx, event)
//	        log.Println("Completed event:", event.EventID)
//	        return err
//	    }
//	}
type Middleware[T any] func(Handler[T]) Handler[T]

// Chain composes multiple middleware into a single middleware by applying them
// in reverse order (right to left). This means the first middleware in the slice
// will be the outermost wrapper.
//
// Example:
//
//	middlewares := []Middleware[MyPayload]{
//	    RateLimitMiddleware,
//	    LoggingMiddleware,
//	    MetricsMiddleware,
//	}
//	handler := baseHandler
//	composedMiddleware := Chain(middlewares...)
//	wrappedHandler := composedMiddleware(handler)
//
// The resulting handler will execute: RateLimit -> Logging -> Metrics -> baseHandler
func Chain[T any](middlewares ...Middleware[T]) Middleware[T] {
	return func(handler Handler[T]) Handler[T] {
		// Apply middlewares in reverse order so they execute left-to-right
		for i := len(middlewares) - 1; i >= 0; i-- {
			handler = middlewares[i](handler)
		}
		return handler
	}
}

// RecoveryMiddleware is a built-in middleware that recovers from panics
// during event processing and logs them without crashing the worker.
func RecoveryMiddleware[T any](logger *slog.Logger) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) (err error) {
			defer func() {
				if r := recover(); r != nil {
					if logger.Enabled(ctx, slog.LevelError) {
						logger.Error("panic recovered while processing event",
							slog.Uint64("event_id", event.EventID),
							slog.Any("panic", r),
						)
					}
					// Convert panic to error return
					err = &ProviderError{
						Err:  ErrEventExpired, // Placeholder, could be custom panic error
						Type: ErrorTransient,
					}
				}
			}()
			return next(ctx, event)
		}
	}
}
