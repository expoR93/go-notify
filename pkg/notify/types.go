package notify

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/sony/sonyflake"
)

type ChannelType string

const (
	ChannelEmail ChannelType = "email"
	ChannelSMS   ChannelType = "sms"
	ChannelPush  ChannelType = "push"
)

type ErrorType int

const (
	ErrorTransient ErrorType = iota
	ErrorPermanent
	ErrorInvalidData
)

var (
	errEventIDZero          = errors.New("event_id cannot be zero")
	errEventAttemptZero     = errors.New("event_attempt cannot be zero")
	errEventChannelEmpty    = errors.New("event_channel cannot be empty")
	errEventCreatedAtZero   = errors.New("event_createdat cannot be zero")
	errEventCreatedAtFuture = errors.New("event_createdat cannot be further in the future (5s+), potential serialization error or a severely de-synchronized clock")
	ErrCircuitOpen          = errors.New("circuit breaker is open")
)

type ValidationError struct {
	Err  error
	Type ErrorType
}

func (v *ValidationError) Error() string {
	return v.Err.Error()
}

type ProviderError struct {
	Err  error
	Type ErrorType
}

func (e *ProviderError) Error() string {
	return e.Err.Error()
}

type Config struct {
	MachineID uint16
}

type NotificationEvent[T any] struct {
	// Identity & Routing (Infrastructure)
	EventID   uint64            `json:"event_id"`
	Channel   ChannelType       `json:"channel"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at,omitempty"`
	Attempt   int               `json:"attempt"`
	Metadata  map[string]string `json:"metadata"`
	Payload   T                 `json:"payload"`
}

func (e *NotificationEvent[T]) Validate(now time.Time) error {
	if e.EventID == 0 {
		return &ValidationError{
			Err:  errEventIDZero,
			Type: ErrorInvalidData,
		}
	}
	if e.Attempt == 0 {
		return &ValidationError{
			Err:  errEventAttemptZero,
			Type: ErrorInvalidData,
		}
	}
	if e.Channel == "" {
		return &ValidationError{
			Err:  errEventChannelEmpty,
			Type: ErrorInvalidData,
		}
	}
	if e.CreatedAt.IsZero() {
		return &ValidationError{
			Err:  errEventCreatedAtZero,
			Type: ErrorInvalidData,
		}
	}
	if e.CreatedAt.After(now.Add(5 * time.Second)) {
		return &ValidationError{
			Err:  errEventCreatedAtFuture,
			Type: ErrorInvalidData,
		}
	}

	return nil
}

func (e *NotificationEvent[T]) IsExpired() bool {
	if e.ExpiresAt.IsZero() {
		return false // Never expires by default
	}
	return time.Now().After(e.ExpiresAt)
}

func newNotificationEvent[T any](id uint64, channel ChannelType, payload T) NotificationEvent[T] {

	return NotificationEvent[T]{
		EventID:   id,
		Channel:   channel,
		CreatedAt: time.Now(),
		Attempt:   1,
		Metadata:  nil,
		Payload:   payload,
	}
}

type Manager[T any] struct {
	flake *sonyflake.Sonyflake
}

func NewManager[T any](cfg Config) *Manager[T] {
	st := sonyflake.Settings{
		MachineID: func() (uint16, error) {
			return cfg.MachineID, nil
		},
	}

	return &Manager[T]{
		flake: sonyflake.NewSonyflake(st),
	}
}

func (m *Manager[T]) CreateEvent(channel ChannelType, payload T) (NotificationEvent[T], error) {
	id, err := m.flake.NextID()
	if err != nil {
		return NotificationEvent[T]{}, err
	}

	return newNotificationEvent(id, channel, payload), nil
}

type CircuitState int

const (
	StateClosed CircuitState = iota
	StateOpen
	StateHalfOpen
)

// CircuitBreakerProvider wraps a standard Provider to add fault tolerance.
type CircuitBreakerProvider[T any] struct {
	inner           Provider[T]
	mu              sync.RWMutex
	state           CircuitState
	failures        int
	threshold       int
	cooldown        time.Duration
	lastFailureTime time.Time
}

func (cb *CircuitBreakerProvider[T]) Send(ctx context.Context, event *NotificationEvent[T]) error {
	if !cb.allowRequest() {
		return &ProviderError{
			Err:  ErrCircuitOpen,
			Type: ErrorTransient,
		}
	}

	err := cb.inner.Send(ctx, event)
	cb.recordResult(err)
	return err
}

func (cb *CircuitBreakerProvider[T]) Ping(ctx context.Context) error {
	err := cb.inner.Ping(ctx)

	if err == nil {
		cb.mu.Lock()
		cb.state = StateClosed
		cb.failures = 0
		cb.mu.Unlock()
	}
	return err
}

func (cb *CircuitBreakerProvider[T]) allowRequest() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.state == StateClosed {
		return true
	}

	if cb.state == StateOpen {
		if time.Since(cb.lastFailureTime) > cb.cooldown {
			cb.state = StateHalfOpen
			return true // This first worker is the "probe"
		}
		return false // Cooldown hasn't passed yet
	}

	// If we are already in StateHalfOpen, block all other workers
	// until the "probe" worker calls recordResult.
	if cb.state == StateHalfOpen {
		return false
	}

	return false
}

func (cb *CircuitBreakerProvider[T]) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err == nil {
		cb.state = StateClosed
		cb.failures = 0
		return
	}

	var provErr *ProviderError
	if errors.As(err, &provErr) && provErr.Type == ErrorTransient {
		// If the probe failed during Half-Open, immediately re-open the circuit
		if cb.state == StateHalfOpen {
			cb.state = StateOpen
			cb.lastFailureTime = time.Now()
			return
		}

		cb.failures++
		cb.lastFailureTime = time.Now()
		if cb.failures >= cb.threshold {
			cb.state = StateOpen
		}
	}
}

func (cb *CircuitBreakerProvider[T]) Type() string {
	return cb.inner.Type()
}
