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

type Engine[T any] struct {
	driver      Driver[T]
	providers   map[string]Provider[T]
	workerCount int
	startTime   time.Time
	logger      *slog.Logger
}

func NewEngine[T any](driver Driver[T], providers []Provider[T], workerCount int, logger *slog.Logger) (*Engine[T], error) {
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
		driver:      driver,
		providers:   registry,
		workerCount: workerCount,
		logger:      logger,
	}, nil
}

func (e *Engine[T]) Start(ctx context.Context) error {
	e.startTime = time.Now()

	events, err := e.driver.Listen(ctx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	for i := 0; i < e.workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-events:
					if !ok {
						return
					}
					e.processEvent(event)
				}
			}
		}()
	}

	wg.Wait()

	return nil
}

func (e *Engine[T]) processEvent(event NotificationEvent[T]) {
	if event.Attempt > MaxRetries {
		// Here we would trigger a DLQ (Dead Letter Queue) move
		// For now, we log it and Ack to kill the infinite loop
		e.logger.Warn("max retries reached",
			slog.Uint64("event_id", event.EventID),
			slog.Int("attempts", event.Attempt))
		e.driver.Ack(event.EventID)
		return
	}

	targetChannel := string(event.Channel)

	provider, exists := e.providers[targetChannel]
	if !exists {
		e.logger.Error("routing failed: no provider registered for channel",
			slog.String("channel", targetChannel),
			slog.Uint64("event_id", event.EventID),
			slog.String("suggestion", "check NewEngine provider slice initialization"),
		)
		e.driver.Ack(event.EventID)
		return
	}

	if err := provider.Send(event); err != nil {
		var provErr *ProviderError

		// Handle Permanent Failures (Invalid API Key, Bad Request, etc.)
		if errors.As(err, &provErr) && provErr.Type == ErrorPermanent {
			e.logger.Error("permanent provider failure: dropping message",
				slog.Uint64("event_id", event.EventID),
				slog.String("channel", string(event.Channel)),
				slog.String("error", err.Error()),
			)
			e.driver.Ack(event.EventID)
			return
		}

		// Handle Transient Failures (Network timeout, 500 Internal Error, etc.)
		jitter := time.Duration(rand.Intn(100)) * time.Millisecond
		delay := e.calculateBackoff(event.Attempt) + jitter

		event.Attempt++

		e.logger.Warn("transient provider failure: scheduling retry",
			slog.Uint64("event_id", event.EventID),
			slog.Int("attempt", event.Attempt),
			slog.Duration("backoff_delay", delay),
			slog.String("error", err.Error()),
		)

		e.driver.Nack(event, delay)
		return
	}

	e.logger.Info("notification sent successfully",
		slog.Uint64("event_id", event.EventID),
		slog.String("channel", string(event.Channel)),
		slog.Duration("duration", time.Since(event.CreatedAt)), // Performance metric!
	)

	e.driver.Ack(event.EventID)
}

func (e *Engine[T]) calculateBackoff(attempt int) time.Duration {
	// 1s, 2s, 4s, 8s, 16s...
	baseDelay := time.Second
	return baseDelay * time.Duration(1<<(uint(attempt)-1))
}
