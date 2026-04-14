package notify

import (
	"context"
	"time"

	// "sync"
	"testing"
)

func BenchmarkEngineProcessing(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testCh := make(chan NotificationEvent[TestPayload], b.N)

	driver := &MockDriver[TestPayload]{
		ListenFunc: func(ctx context.Context) (<-chan NotificationEvent[TestPayload], error) {
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
		testCh <- event
	}
	b.StopTimer()
}
