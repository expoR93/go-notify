package notify

import (
	// "fmt"
	"context"
	"errors"
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
	// wg          sync.WaitGroup
}

func NewEngine[T any](driver Driver[T], providers []Provider[T], workerCount int) (*Engine[T], error) {
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

	return &Engine[T]{
		driver:      driver,
		providers:   registry,
		workerCount: workerCount,
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
		// e.logger.Warn("Max retries reached", "event_id", event.EventID)
		e.driver.Ack(event.EventID)
		return
	}

	targetChannel := string(event.Channel)

	provider, exists := e.providers[targetChannel]
	if !exists {
		// Before Ack here we'll implement a loggin fuctionality
		// to tell the user that the provider doesn't exists.
		// In such case no Retry should be performed.
		e.driver.Ack(event.EventID)
		return
	}

	if err := provider.Send(event); err != nil {
		var provErr *ProviderError
		if errors.As(err, &provErr) && provErr.Type == ErrorPermanent {
			// Log the permanent failure and Ack to remove it from the queue
			// e.logger.Error("Permanent failure", "event", event.EventID)
			e.driver.Ack(event.EventID)
			return
		}
		// Add a random 0-100ms to the delay
		jitter := time.Duration(rand.Intn(100)) * time.Millisecond
		delay := e.calculateBackoff(event.Attempt) + jitter
		event.Attempt++
		// Log: "Send failed: %v. Retrying (Attempt %d)", err, event.Attempt
		e.driver.Nack(event, delay)
		return
	}

	e.driver.Ack(event.EventID)
}

func (e *Engine[T]) calculateBackoff(attempt int) time.Duration {
	// 1s, 2s, 4s, 8s, 16s...
	baseDelay := time.Second
	return baseDelay * time.Duration(1<<(uint(attempt)-1))
}
