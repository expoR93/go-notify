package notify

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type MockProvider[T any] struct {
	SendFunc func(event NotificationEvent[T]) error
	TypeFunc func() string
}

func (m *MockProvider[T]) Send(event NotificationEvent[T]) error {
	if m.SendFunc != nil {
		return m.SendFunc(event)
	}

	return nil // Default success
}

func (m *MockProvider[T]) Type() string {
	if m.TypeFunc != nil {
		return m.TypeFunc()
	}

	return "mock"
}

type MockDriver[T any] struct {
	ListenFunc func(ctx context.Context) (<-chan NotificationEvent[T], error)
	AckFunc    func(eventID uint64) error
	NackFunc   func(event NotificationEvent[T], delay time.Duration) error
	AckCalls   int
	NackCalls  int
	LastAckID  uint64
	LastNackID uint64
	WG         *sync.WaitGroup
}

func (m *MockDriver[T]) Listen(ctx context.Context) (<-chan NotificationEvent[T], error) {
	if m.ListenFunc != nil {
		return m.ListenFunc(ctx)
	}

	return nil, nil
}

func (m *MockDriver[T]) Ack(eventID uint64) error {
	m.AckCalls++
	m.LastAckID = eventID
	if m.AckFunc != nil {
		return m.AckFunc(eventID)
	}
	if m.WG != nil {
		m.WG.Done()
	}

	return nil
}

func (m *MockDriver[T]) Nack(event NotificationEvent[T], delay time.Duration) error {
	m.NackCalls++
	m.LastNackID = event.EventID
	if m.NackFunc != nil {
		return m.NackFunc(event, delay)
	}
	if m.WG != nil {
		m.WG.Done()
	}

	return nil
}

type MockDeadLetter[T any] struct {
	HandleFunc func(event NotificationEvent[T], finalErr error) error
	DLQ        []NotificationEvent[T]
	DLQCall    int
}

func (dl *MockDeadLetter[T]) Handle(event NotificationEvent[T], finalErr error) error {
	dl.DLQCall++
	dl.DLQ = append(dl.DLQ, event)
	if dl.HandleFunc != nil {
		return dl.HandleFunc(event, finalErr)
	}

	return nil
}

func TestEngine(t *testing.T) {
	testCases := []struct {
		name         string
		inputEvent   NotificationEvent[TestPayload]
		providerErr  error
		expectedAck  bool
		expectedNack bool
		expectedDLQ  bool
	}{
		{
			name: "Routing Success",
			inputEvent: NotificationEvent[TestPayload]{
				EventID:   1,
				Channel:   ChannelEmail,
				CreatedAt: time.Now(),
				Attempt:   1,
			},
			providerErr: nil,
			expectedAck: true,
		},
		{
			name: "Permanent Failure - Drop Messages",
			inputEvent: NotificationEvent[TestPayload]{
				EventID:   2,
				Channel:   ChannelEmail,
				CreatedAt: time.Now(),
				Attempt:   1,
			},
			providerErr: &ProviderError{
				Err:  fmt.Errorf("invalid api key"),
				Type: ErrorPermanent,
			},
			expectedAck: true,
		},
		{
			name: "Transient Failure - Retry Message",
			inputEvent: NotificationEvent[TestPayload]{
				EventID:   3,
				Channel:   ChannelEmail,
				CreatedAt: time.Now(),
				Attempt:   1,
			},
			providerErr: &ProviderError{
				Err:  fmt.Errorf("transient provider failure: scheduling retry"),
				Type: ErrorTransient,
			},
			expectedNack: true,
		},
		{
			name: "Routing Miss",
			inputEvent: NotificationEvent[TestPayload]{
				EventID:   4,
				Channel:   "Slack",
				CreatedAt: time.Now(),
				Attempt:   1,
			},
			providerErr: nil,
			expectedAck: true,
		},
		{
			name: "Max Tries Wall",
			inputEvent: NotificationEvent[TestPayload]{
				EventID:   5,
				Channel:   ChannelEmail,
				CreatedAt: time.Now(),
				Attempt:   6,
			},
			providerErr: nil,
			expectedAck: true,
			expectedDLQ: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			var wg sync.WaitGroup

			testCh := make(chan NotificationEvent[TestPayload], 1)

			driver := &MockDriver[TestPayload]{
				WG: &wg,
				ListenFunc: func(ctx context.Context) (<-chan NotificationEvent[TestPayload], error) {
					return testCh, nil
				},
			}

			providers := []Provider[TestPayload]{
				&MockProvider[TestPayload]{
					SendFunc: func(ev NotificationEvent[TestPayload]) error {
						return tc.providerErr
					},
					TypeFunc: func() string { return string(ChannelEmail) },
				},
			}

			dlh := &MockDeadLetter[TestPayload]{
				HandleFunc: func(ev NotificationEvent[TestPayload], finalErr error) error {
					return finalErr
				},
			}

			engine, err := NewEngine(driver, providers, 0, nil, dlh)
			if err != nil {
				t.Errorf("Error initializing the Engine: %v", err)
			}

			engineErrors := make(chan error, 1)
			go func() {
				engineErrors <- engine.Start(ctx)
			}()

			wg.Add(1)

			testCh <- tc.inputEvent

			wg.Wait()

			close(testCh)
			cancel()

			if tc.expectedAck && driver.AckCalls == 0 {
				t.Error("Failed to Ack the message")
			}

			if tc.expectedAck && driver.LastAckID != tc.inputEvent.EventID {
				t.Errorf("Expected Ack for ID %d, got %d", tc.inputEvent.EventID, driver.LastAckID)
			}

			if tc.expectedNack && driver.NackCalls == 0 {
				t.Error("Failed to Nack the message")
			}

			if tc.expectedNack && driver.LastNackID != tc.inputEvent.EventID {
				t.Errorf("Expected Nack for ID %d, got %d", tc.inputEvent.EventID, driver.LastNackID)
			}

			if tc.expectedDLQ && dlh.DLQCall == 0 {
				t.Error("Failed to put the message in DLQ")
			}

			if tc.expectedDLQ && dlh.DLQ[(len(dlh.DLQ)-1)].EventID != tc.inputEvent.EventID {
				t.Errorf("Expected DLQ for ID %d, got %d", tc.inputEvent.EventID, dlh.DLQ[(len(dlh.DLQ)-1)].EventID)
			}
		})
	}
}

func TestEngine_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	inputEvent := NotificationEvent[TestPayload]{
		EventID:   1,
		Channel:   ChannelEmail,
		CreatedAt: time.Now(),
		Attempt:   1,
	}

	testCh := make(chan NotificationEvent[TestPayload], 1)
	startedProcessing := make(chan struct{})

	driver := &MockDriver[TestPayload]{
		ListenFunc: func(ctx context.Context) (<-chan NotificationEvent[TestPayload], error) {
			var once sync.Once
			go func() {
				<-ctx.Done()
				once.Do(func() { close(testCh) })
			}()
			return testCh, nil
		},
	}

	providers := []Provider[TestPayload]{
		&MockProvider[TestPayload]{
			SendFunc: func(ev NotificationEvent[TestPayload]) error {
				startedProcessing <- struct{}{}
				time.Sleep(500 * time.Millisecond)
				return nil
			},
			TypeFunc: func() string { return string(ChannelEmail) },
		},
	}

	dlh := &MockDeadLetter[TestPayload]{
		HandleFunc: func(ev NotificationEvent[TestPayload], finalErr error) error {
			return finalErr
		},
	}

	engine, err := NewEngine(driver, providers, 0, nil, dlh)
	if err != nil {
		t.Errorf("Error initializing the Engine: %v", err)
	}

	engineErrors := make(chan error, 1)
	go func() {
		engineErrors <- engine.Start(ctx)
	}()

	testCh <- inputEvent

	<-startedProcessing

	cancel()

	if err := <-engineErrors; err != nil {
		t.Error("Expected a graceful shutdown, the engine crashed instead")
	}

	if driver.AckCalls != 1 {
		t.Errorf("Expected AckCalls to be 1, got %v instead", driver.AckCalls)
	}
}
