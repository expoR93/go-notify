package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMiddlewareChain(t *testing.T) {
	callOrder := []string{}

	middlewares := []Middleware[TestPayload]{
		func(next Handler[TestPayload]) Handler[TestPayload] {
			return func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
				callOrder = append(callOrder, "mw1_before")
				err := next(ctx, event)
				callOrder = append(callOrder, "mw1_after")
				return err
			}
		},
		func(next Handler[TestPayload]) Handler[TestPayload] {
			return func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
				callOrder = append(callOrder, "mw2_before")
				err := next(ctx, event)
				callOrder = append(callOrder, "mw2_after")
				return err
			}
		},
	}

	baseHandler := func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
		callOrder = append(callOrder, "handler")
		return nil
	}

	composedMW := Chain(middlewares...)
	handler := composedMW(baseHandler)

	ctx := context.Background()
	event := &NotificationEvent[TestPayload]{
		EventID:   1,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
	}

	err := handler(ctx, event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedOrder := []string{"mw1_before", "mw2_before", "handler", "mw2_after", "mw1_after"}

	if len(callOrder) != len(expectedOrder) {
		t.Fatalf("expected call order length %d, got %d: %v", len(expectedOrder), len(callOrder), callOrder)
	}

	for i, expected := range expectedOrder {
		if callOrder[i] != expected {
			t.Errorf("position %d: expected %q, got %q", i, expected, callOrder[i])
		}
	}
}

func TestLoggingMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mw := LoggingMiddleware[TestPayload](logger)

	baseHandler := func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
		return nil
	}

	handler := mw(baseHandler)
	ctx := context.Background()
	event := &NotificationEvent[TestPayload]{
		EventID:   123,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
	}

	err := handler(ctx, event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if len(output) == 0 {
		t.Error("expected logging output, got none")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	limiter := NewChannelRateLimiter(2, 1*time.Second)
	mw := RateLimitMiddleware[TestPayload](limiter)

	baseHandler := func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
		return nil
	}

	handler := mw(baseHandler)
	ctx := context.Background()

	event := &NotificationEvent[TestPayload]{
		EventID:   1,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
	}

	// First two events should succeed
	for i := 0; i < 2; i++ {
		err := handler(ctx, event)
		if err != nil {
			t.Errorf("event %d: expected no error, got %v", i, err)
		}
	}

	// Third event should be rate limited
	err := handler(ctx, event)
	if err == nil {
		t.Error("expected rate limit error, got none")
	}

	var provErr *ProviderError
	if !errors.As(err, &provErr) || provErr.Type != ErrorTransient {
		t.Errorf("expected transient error, got %v", err)
	}
}

func TestFilterMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	handlerCalled := false
	baseHandler := func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
		handlerCalled = true
		return nil
	}

	filter := func(ctx context.Context, event *NotificationEvent[TestPayload]) bool {
		return event.EventID != 999
	}

	mw := FilterMiddleware(filter, logger)
	handler := mw(baseHandler)

	ctx := context.Background()

	// Event should be filtered
	event := &NotificationEvent[TestPayload]{
		EventID:   999,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
	}

	err := handler(ctx, event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if handlerCalled {
		t.Error("expected handler to not be called when event is filtered")
	}

	// Event should pass through
	handlerCalled = false
	event.EventID = 1
	err = handler(ctx, event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !handlerCalled {
		t.Error("expected handler to be called when event passes filter")
	}
}

func TestEnrichmentMiddleware(t *testing.T) {
	enriched := false
	enricher := func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
		enriched = true
		if event.Metadata == nil {
			event.Metadata = make(map[string]string)
		}
		event.Metadata["enriched"] = "true"
		return nil
	}

	mw := EnrichmentMiddleware(enricher)

	baseHandler := func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
		if event.Metadata["enriched"] != "true" {
			t.Error("expected event to be enriched")
		}
		return nil
	}

	handler := mw(baseHandler)
	ctx := context.Background()
	event := &NotificationEvent[TestPayload]{
		EventID:   1,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
	}

	err := handler(ctx, event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !enriched {
		t.Error("expected enricher to be called")
	}
}

func TestTimeoutMiddleware(t *testing.T) {
	mw := TimeoutMiddleware[TestPayload](100 * time.Millisecond)
	baseHandler := func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
		select {
		case <-time.After(500 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	handler := mw(baseHandler)
	ctx := context.Background()
	event := &NotificationEvent[TestPayload]{
		EventID:   1,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
	}

	err := handler(ctx, event)
	if err == nil {
		t.Error("expected timeout error, got none")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestRetryMiddleware(t *testing.T) {
	callCount := atomic.Int32{}
	mw := RetryMiddleware[TestPayload](RetryConfig{
		MaxAttempts: 3,
		Backoff:     10 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	baseHandler := func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
		callCount.Add(1)
		if callCount.Load() < 3 {
			return fmt.Errorf("temporary error")
		}
		return nil
	}

	handler := mw(baseHandler)
	ctx := context.Background()
	event := &NotificationEvent[TestPayload]{
		EventID:   1,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
	}

	err := handler(ctx, event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount.Load() != 3 {
		t.Errorf("expected handler to be called 3 times, got %d", callCount.Load())
	}
}

func TestEngineWithMiddleware(t *testing.T) {
	mockProvider := &MockProvider[TestPayload]{
		TypeFunc: func() string { return "email" },
		SendFunc: func(ctx context.Context, event NotificationEvent[TestPayload]) error {
			return nil
		},
	}

	events := make(chan *NotificationEvent[TestPayload], 10)
	mockDriver := &MockDriver[TestPayload]{
		ListenFunc: func(ctx context.Context) (<-chan *NotificationEvent[TestPayload], error) {
			return events, nil
		},
		WG: &sync.WaitGroup{},
	}

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	engine, err := NewEngine(mockDriver, []Provider[TestPayload]{mockProvider}, 1, logger, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	// Add middleware that tracks if it was called
	middlewareCalled := false
	testMiddleware := func(next Handler[TestPayload]) Handler[TestPayload] {
		return func(ctx context.Context, event *NotificationEvent[TestPayload]) error {
			middlewareCalled = true
			return next(ctx, event)
		}
	}

	engine.UseMiddleware(testMiddleware)

	// Process an event
	mockDriver.WG.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		engine.Start(ctx)
	}()

	// Send event
	events <- &NotificationEvent[TestPayload]{
		EventID:   1,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
		Payload:   TestPayload{Message: "test"},
	}

	// Wait for processing
	mockDriver.WG.Wait()
	cancel()

	if !middlewareCalled {
		t.Error("expected middleware to be called")
	}

	if mockDriver.AckCalls != 1 {
		t.Errorf("expected 1 Ack call, got %d", mockDriver.AckCalls)
	}
}
