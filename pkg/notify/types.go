package notify

import (
	"errors"
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
