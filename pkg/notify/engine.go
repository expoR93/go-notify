package notify

import (
	// "fmt"
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"runtime"
	"sync"
	"time"
)

const MaxRetries = 5

var (
	ErrEventExpired    = errors.New("notification expired")
	ErrMaxRetries      = errors.New("max retries exceeded")
	ErrProviderMissing = errors.New("no provider found for channel")
)

type Engine[T any] struct {
	driver         Driver[T]
	providers      map[string]Provider[T]
	workerCount    int
	startTime      time.Time
	logger         *slog.Logger
	deadLetterHook DeadLetterHook[T]
}

func NewEngine[T any](driver Driver[T], providers []Provider[T], workerCount int, logger *slog.Logger, dlh DeadLetterHook[T]) (*Engine[T], error) {
	if driver == nil {
		return nil, errors.New("Expected a driver, found none")
	}

	if workerCount <= 0 {
		workerCount = runtime.NumCPU()
	}

	if len(providers) == 0 {
		return nil, errors.New("Expected providers, found none")
	}

	registry := make(map[string]Provider[T])

	for _, provider := range providers {
		key := provider.Type()
		if _, ok := registry[key]; !ok {
			registry[key] = provider
		} else {
			return nil, errors.New("Only one Provider per type is currently supported")
		}
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &Engine[T]{
		driver:         driver,
		providers:      registry,
		workerCount:    workerCount,
		logger:         logger,
		deadLetterHook: dlh,
	}, nil
}

func (e *Engine[T]) Start(ctx context.Context) error {
	e.startTime = time.Now()
	reconnectAttempt := 0

	for {
		events, err := e.driver.Listen(ctx)
		if err != nil {
			reconnectAttempt++

			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}

			delay := e.calculateBackoff(reconnectAttempt)
			e.logger.Error("driver connection failed, retrying",
				slog.Int("attempt", reconnectAttempt),
				slog.Duration("retry_in", delay),
				slog.String("error", err.Error()),
			)

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
				continue
			}
		}

		reconnectAttempt = 0

		e.logger.Info("driver connection established, spawning workers",
			slog.Int("count", e.workerCount),
		)

		var wg sync.WaitGroup
		for i := 0; i < e.workerCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for event := range events {
					e.processEvent(ctx, event)
				}
			}()
		}

		wg.Wait()

		select {
		case <-ctx.Done():
			e.logger.Info("engine stopped gracefully")
			return nil
		default:
			e.logger.Warn("driver connection lost, entering reconnection loop")
		}
	}
}

func (e *Engine[T]) processEvent(ctx context.Context, event *NotificationEvent[T]) {
	if err := event.Validate(); err != nil {
		if e.logger.Enabled(ctx, slog.LevelError) {
			e.logger.Error("notification_event validation failed",
				slog.Uint64("event_id", event.EventID),
				slog.String("error", err.Error()),
			)
		}
		e.driver.Ack(event.EventID)
		return
	}

	if event.IsExpired() {
		if e.logger.Enabled(ctx, slog.LevelWarn) {
			e.logger.Warn("notification expired, routing to dead letter",
				slog.Uint64("event_id", event.EventID),
				slog.Time("expired_at", event.ExpiresAt),
			)
		}

		if e.deadLetterHook != nil {
			if err := e.deadLetterHook.Handle(event, ErrEventExpired); err != nil {
				e.logger.Error("failed to execute dead letter hook for expired event", slog.String("error", err.Error()))

				e.driver.Nack(event, e.calculateBackoff(event.Attempt))
				return
			}
		}
		e.driver.Ack(event.EventID)
		return
	}

	if event.Attempt > MaxRetries {
		if e.logger.Enabled(ctx, slog.LevelWarn) {
			e.logger.Warn("max retries reached",
				slog.Uint64("event_id", event.EventID),
				slog.Int("attempts", event.Attempt))
		}

		if e.deadLetterHook != nil {
			// Persist the failure before we Ack
			if err := e.deadLetterHook.Handle(event, ErrMaxRetries); err != nil {
				e.logger.Error("failed to execute dead letter hook", slog.String("error", err.Error()))

				e.driver.Nack(event, e.calculateBackoff(event.Attempt))
				return
			}
		}
		e.driver.Ack(event.EventID)
		return
	}

	targetChannel := string(event.Channel)

	provider, exists := e.providers[targetChannel]
	if !exists {
		if e.logger.Enabled(ctx, slog.LevelError) {
			e.logger.Error("routing failed: no provider registered for channel",
				slog.String("channel", targetChannel),
				slog.Uint64("event_id", event.EventID),
				slog.String("suggestion", "check NewEngine provider slice initialization"),
			)
		}

		e.driver.Ack(event.EventID)
		return
	}

	if err := provider.Send(ctx, event); err != nil {
		var provErr *ProviderError

		// Handle Permanent Failures (Invalid API Key, Bad Request, etc.)
		if errors.As(err, &provErr) && provErr.Type == ErrorPermanent {
			if e.logger.Enabled(ctx, slog.LevelError) {
				e.logger.Error("permanent provider failure: dropping message",
					slog.Uint64("event_id", event.EventID),
					slog.String("channel", string(event.Channel)),
					slog.String("error", err.Error()),
				)
			}

			e.driver.Ack(event.EventID)
			return
		}

		// Handle Transient Failures (Network timeout, 500 Internal Error, etc.)
		jitter := time.Duration(rand.Intn(100)) * time.Millisecond
		delay := e.calculateBackoff(event.Attempt) + jitter

		event.Attempt++

		if e.logger.Enabled(ctx, slog.LevelWarn) {
			e.logger.Warn("transient provider failure: scheduling retry",
				slog.Uint64("event_id", event.EventID),
				slog.Int("attempt", event.Attempt),
				slog.Duration("backoff_delay", delay),
				slog.String("error", err.Error()),
			)
		}

		e.driver.Nack(event, delay)
		return
	}

	if e.logger.Enabled(ctx, slog.LevelInfo) {
		e.logger.Info("notification sent successfully",
			slog.Uint64("event_id", event.EventID),
			slog.String("channel", string(event.Channel)),
			slog.Duration("duration", time.Since(event.CreatedAt)), // Performance metric!
		)
	}

	e.driver.Ack(event.EventID)
}

func (e *Engine[T]) calculateBackoff(attempt int) time.Duration {
	// 1s, 2s, 4s, 8s, 16s...
	baseDelay := time.Second
	return baseDelay * time.Duration(1<<(uint(attempt)-1))
}
