package notify

import (
	"context"
	"io"
	"log/slog"
	"time"

	// "sync"
	"testing"
)

func BenchmarkEngineProcessing(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testCh := make(chan *NotificationEvent[TestPayload], b.N)

	driver := &MockDriver[TestPayload]{
		ListenFunc: func(ctx context.Context) (<-chan *NotificationEvent[TestPayload], error) {
			return testCh, nil
		},
		AckFunc: func(id uint64) error { return nil },
	}

	providers := []Provider[TestPayload]{
		&MockProvider[TestPayload]{
			SendFunc: func(ctx context.Context, ev NotificationEvent[TestPayload]) error { return nil },
			TypeFunc: func() string { return string(ChannelEmail) },
		},
	}

	engine, _ := NewEngine(driver, providers, 0, nil, nil)

	go engine.Start(ctx)

	event := NotificationEvent[TestPayload]{
		EventID:   1,
		Channel:   ChannelEmail,
		Attempt:   1,
		CreatedAt: time.Now(),
	}

	b.ResetTimer() // Don't count setup time
	for i := 0; i < b.N; i++ {
		testCh <- &event
	}
	b.StopTimer()
}

func BenchmarkEngine_SaturationRecovery(b *testing.B) {
	ctx := context.Background()

	// Use io.Discard and an error-level handler to avoid Info-level logging allocations
	noopLogger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))

	driver := &MockDriver[TestPayload]{
		AckFunc: func(id uint64) error { return nil },
	}

	providers := []Provider[TestPayload]{
		&MockProvider[TestPayload]{
			SendFunc: func(ctx context.Context, ev NotificationEvent[TestPayload]) error { return nil },
			TypeFunc: func() string { return string(ChannelEmail) },
		},
	}

	dlh := &MockDeadLetter[TestPayload]{}

	engine, _ := NewEngine(driver, providers, 1, noopLogger, dlh)

	// Pre-generate batch...
	batchSize := 1000
	events := make([]*NotificationEvent[TestPayload], batchSize)
	for j := 0; j < batchSize; j++ {
		ev := &NotificationEvent[TestPayload]{ // Create as pointer
			EventID:   uint64(j + 1),
			Channel:   ChannelEmail,
			CreatedAt: time.Now(),
			Attempt:   1,
		}
		// ... setup expiration ...
		events[j] = ev
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, ev := range events {
			engine.processEvent(ctx, ev)
		}
	}
}
