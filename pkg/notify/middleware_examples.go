package notify

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// LoggingMiddleware wraps event processing with detailed logging at each stage.
func LoggingMiddleware[T any](logger *slog.Logger) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) error {
			start := time.Now()

			if logger.Enabled(ctx, slog.LevelDebug) {
				logger.Debug("starting event processing",
					slog.Uint64("event_id", event.EventID),
					slog.String("channel", string(event.Channel)),
				)
			}

			err := next(ctx, event)

			if logger.Enabled(ctx, slog.LevelDebug) {
				status := "success"
				if err != nil {
					status = "failed"
				}
				logger.Debug("event processing completed",
					slog.Uint64("event_id", event.EventID),
					slog.String("status", status),
					slog.Duration("duration", time.Since(start)),
				)
			}

			return err
		}
	}
}

// MetricsMiddleware collects timing and count metrics about event processing.
// This can be used to integrate with monitoring systems.
type MetricsCollector interface {
	RecordEventProcessed(duration time.Duration, channel string, success bool)
}

func MetricsMiddleware[T any](collector MetricsCollector) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) error {
			start := time.Now()
			err := next(ctx, event)
			duration := time.Since(start)

			success := err == nil
			collector.RecordEventProcessed(duration, string(event.Channel), success)

			return err
		}
	}
}

// RateLimitMiddleware prevents processing more than a certain number of events
// per time window per channel. Returns a transient error if the rate limit is exceeded.
type ChannelRateLimiter struct {
	mu        sync.Mutex
	limits    map[string]*bucketLimiter
	maxEvents int
	window    time.Duration
}

type bucketLimiter struct {
	tokens     float64
	lastRefill time.Time
}

func NewChannelRateLimiter(maxEvents int, window time.Duration) *ChannelRateLimiter {
	return &ChannelRateLimiter{
		limits:    make(map[string]*bucketLimiter),
		maxEvents: maxEvents,
		window:    window,
	}
}

func (crl *ChannelRateLimiter) Allow(channel string) bool {
	crl.mu.Lock()
	defer crl.mu.Unlock()

	limiter, exists := crl.limits[channel]
	if !exists {
		limiter = &bucketLimiter{
			tokens:     float64(crl.maxEvents),
			lastRefill: time.Now(),
		}
		crl.limits[channel] = limiter
	}

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(limiter.lastRefill)
	refillRate := float64(crl.maxEvents) / crl.window.Seconds()
	limiter.tokens += refillRate * elapsed.Seconds()

	if limiter.tokens > float64(crl.maxEvents) {
		limiter.tokens = float64(crl.maxEvents)
	}

	limiter.lastRefill = now

	if limiter.tokens >= 1.0 {
		limiter.tokens -= 1.0
		return true
	}

	return false
}

func RateLimitMiddleware[T any](limiter *ChannelRateLimiter) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) error {
			if !limiter.Allow(string(event.Channel)) {
				return &ProviderError{
					Err:  fmt.Errorf("rate limit exceeded for channel %s", event.Channel),
					Type: ErrorTransient,
				}
			}
			return next(ctx, event)
		}
	}
}

// FilterMiddleware allows conditionally skipping event processing based on custom logic.
// If the filter function returns false, the event is acknowledged but not processed.
type EventFilter[T any] func(ctx context.Context, event *NotificationEvent[T]) bool

func FilterMiddleware[T any](filter EventFilter[T], logger *slog.Logger) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) error {
			if !filter(ctx, event) {
				if logger.Enabled(ctx, slog.LevelDebug) {
					logger.Debug("event filtered out",
						slog.Uint64("event_id", event.EventID),
						slog.String("channel", string(event.Channel)),
					)
				}
				// Silently skip processing
				return nil
			}
			return next(ctx, event)
		}
	}
}

// EnrichmentMiddleware allows modifying event metadata before processing.
type EventEnricher[T any] func(ctx context.Context, event *NotificationEvent[T]) error

func EnrichmentMiddleware[T any](enricher EventEnricher[T]) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) error {
			if err := enricher(ctx, event); err != nil {
				return &ProviderError{
					Err:  fmt.Errorf("event enrichment failed: %w", err),
					Type: ErrorTransient,
				}
			}
			return next(ctx, event)
		}
	}
}

// TimeoutMiddleware adds a timeout to event processing.
// If processing takes longer than the timeout, the context is cancelled.
func TimeoutMiddleware[T any](timeout time.Duration) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) error {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return next(ctx, event)
		}
	}
}

// RetryMiddleware wraps event processing with automatic retry logic.
// Note: This is independent of the engine's built-in retry mechanism.
type RetryConfig struct {
	MaxAttempts int
	Backoff     time.Duration
}

func RetryMiddleware[T any](config RetryConfig, logger *slog.Logger) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) error {
			var lastErr error

			for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
				err := next(ctx, event)
				if err == nil {
					return nil
				}

				lastErr = err

				if attempt < config.MaxAttempts {
					if logger.Enabled(ctx, slog.LevelDebug) {
						logger.Debug("middleware retry scheduled",
							slog.Uint64("event_id", event.EventID),
							slog.Int("attempt", attempt),
							slog.Duration("backoff", config.Backoff),
						)
					}

					select {
					case <-time.After(config.Backoff):
						// Continue to next attempt
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}

			return lastErr
		}
	}
}

// TracingMiddleware adds trace context propagation for distributed tracing systems.
// This is a simple example; real implementations would integrate with OpenTelemetry, Jaeger, etc.
func TracingMiddleware[T any](logger *slog.Logger) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, event *NotificationEvent[T]) error {
			// Extract or generate a trace ID
			traceID := fmt.Sprintf("%d-%d", event.EventID, time.Now().UnixNano())

			// Add to context (in real scenario, this would use OpenTelemetry)
			ctx = context.WithValue(ctx, "trace_id", traceID)

			if logger.Enabled(ctx, slog.LevelDebug) {
				logger.Debug("trace started",
					slog.String("trace_id", traceID),
					slog.Uint64("event_id", event.EventID),
				)
			}

			err := next(ctx, event)

			if logger.Enabled(ctx, slog.LevelDebug) {
				logger.Debug("trace completed",
					slog.String("trace_id", traceID),
				)
			}

			return err
		}
	}
}
